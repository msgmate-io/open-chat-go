package msgmate

import (
	"backend/api/user"
	wsapi "backend/api/websocket"
	"backend/database"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"log"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
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

func StartBot(host string, ch *wsapi.WebSocketHandler, username string, password string) error {
	// 'host' e.g.: 'http://localhost:1984'
	// TODO useSSL :=
	hostNoProto := strings.Replace(strings.Replace(host, "http://", "", 1), "https://", "", 1)
	ctx := context.Background() // Persistent context for the WebSocket connection

	// Login the bot
	err, sessionId := loginUser(host, user.UserLogin{
		Email:    username,
		Password: password,
	})
	if err != nil {
		return fmt.Errorf("failed to login bot: %w", err)
	}

	err, botUser := getUserInfo(host, sessionId)
	if err != nil {
		return fmt.Errorf("failed to get user info: %w", err)
	}

	chatCaneler := ChatCanceler{
		cancels: make(map[string]context.CancelFunc),
	}

	for {
		// TODO: allow also connecting to the websocket via ssl
		c, _, err := websocket.Dial(ctx, fmt.Sprintf("ws://%s/ws/connect", hostNoProto), &websocket.DialOptions{
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
		err = readWebSocketMessages(host, ch, *botUser, ctx, c, sessionId, &chatCaneler)
		if err != nil {
			log.Printf("Error reading from WebSocket: %v", err)
		}
	}
}

func parseMessage(messageType string, rawMessage json.RawMessage) (error, *wsapi.NewMessage) {
	if messageType == "new_message" {
		var message wsapi.NewMessage
		err := json.Unmarshal(rawMessage, &message)

		if err != nil {
			return err, nil
		}

		return nil, &message
	}

	return fmt.Errorf("Unsupported message type '%s'", messageType), nil
}

type ChatCanceler struct {
	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

func NewChatCanceler() *ChatCanceler {
	return &ChatCanceler{
		cancels: make(map[string]context.CancelFunc),
	}
}

func (cc *ChatCanceler) Store(chatUUID string, cancel context.CancelFunc) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	cc.cancels[chatUUID] = cancel
}

func (cc *ChatCanceler) Load(chatUUID string) (context.CancelFunc, bool) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	cf, ok := cc.cancels[chatUUID]
	return cf, ok
}

func (cc *ChatCanceler) Delete(chatUUID string) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	delete(cc.cancels, chatUUID)
}

func cancelChatResponse(chatCanceler *ChatCanceler, chatUUID string) {
	if cancel, found := chatCanceler.Load(chatUUID); found {
		cancel()
		chatCanceler.Delete(chatUUID)
	}
}

func readWebSocketMessages(
	host string,
	ch *wsapi.WebSocketHandler,
	botUser database.User,
	ctx context.Context,
	conn *websocket.Conn,
	sessionId string,
	chatCanceler *ChatCanceler, // pass your ChatCanceler in here
) error {
	// TODO: handle chats in separate goroutines
	for {
		var rawMessage json.RawMessage
		err := wsjson.Read(ctx, conn, &rawMessage)
		if err != nil {
			// Differentiating between normal disconnection and error
			if websocket.CloseStatus(err) == websocket.StatusNormalClosure ||
				websocket.CloseStatus(err) == websocket.StatusGoingAway {
				log.Println("WebSocket closed normally")
				return nil
			}
			return fmt.Errorf("read error: %w", err) // Signal upstream to reconnect
		}

		// Process the message
		err, messageType, chatUUID, senderUUID := preProcessMessage(rawMessage)

		if err != nil {
			log.Printf("Error processing message: %v", err)
			continue // Continue reading messages even if processing one fails
		}

		if senderUUID != botUser.UUID {

			if _, found := chatCanceler.Load(chatUUID); found {
				// We’re already responding to this chat.
				// You can decide what to do: skip, or maybe cancel the old one and start a new one, etc.
				log.Printf("Already responding to chat %s. Skipping or handle logic here.", chatUUID)
				continue
			}

			err, message := parseMessage(messageType, rawMessage)

			if err != nil {
				log.Printf("Error processing message: %v", err)
				continue // Continue reading messages even if processing one fails
			}

			// We may only process this message if there is not yet a context for that chat
			// that way we also avoid responding twich in one chat

			chatCtx, cancel := context.WithCancel(context.Background())

			chatCanceler.Store(chatUUID, cancel)

			go func() {
				defer chatCanceler.Delete(chatUUID)
				if err := respondMsgmate(host, chatCtx, ch, sessionId, *message); err != nil {
					log.Println("Error while respondMsgmate:", err)
					sendChatMessage(host, sessionId, chatUUID, SendMessage{
						Text: "An error occured while generating the response, please try again later",
					})
				}
			}()
		}
	}
}

func respondMsgmate(host string, ctx context.Context, ch *wsapi.WebSocketHandler, sessionId string, message wsapi.NewMessage) error {
	// 1 - first check if its a command or a plain text message
	if strings.HasPrefix(message.Content.Text, "/") {
		command := strings.Replace(message.Content.Text, "/", "", 1)
		if strings.HasPrefix(command, "pong") {
			sendChatMessage(host, sessionId, message.Content.ChatUUID, SendMessage{
				Text: fmt.Sprintf("PONG '%s' ", command),
			})
			return nil
		} else if strings.HasPrefix(command, "loop") {
			var timeSlept float32 = 0.0
			for {
				sendChatMessage(host, sessionId, message.Content.ChatUUID, SendMessage{
					Text: fmt.Sprintf("LOOP '%f' ", timeSlept),
				})
				time.Sleep(1 * time.Second)
				timeSlept += 1
				if timeSlept > 10 {
					break
				}
			}
			return nil
		}
		return fmt.Errorf("Unknown command '%s'", command)
	} else {
		// TODO: load past messgaes
		// send a message trough the websocket
		chunks, errs := streamChatCompletion(
			"http://localai:8080",
			"meta-llama-3.1-8b-instruct",
			[]map[string]string{
				{"role": "user", "content": message.Content.Text},
			})

		var fullText strings.Builder
		ch.MessageHandler.SendMessage(
			ch,
			message.Content.SenderUUID,
			ch.MessageHandler.StartPartialMessage(
				message.Content.ChatUUID,
				message.Content.SenderUUID,
			),
		)
		for {
			select {
			case <-ctx.Done():
				log.Printf("Cancellation received. Stopping response for chat %s\n", message.Content.ChatUUID)
				return ctx.Err()
			case chunk, ok := <-chunks:
				if !ok {
					chunks = nil
				} else {
					// send partial message to the user
					ch.MessageHandler.SendMessage(
						ch,
						message.Content.SenderUUID,
						ch.MessageHandler.NewPartialMessage(
							message.Content.ChatUUID,
							message.Content.SenderUUID,
							chunk,
						),
					)
					fullText.WriteString(chunk)
				}
			case err, ok := <-errs:
				if ok && err != nil {
					// Handle error
					log.Printf("streamChatCompletion error: %v", err)
					return err
				}
				errs = nil
			}

			// End when both channels are nil or closed
			if chunks == nil && errs == nil {
				break
			}
		}
		ch.MessageHandler.SendMessage(
			ch,
			message.Content.SenderUUID,
			ch.MessageHandler.EndPartialMessage(
				message.Content.ChatUUID,
				message.Content.SenderUUID,
			),
		)

		sendChatMessage(host, sessionId, message.Content.ChatUUID, SendMessage{
			Text: fullText.String(),
		})

		return nil
	}
}

func preProcessMessage(rawMessage json.RawMessage) (error, string, string, string) {
	var chatMessageTypes = []string{"new_message"}
	var messageMap map[string]interface{}
	err := json.Unmarshal(rawMessage, &messageMap)
	if err != nil {
		return err, "", "", ""
	}

	messageType := messageMap["type"].(string)

	if slices.Contains(chatMessageTypes, messageType) {
		chatUUID := (messageMap["content"].(map[string]interface{}))["chat_uuid"].(string)
		senderUUID := (messageMap["content"].(map[string]interface{}))["sender_uuid"].(string)
		return nil, messageType, chatUUID, senderUUID
	}

	return fmt.Errorf("Cannot process category"), "", "", ""

}
