package msgmate

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"slices"

	wsapi "backend/api/websocket"
	"backend/client"
)

// MessageProcessorImpl implements the MessageProcessor interface
type MessageProcessorImpl struct {
	botContext *BotContext
}

// NewMessageProcessor creates a new message processor
func NewMessageProcessor(botContext *BotContext) *MessageProcessorImpl {
	return &MessageProcessorImpl{
		botContext: botContext,
	}
}

// ProcessMessage processes a single incoming message
func (mp *MessageProcessorImpl) ProcessMessage(ctx context.Context, rawMessage []byte) error {
	// Process the message
	messageType, chatUUID, senderUUID, err := mp.PreProcessMessage(rawMessage)
	if err != nil {
		return fmt.Errorf("error processing message: %w", err)
	}

	// Skip messages from the bot itself
	if senderUUID == mp.botContext.BotUser.UUID {
		return nil
	}

	// Handle interrupt signals
	if messageType == "interrupt_signal" {
		log.Printf("Stopping response for chat %s", chatUUID)
		CancelChatResponse(mp.botContext.ChatCanceler, chatUUID)
		return nil
	}

	// Check if we're already responding to this chat
	if _, found := mp.botContext.ChatCanceler.Load(chatUUID); found {
		log.Printf("Already responding to chat %s. Skipping.", chatUUID)
		return nil
	}

	// Parse the message
	message, err := mp.parseMessage(messageType, rawMessage)
	if err != nil {
		return fmt.Errorf("error parsing message: %w", err)
	}

	// Process the message in a separate goroutine
	chatCtx, cancel := context.WithCancel(context.Background())
	mp.botContext.ChatCanceler.Store(chatUUID, cancel)

	go func() {
		defer mp.botContext.ChatCanceler.Delete(chatUUID)

		// Create AI handler and process the message
		aiHandler := NewAIHandler(mp.botContext)
		if err := aiHandler.GenerateResponse(chatCtx, *message); err != nil {
			if err != context.Canceled {
				log.Println("Error while generating response:", err)
				mp.botContext.Client.SendChatMessage(message.Content.ChatUUID, client.SendMessage{
					Text: "An error occurred while generating the response, please try again later",
				})
			}
		}
	}()

	return nil
}

// PreProcessMessage extracts message metadata
func (mp *MessageProcessorImpl) PreProcessMessage(rawMessage []byte) (messageType, chatUUID, senderUUID string, err error) {
	var chatMessageTypes = []string{"new_message", "interrupt_signal"}
	var messageMap map[string]interface{}

	err = json.Unmarshal(rawMessage, &messageMap)
	if err != nil {
		return "", "", "", err
	}

	messageType = messageMap["type"].(string)

	if slices.Contains(chatMessageTypes, messageType) {
		chatUUID = (messageMap["content"].(map[string]interface{}))["chat_uuid"].(string)
		senderUUID = (messageMap["content"].(map[string]interface{}))["sender_uuid"].(string)
		return messageType, chatUUID, senderUUID, nil
	}

	return "", "", "", fmt.Errorf("cannot process message type: %s", messageType)
}

// parseMessage parses a message based on its type
func (mp *MessageProcessorImpl) parseMessage(messageType string, rawMessage []byte) (*wsapi.NewMessage, error) {
	if messageType == "new_message" {
		var message wsapi.NewMessage
		err := json.Unmarshal(rawMessage, &message)
		if err != nil {
			return nil, err
		}
		return &message, nil
	}

	return nil, fmt.Errorf("unsupported message type '%s'", messageType)
}

// MessageProcessorFactory creates a message processor with the given context
func MessageProcessorFactory(botContext *BotContext) MessageProcessor {
	return NewMessageProcessor(botContext)
}
