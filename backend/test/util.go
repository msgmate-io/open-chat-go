package test

import (
	"backend/api/chats"
	"backend/api/contacts"
	"backend/api/federation"
	"backend/api/user"
	"backend/cmd"
	"backend/database"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"time"
)

func isServerRunning(host string) (bool, error) {
	// TODO: /_health was depricated
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

func requestNode(host string, sessionId string, nodeUUID string, data federation.RequestNode) (error, *interface{}) {
	body := new(bytes.Buffer)
	err := json.NewEncoder(body).Encode(data)
	if err != nil {
		log.Printf("Erroror encoding data: %v", err)
		return err, nil
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/api/v1/federation/nodes/%s/request", host, nodeUUID), body)
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

	// parse the response
	var response interface{}
	err = json.NewDecoder(resp.Body).Decode(&response)
	if err != nil {
		log.Printf("Error decoding response: %v", err)
		return err, nil
	}

	return nil, &response
}

func listNodes(host string, sessionId string) (error, *[]database.Node) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/federation/nodes/list", host), nil)
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

	var nodes []database.Node
	err = json.NewDecoder(resp.Body).Decode(&nodes)
	if err != nil {
		log.Printf("Error decoding response: %v", err)
		return err, nil
	}

	return nil, &nodes
}

func registerNode(host string, sessionId string, data federation.RegisterNode) (error, *database.Node) {
	body := new(bytes.Buffer)
	err := json.NewEncoder(body).Encode(data)
	if err != nil {
		log.Printf("Erroror encoding data: %v", err)
		return err, nil
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/api/v1/federation/nodes/register", host), body)
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

	var node database.Node
	err = json.NewDecoder(resp.Body).Decode(&node)
	if err != nil {
		log.Printf("Error decoding response: %v", err)
		return err, nil
	}

	return nil, &node
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
		if cmd.Int("port") != 0 {
			log.Printf("Server started on port %d", cmd.Int("port"))
			break
		}
		time.Sleep(time.Millisecond * 300)
		if time.Now().After(maxLoopTime) {
			return fmt.Errorf("Server did not start in time"), "", cancel
		}
	}

	protocol := "http"
	if cmd.Bool("ssl") {
		protocol = "https"
	}

	host := fmt.Sprintf("%s://%s:%d", protocol, cmd.String("host"), cmd.Int("port"))

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

func getFederationIdentity(host string, sessionId string) (error, *federation.IdentityResponse) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/federation/identity", host), nil)

	log.Printf("Request: %v, host: %s", req, host)
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
		return fmt.Errorf("Error response: %v", resp.Status), nil
	}

	var identityResponse federation.IdentityResponse
	err = json.NewDecoder(resp.Body).Decode(&identityResponse)
	if err != nil {
		log.Printf("Error decoding response: %v", err)
		return err, nil
	}

	return nil, &identityResponse
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
