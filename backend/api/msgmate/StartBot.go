package msgmate

import (
	"backend/api/user"
	wsapi "backend/api/websocket"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"log"
	"net/http"
	"regexp"
	"time"
)

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

type SendMessage struct {
	Text string `json:"text"`
}

func sendChatMessage(host string, sessionId string, chatUUID string, data SendMessage) error {
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

func StartBot(username string, password string) error {
	ctx := context.Background() // Persistent context for the WebSocket connection

	// Login the bot
	err, sessionId := loginUser("http://localhost:1984", user.UserLogin{
		Email:    username,
		Password: password,
	})
	if err != nil {
		return fmt.Errorf("failed to login bot: %w", err)
	}

	for {
		c, _, err := websocket.Dial(ctx, fmt.Sprintf("ws://%s/ws/connect", "localhost:1984"), &websocket.DialOptions{
			HTTPHeader: http.Header{
				"Cookie": []string{fmt.Sprintf("session_id=%s", sessionId)},
			},
		})
		if err != nil {
			log.Printf("WebSocket connection error: %v", err)
			time.Sleep(5 * time.Second) // Wait before retrying to connect
			continue                    // Retry connecting
		}

		defer c.Close(websocket.StatusNormalClosure, "closing connection") // Ensure connection closed on function termination

		log.Println("Bot connected to WebSocket")

		// Blocking call to continuously read messages
		err = readWebSocketMessages(ctx, c, sessionId)
		if err != nil {
			log.Printf("Error reading from WebSocket: %v", err)
		}
	}
}

func readWebSocketMessages(ctx context.Context, conn *websocket.Conn, sessionId string) error {
	var rawMessage json.RawMessage
	err := wsjson.Read(ctx, conn, &rawMessage)
	fmt.Errorf("GOT MESSAGE", rawMessage)
	if err != nil {
		// Differentiating between normal disconnection and error
		if websocket.CloseStatus(err) == websocket.StatusNormalClosure ||
			websocket.CloseStatus(err) == websocket.StatusGoingAway {
			log.Println("WebSocket closed normally")
			return nil // Or consider continuing to stay operational
		}
		return fmt.Errorf("read error: %w", err) // Signal upstream to reconnect
	}

	// Process the message
	if err := processMessage(rawMessage, sessionId); err != nil {
		log.Printf("Error processing message: %v", err)
	}

	return nil
}

func processMessage(rawMessage json.RawMessage, sessionId string) error {

	var messageMap map[string]interface{}
	err := json.Unmarshal(rawMessage, &messageMap)
	if err != nil {
		return err
	}

	messageType := messageMap["type"].(string)
	fmt.Printf("RAW MESSAGE TYPE: %s\n", messageType)

	if messageType == "new_message" {
		var message wsapi.NewMessage
		err = json.Unmarshal(rawMessage, &message)

		if err != nil {
			return err
		}

		sendChatMessage("http://localhost:1984", sessionId, message.Content.ChatUUID, SendMessage{
			Text: "You said: " + message.Content.Text,
		})
	}

	return nil
}
