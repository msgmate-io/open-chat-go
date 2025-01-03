package test

import (
	"backend/api/chats"
	"backend/api/contacts"
	"backend/api/user"
	wsapi "backend/api/websocket"
	"context"
	"encoding/json"
	"fmt"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"net/http"
	"strings"
	"testing"
	"time"
)

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
