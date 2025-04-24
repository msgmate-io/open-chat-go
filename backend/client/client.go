package client

import (
	"backend/api/chats"
	"backend/api/metrics"
	"backend/client/raw"
	"backend/database"
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"
	"strconv"

	"backend/api/contacts"
)

type SendMessage struct {
	Text      string                  `json:"text"`
	Reasoning []string                `json:"reasoning"`
	MetaData  *map[string]interface{} `json:"meta_data,omitempty"`
	ToolCalls *[]interface{}          `json:"tool_calls,omitempty"`
}

type Client struct {
	host      string
	sessionId string
	sealKey   []byte
	apiKeys   map[string]string
	User      database.User
}

// @doc:open-chat-provider-env-vars
// Provider credentials used by bot/tool execution are resolved from environment.
// Supported keys are OPENAI_API_KEY, DEEPINFRA_API_KEY, GROQ_API_KEY,
// LITELLM_API_KEY, and ANTHROPIC_API_KEY.
// OPEN_CHAT_SEAL_KEY is also read for payload sealing/encryption flows.
func NewClient(host string) *Client {
	sealKey := os.Getenv("OPEN_CHAT_SEAL_KEY")
	if sealKey == "" {
		sealKey = ""
	}
	apiKeys := map[string]string{
		"deepinfra": os.Getenv("DEEPINFRA_API_KEY"),
		"openai":    os.Getenv("OPENAI_API_KEY"),
		"groq":      os.Getenv("GROQ_API_KEY"),
		"litellm":   os.Getenv("LITELLM_API_KEY"),
		"anthropic": os.Getenv("ANTHROPIC_API_KEY"),
	}
	return &Client{
		host:      host,
		sessionId: "",
		sealKey:   []byte(sealKey),
		apiKeys:   apiKeys,
	}
}

func (c *Client) GetSessionId() string {
	return c.sessionId
}

func (c *Client) GetHost() string {
	return c.host
}

func (c *Client) SendChatMessage(chatUUID string, data SendMessage) error {
	body := new(bytes.Buffer)
	err := json.NewEncoder(body).Encode(data)
	if err != nil {
		log.Printf("Erroror encoding data: %v", err)
		return err
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/api/v1/chats/%s/messages/send", c.host, chatUUID), body)

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", c.host)
	req.Header.Set("Cookie", fmt.Sprintf("session_id=%s", c.sessionId))

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

func (c *Client) GetUserInfo() (error, *database.User) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/user/self", c.host), nil)
	if err != nil {
		log.Printf("Error creating request: %v", err)
		return err, nil
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", c.host)
	req.Header.Set("Cookie", fmt.Sprintf("session_id=%s", c.sessionId))

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

func (c *Client) LoginUser(username string, password string) (error, string) {
	err, sessionId := raw.RawLoginUser(c.host, username, password)
	if err != nil {
		return err, ""
	}
	c.sessionId = sessionId
	c.sealKey = []byte(password)
	err, user := c.GetUserInfo()
	if err != nil {
		return err, ""
	}
	c.User = *user
	return nil, sessionId
}

type PaginatedChats struct {
	database.Pagination
	Rows []chats.ListedChat `json:"rows"`
}

func (c *Client) SetSessionId(sessionId string) {
	c.sessionId = sessionId
}

func (c *Client) GetChats(index int64, limit int64) (error, PaginatedChats) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/chats/list", c.host), nil)
	if err != nil {
		log.Printf("Error creating request: %v", err)
		return err, PaginatedChats{}
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", c.host)
	req.Header.Set("Cookie", fmt.Sprintf("session_id=%s", c.sessionId))

	query := req.URL.Query()
	query.Add("page", strconv.FormatInt(index, 10))
	query.Add("limit", strconv.FormatInt(limit, 10))
	req.URL.RawQuery = query.Encode()

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error sending request: %v", err)
		return err, PaginatedChats{}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Error response: %v", resp.Status), PaginatedChats{}
	}

	var paginatedChats PaginatedChats
	err = json.NewDecoder(resp.Body).Decode(&paginatedChats)
	if err != nil {
		log.Printf("Error decoding response: %v", err)
		return err, PaginatedChats{}
	}

	return nil, paginatedChats
}

func (c *Client) RandomPassword() string {
	const passwordLength = 12
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

	password := make([]byte, passwordLength)
	for i := range password {
		num, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			log.Printf("Error generating random number: %v", err)
			return "password" // fallback in case of error
		}
		password[i] = charset[num.Int64()]
	}

	return string(password)
}

func (c *Client) RandomSSHPort() string {
	num, err := rand.Int(rand.Reader, big.NewInt(65535-1024))
	if err != nil {
		log.Printf("Error generating random port: %v", err)
		return "2222"
	}
	return strconv.Itoa(int(num.Int64()) + 1024)
}

func (c *Client) GetMetrics() (error, metrics.Metrics) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/metrics", c.host), nil)
	if err != nil {
		log.Printf("Error creating request: %v", err)
		return err, metrics.Metrics{}
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", c.host)
	req.Header.Set("Cookie", fmt.Sprintf("session_id=%s", c.sessionId))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error sending request: %v", err)
		return err, metrics.Metrics{}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Error response: %v", resp.Status), metrics.Metrics{}
	}

	var out metrics.Metrics
	err = json.NewDecoder(resp.Body).Decode(&out)
	if err != nil {
		log.Printf("Error decoding response: %v", err)
		return err, metrics.Metrics{}
	}

	return nil, out
}

type PaginatedMessages struct {
	database.Pagination
	Rows []chats.ListedMessage `json:"rows"`
}

func (c *Client) GetMessages(chatUUID string, index int64, limit int64) (error, PaginatedMessages) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/chats/%s/messages/list?page=%d&limit=%d", c.host, chatUUID, index, limit), nil)
	if err != nil {
		log.Printf("Error creating request: %v", err)
		return err, PaginatedMessages{}
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", c.host)
	req.Header.Set("Cookie", fmt.Sprintf("session_id=%s", c.sessionId))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error sending request: %v", err)
		return err, PaginatedMessages{}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Error response: %v", resp.Status), PaginatedMessages{}
	}

	var paginatedMessages PaginatedMessages
	err = json.NewDecoder(resp.Body).Decode(&paginatedMessages)
	if err != nil {
		log.Printf("Error decoding response: %v", err)
		return err, PaginatedMessages{}
	}

	return nil, paginatedMessages
}

func (c *Client) GetChat(chatUUID string) (error, chats.ListedChat) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/chats/%s", c.host, chatUUID), nil)
	if err != nil {
		log.Printf("Error creating request: %v", err)
		return err, chats.ListedChat{}
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", c.host)
	req.Header.Set("Cookie", fmt.Sprintf("session_id=%s", c.sessionId))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error sending request: %v", err)
		return err, chats.ListedChat{}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Error response: %v", resp.Status), chats.ListedChat{}
	}

	var chat chats.ListedChat
	err = json.NewDecoder(resp.Body).Decode(&chat)
	if err != nil {
		log.Printf("Error decoding response: %v", err)
		return err, chats.ListedChat{}
	}

	return nil, chat
}

func (c *Client) GetApiKey(keyName string) string {
	return c.apiKeys[keyName]
}

// ListContacts retrieves a paginated list of contacts for the current user
func (c *Client) ListContacts(page int64, limit int64) (error, contacts.PaginatedContacts) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/contacts/list", c.host), nil)
	if err != nil {
		log.Printf("Error creating request: %v", err)
		return err, contacts.PaginatedContacts{}
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", c.host)
	req.Header.Set("Cookie", fmt.Sprintf("session_id=%s", c.sessionId))

	query := req.URL.Query()
	query.Add("page", strconv.FormatInt(page, 10))
	query.Add("limit", strconv.FormatInt(limit, 10))
	req.URL.RawQuery = query.Encode()

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error sending request: %v", err)
		return err, contacts.PaginatedContacts{}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Error response: %v", resp.Status), contacts.PaginatedContacts{}
	}

	var paginatedContacts contacts.PaginatedContacts
	err = json.NewDecoder(resp.Body).Decode(&paginatedContacts)
	if err != nil {
		log.Printf("Error decoding response: %v", err)
		return err, contacts.PaginatedContacts{}
	}

	return nil, paginatedContacts
}

// CreateChatWithAttachments creates a new chat with the specified contact and attachments
func (c *Client) CreateChatWithAttachments(contactToken string, firstMessage string, attachments []chats.FileAttachment, sharedConfig map[string]interface{}, chatType string) (error, chats.ListedChat) {
	log.Printf("=== Client.CreateChatWithAttachments START ===")
	log.Printf("ContactToken: %s", contactToken)
	log.Printf("FirstMessage: %s", firstMessage)
	log.Printf("Attachments: %+v", attachments)
	log.Printf("ChatType: %s", chatType)

	createChatData := chats.CreateChat{
		ContactToken: contactToken,
		FirstMessage: firstMessage,
		Attachments:  attachments,
		SharedConfig: sharedConfig,
		ChatType:     chatType,
	}

	log.Printf("CreateChatData: %+v", createChatData)

	err, result := c.CreateChat(createChatData)
	log.Printf("=== Client.CreateChatWithAttachments END ===")
	return err, result
}

// CreateChat creates a new chat with the specified contact
func (c *Client) CreateChat(data interface{}) (error, chats.ListedChat) {
	body := new(bytes.Buffer)
	err := json.NewEncoder(body).Encode(data)
	if err != nil {
		log.Printf("Error encoding data: %v", err)
		return err, chats.ListedChat{}
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/api/v1/chats/create", c.host), body)
	if err != nil {
		log.Printf("Error creating request: %v", err)
		return err, chats.ListedChat{}
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", c.host)
	req.Header.Set("Cookie", fmt.Sprintf("session_id=%s", c.sessionId))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error sending request: %v", err)
		return err, chats.ListedChat{}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Error response: %v - %s", resp.Status, string(bodyBytes)), chats.ListedChat{}
	}

	var listedChat chats.ListedChat
	err = json.NewDecoder(resp.Body).Decode(&listedChat)
	if err != nil {
		log.Printf("Error decoding response: %v", err)
		return err, chats.ListedChat{}
	}

	return nil, listedChat
}
