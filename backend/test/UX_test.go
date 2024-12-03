package test

import (
	"backend/api/user"
	"backend/cmd"
	"backend/database"
	"backend/server"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
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

// go test -v ./... -run "^Test_UXFlow$"
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

	if userAInfo.Name != userA.Name {
		t.Errorf("User A name mismatch: %v", userAInfo.Name)
	}

	cancel() // Stop the server
}
