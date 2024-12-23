package test

import (
	"backend/api/chats"
	"backend/api/contacts"
	"backend/api/user"
	wsapi "backend/api/websocket"
	"backend/cmd"
	"backend/database"
	"backend/server"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

func isServerRunning(host string) (bool, error) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/_health", host), nil)
	if err != nil {
		log.Printf("Error creating request: %v", err)
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", host)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error sending request: %v", err)
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Error response: %v", resp.Status)
		return false, err
	}

	return true, nil
}

func loginUser(host string, data user.UserLogin) (error, string) {
	body := new(bytes.Buffer)
	err := json.NewEncoder(body).Encode(data)
	if err != nil {
		log.Printf("Erroror encoding data: %v", err)
		return err, ""
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/api/v1/user/login", host), body)
	if err != nil {
		log.Printf("Error creating request: %v", err)
		return err, ""
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", host)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error sending request: %v", err)
		return err, ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Error response: %v", resp.Status)
		return err, ""
	}

	cookieHeader := resp.Header.Get("Set-Cookie")
	re := regexp.MustCompile(`session_id=([^;]+)`)

	// Find the first match
	// e.g.:  session_id=877a0b36a59391125d133ba73e9edeba; Path=/; Domain=localhost; Expires=Tue, 24 Dec 2024 14:54:51 GMT; Max-Age=86400; HttpOnly; Secure; SameSite=Strict
	match := re.FindStringSubmatch(cookieHeader)
	if match != nil && len(match) > 1 {
		return nil, match[1]
	}
	return nil, match[0]
}

func registerUser(host string, data user.UserRegister) error {
	body := new(bytes.Buffer)
	err := json.NewEncoder(body).Encode(data)
	if err != nil {
		log.Printf("Erroror encoding data: %v", err)
		return err
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/api/v1/user/register", host), body)
	if err != nil {
		log.Printf("Error creating request: %v", err)
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", host)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error sending request: %v", err)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		log.Printf("Error response: %v", resp.Status)
		return err
	}

	return nil
}

func getUserInfo(host string, sessionId string) (error, *database.User) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/user/self", host), nil)
	if err != nil {
		log.Printf("Error creating request: %v", err)
		return err, nil
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", host)
	req.Header.Set("Cookie", fmt.Sprintf("session_id=%s", sessionId))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error sending request: %v", err)
		return err, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Error response: %v", resp.Status)
		return err, nil
	}

	var user database.User

	err = json.NewDecoder(resp.Body).Decode(&user)
	if err != nil {
		log.Printf("Error decoding response: %v", err)
		return err, nil
	}

	return nil, &user
}

func addContact(host string, sessionId string, data contacts.AddContact) error {
	body := new(bytes.Buffer)
	err := json.NewEncoder(body).Encode(data)
	if err != nil {
		log.Printf("Erroror encoding data: %v", err)
		return err
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/api/v1/contacts/add", host), body)

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", host)
	req.Header.Set("Cookie", fmt.Sprintf("session_id=%s", sessionId))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error sending request: %v", err)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Error response: %v", resp.Status)
	}

	return nil
}

func listContacts(host string, sessionId string) (error, *contacts.PaginatedContacts) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/contacts/list", host), nil)
	if err != nil {
		log.Printf("Error creating request: %v", err)
		return err, nil
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", host)
	req.Header.Set("Cookie", fmt.Sprintf("session_id=%s", sessionId))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error sending request: %v", err)
		return err, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Error response: %v", resp.Status)
		return err, nil
	}

	var contactsPaginated contacts.PaginatedContacts
	err = json.NewDecoder(resp.Body).Decode(&contactsPaginated)

	if err != nil {
		log.Printf("Error decoding response: %v", err)
		return err, nil
	}

	return nil, &contactsPaginated
}

func startTestServer(args []string) (error, string, context.CancelFunc) {
	cmd := cmd.ServerCli()
	os.Args = args

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		if err := cmd.Run(ctx, os.Args); err != nil {
			fmt.Fprintf(os.Stderr, "Unhandled error: %[1]v\n", err)
			os.Exit(86)
		}
	}()

	maxLoopTime := time.Now().Add(3 * time.Second)
	for {
		if server.Config != nil {
			break
		}
		time.Sleep(time.Millisecond * 300)
		if time.Now().After(maxLoopTime) {
			return fmt.Errorf("Server did not start in time"), "", cancel
		}
	}

	protocol := "http"
	if server.Config.Bool("ssl") {
		protocol = "https"
	}

	host := fmt.Sprintf("%s://%s:%d", protocol, server.Config.String("host"), server.Config.Int("port"))

	// Loop untill the server is fully started
	maxLoopTime = time.Now().Add(10 * time.Second)
	for {
		running, _ := isServerRunning(host)
		if running {
			break
		}
		time.Sleep(time.Second)

		if time.Now().After(maxLoopTime) {
			return fmt.Errorf("Server did not start in time"), host, cancel
		}
	}

	return nil, host, cancel
}

func createChat(host string, sessionId string, data chats.CreateChat) (error, *database.Chat) {
	body := new(bytes.Buffer)
	err := json.NewEncoder(body).Encode(data)
	if err != nil {
		log.Printf("Erroror encoding data: %v", err)
		return err, nil
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/api/v1/chats/create", host), body)

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", host)
	req.Header.Set("Cookie", fmt.Sprintf("session_id=%s", sessionId))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error sending request: %v", err)
		return err, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Error response: %v", resp.Status), nil
	}

	var chat database.Chat
	err = json.NewDecoder(resp.Body).Decode(&chat)

	if err != nil {
		log.Printf("Error decoding response: %v", err)
		return err, nil
	}

	return nil, &chat
}

func sendChatMessage(host string, sessionId string, chatUUID string, data chats.SendMessage) error {
	body := new(bytes.Buffer)
	err := json.NewEncoder(body).Encode(data)
	if err != nil {
		log.Printf("Erroror encoding data: %v", err)
		return err
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/api/v1/chats/%s/messages/send", host, chatUUID), body)

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", host)
	req.Header.Set("Cookie", fmt.Sprintf("session_id=%s", sessionId))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error sending request: %v", err)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Error response: %v", resp.Status)
	}

	return nil

}

// 'go test -v ./... -run "^Test_UXFlow$"'
func Test_UXFlow(t *testing.T) {
	// what used to be _scripts/simple_api_test.sh
	err, host, cancel := startTestServer([]string{"backend", "-b", "127.0.0.1", "-p", "1984", "-pp2p", "1985"})

	fmt.Println("Registering user 1")

	// Register User A
	userA := user.UserRegister{
		Name:     "User A",
		Email:    "herrduenschnlate+testA@gmail.com",
		Password: "password",
	}

	err = registerUser(host, userA)

	if err != nil {
		t.Errorf("Error registering user: %v", err)
	}

	// Register User B
	userB := user.UserRegister{
		Name:     "User B",
		Email:    "herrduenschnlate+testB@gmail.com",
		Password: "password",
	}

	err = registerUser(host, userB)

	if err != nil {
		t.Errorf("Error registering user: %v", err)
	}

	// Login User A
	err, sessionIdA := loginUser(host, user.UserLogin{
		Email:    userA.Email,
		Password: userA.Password,
	})

	if err != nil {
		t.Errorf("Error logging in user: %v", err)
	}

	fmt.Println("Session A:", sessionIdA)

	// Login User B
	err, sessionIdB := loginUser(host, user.UserLogin{
		Email:    userB.Email,
		Password: userB.Password,
	})

	if err != nil {
		t.Errorf("Error logging in user: %v", err)
	}

	fmt.Println("Session A:", sessionIdB)

	// Try fetching self info
	err, userAInfo := getUserInfo(host, sessionIdA)
	if err != nil {
		t.Errorf("Error fetching user info: %v", err)
	}

	if userAInfo == nil {
		t.Errorf("User A info is nil")
	}

	pretty, _ := json.MarshalIndent(userAInfo, "", "  ")
	fmt.Println("User A info:", string(pretty))

	err, userBInfo := getUserInfo(host, sessionIdB)
	if err != nil {
		t.Errorf("Error fetching user info: %v", err)
	}

	if userBInfo == nil {
		t.Errorf("User B info is nil")
	}

	if userAInfo.Name != userA.Name {
		t.Errorf("User A name mismatch: %v", userAInfo.Name)
	}

	err = addContact(host, sessionIdA, contacts.AddContact{
		ContactToken: userBInfo.ContactToken,
	})

	if err != nil {
		t.Errorf("Error adding contact: %v", err)
	}

	err = addContact(host, sessionIdB, contacts.AddContact{
		ContactToken: userAInfo.ContactToken,
	})

	if err != nil {
		t.Errorf("Error adding contact: %v", err)
	}

	err, contactsA := listContacts(host, sessionIdA)

	if err != nil || contactsA == nil {
		t.Errorf("Error listing contacts: %v", err)
	}

	pretty, _ = json.MarshalIndent(contactsA, "", "  ")
	fmt.Println("Contacts A:", string(pretty))

	err, contactsB := listContacts(host, sessionIdB)

	if err != nil || contactsB == nil {
		t.Errorf("Error listing contacts: %v", err)
	}

	pretty, _ = json.MarshalIndent(contactsB, "", "  ")
	fmt.Println("Contacts B:", string(pretty))

	// now we cen try connecting to the websocket

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	hostNoProtocol := strings.Replace(strings.Replace(host, "http://", "", 1), "https://", "", 1)
	fmt.Println("Connecting to websocket", hostNoProtocol)

	// create a new chat

	err, chat := createChat(host, sessionIdA, chats.CreateChat{
		ContactToken: userBInfo.ContactToken,
	})
	pretty, _ = json.MarshalIndent(chat, "", "  ")
	fmt.Println("Chat:", string(pretty))

	// ================== Websocket communication tests ==================

	c, _, err := websocket.Dial(ctx, fmt.Sprintf("ws://%s/ws/connect", hostNoProtocol), &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Cookie": []string{fmt.Sprintf("session_id=%s", sessionIdA)},
		},
	})
	if err != nil {
		t.Errorf("Error connecting to websocket")
	}
	defer c.CloseNow()

	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel2()

	// Create a results channel
	resultCh := make(chan interface{})
	errorCh := make(chan error)

	// Run the websocket reading in a separate goroutine
	go func() {
		var rawMessage json.RawMessage
		err := wsjson.Read(ctx2, c, &rawMessage)
		if err != nil {
			errorCh <- err
			return
		}

		// First attempt to unmarshal into a string
		var messageString string
		if err := json.Unmarshal(rawMessage, &messageString); err == nil {
			resultCh <- messageString
			return
		}

		// Fallback to more complex types
		var messageMap map[string]interface{}
		if err := json.Unmarshal(rawMessage, &messageMap); err == nil {
			resultCh <- messageMap
			return
		}

		// If neither unmarshal succeeded, report an error
		errorCh <- fmt.Errorf("Unsupported message type")
	}()

	// Now send a message from user B to user A
	time.Sleep(1 * time.Second)

	messageText := "Hello from user B"
	err = sendChatMessage(host, sessionIdB, chat.UUID, chats.SendMessage{
		Text: messageText,
	})

	// Wait for a message or timeout
	select {
	case msg := <-resultCh:
		pretty, _ = json.MarshalIndent(msg, "", "  ")
		fmt.Println("Message from websocket:", string(pretty))

		messageType := msg.(map[string]interface{})["type"].(string)
		jsonData, err := json.Marshal(msg)

		if err != nil {
			t.Errorf("Error marshalling message: %v", err)
		}

		if messageType != "new_message" {
			t.Errorf("Unexpected message type: %v", messageType)
		}

		var message wsapi.NewMessage
		err = json.Unmarshal(jsonData, &message)

		if err != nil {
			t.Errorf("Error unmarshalling message: %v", err)
		}

		if message.Content.Text != messageText {
			t.Errorf("Unexpected message text: %v", message.Content.Text)
		}

	case err := <-errorCh:
		t.Errorf("Error reading message from websocket: %v", err)
	case <-ctx2.Done():
		t.Errorf("Timeout waiting for message from websocket")
	}

	cancel2()
	cancel() // Stop the server
}
