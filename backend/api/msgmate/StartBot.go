package msgmate

import (
	wsapi "backend/api/websocket"
	"backend/client"
	"backend/database"
	"context"
	"encoding/json"
	"fmt"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"log"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

func StartBot(host string, ch *wsapi.WebSocketHandler, username string, password string) error {
	// 'host' e.g.: 'http://localhost:1984'
	// TODO useSSL :=
	hostNoProto := strings.Replace(strings.Replace(host, "http://", "", 1), "https://", "", 1)
	ctx := context.Background() // Persistent context for the WebSocket connection

	// Login the bot
	ocClient := client.NewClient(host)
	err, _ := ocClient.LoginUser(username, password)

	if err != nil {
		return fmt.Errorf("failed to login bot: %w", err)
	}

	err, botUser := ocClient.GetUserInfo()
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
				"Cookie": []string{fmt.Sprintf("session_id=%s", ocClient.GetSessionId())},
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
		err = readWebSocketMessages(ocClient, ch, *botUser, ctx, c, &chatCaneler)
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
	ocClient *client.Client,
	ch *wsapi.WebSocketHandler,
	botUser database.User,
	ctx context.Context,
	conn *websocket.Conn,
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
				if err := respondMsgmate(ocClient, chatCtx, ch, *message); err != nil {
					log.Println("Error while respondMsgmate:", err)
					ocClient.SendChatMessage(message.Content.ChatUUID, client.SendMessage{
						Text: "An error occured while generating the response, please try again later",
					})
				}
			}()
		}
	}
}

func respondMsgmate(ocClient *client.Client, ctx context.Context, ch *wsapi.WebSocketHandler, message wsapi.NewMessage) error {
	// 1 - first check if its a command or a plain text message
	if strings.HasPrefix(message.Content.Text, "/") {
		command := strings.Replace(message.Content.Text, "/", "", 1)
		if strings.HasPrefix(command, "pong") {
			ocClient.SendChatMessage(message.Content.ChatUUID, client.SendMessage{
				Text: fmt.Sprintf("PONG '%s' ", command),
			})
			return nil
		} else if strings.HasPrefix(command, "loop") {
			var timeSlept float32 = 0.0
			for {
				ocClient.SendChatMessage(message.Content.ChatUUID, client.SendMessage{
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

		ocClient.SendChatMessage(message.Content.ChatUUID, client.SendMessage{
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
