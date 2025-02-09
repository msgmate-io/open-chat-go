package client

import (
	"backend/api/chats"
	"backend/api/federation"
	"backend/api/metrics"
	"backend/api/tls"
	"backend/client/raw"
	"backend/database"
	"backend/server/util"
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"
	"regexp"
	"strconv"

	"github.com/libp2p/go-libp2p/core/peer"
)

type SendMessage struct {
	Text string `json:"text"`
}

type Client struct {
	host      string
	sessionId string
	sealKey   []byte
	apiKeys   map[string]string
	User      database.User
}

func NewClient(host string) *Client {
	sealKey := os.Getenv("OPEN_CHAT_SEAL_KEY")
	if sealKey == "" {
		sealKey = ""
	}
	apiKeys := map[string]string{
		"deepinfra": os.Getenv("DEEPINFRA_API_KEY"),
		"openai":    os.Getenv("OPENAI_API_KEY"),
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

func (c *Client) GetWhitelistedPeers() (error, []peer.ID) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/federation/nodes/whitelisted", c.host), nil)
	if err != nil {
		log.Printf("Error creating request: %v", err)
		return err, []peer.ID{}
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", c.host)
	req.Header.Set("Cookie", fmt.Sprintf("session_id=%s", c.sessionId))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error sending request: %v", err)
		return err, []peer.ID{}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Error response: %v", resp.Status), []peer.ID{}
	}

	var peers []peer.ID
	err = json.NewDecoder(resp.Body).Decode(&peers)
	if err != nil {
		log.Printf("Error decoding response: %v", err)
		return err, []peer.ID{}
	}

	return nil, peers
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

func (c *Client) GetFederationIdentity() (error, *federation.IdentityResponse) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/federation/identity", c.host), nil)
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
		return fmt.Errorf("Error response: %v", resp.Status), nil
	}

	var identity federation.IdentityResponse
	err = json.NewDecoder(resp.Body).Decode(&identity)
	if err != nil {
		log.Printf("Error decoding response: %v", err)
		return err, nil
	}

	return nil, &identity
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

func (c *Client) RegisterNode(name string, addresses []string, addToNetwork string) (error, *database.Node) {
	body := new(bytes.Buffer)
	err := json.NewEncoder(body).Encode(federation.RegisterNode{
		Name:         name,
		Addresses:    addresses,
		AddToNetwork: addToNetwork,
	})
	if err != nil {
		log.Printf("Error encoding data: %v", err)
		return err, nil
	}

	// print the body
	fmt.Println(string(body.Bytes()))

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/api/v1/federation/nodes/register", c.host), body)
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
		return fmt.Errorf("Error response: %v", resp.Status), nil
	}

	var node database.Node
	err = json.NewDecoder(resp.Body).Decode(&node)
	if err != nil {
		log.Printf("Error decoding response: %v", err)
		return err, nil
	}

	return nil, &node
}

func (c *Client) GetNodes(index int64, limit int64) (error, federation.PaginatedNodes) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/federation/nodes/list", c.host), nil)
	if err != nil {
		log.Printf("Error creating request: %v", err)
		return err, federation.PaginatedNodes{}
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
		return err, federation.PaginatedNodes{}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Error response: %v", resp.Status), federation.PaginatedNodes{}
	}

	var paginatedNodes federation.PaginatedNodes
	err = json.NewDecoder(resp.Body).Decode(&paginatedNodes)
	if err != nil {
		log.Printf("Error decoding response: %v", err)
		return err, federation.PaginatedNodes{}
	}

	return nil, paginatedNodes
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

func (c *Client) GetKeys(index int64, limit int64) (error, tls.PaginatedKeysResponse) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/tls/keys", c.host), nil)
	if err != nil {
		log.Printf("Error creating request: %v", err)
		return err, tls.PaginatedKeysResponse{}
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", c.host)
	req.Header.Set("Cookie", fmt.Sprintf("session_id=%s", c.sessionId))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error sending request: %v", err)
		return err, tls.PaginatedKeysResponse{}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Error response: %v", resp.Status), tls.PaginatedKeysResponse{}
	}

	var paginatedKeys tls.PaginatedKeysResponse
	err = json.NewDecoder(resp.Body).Decode(&paginatedKeys)
	if err != nil {
		log.Printf("Error decoding response: %v", err)
		return err, tls.PaginatedKeysResponse{}
	}

	return nil, paginatedKeys
}

func (c *Client) CreateProxy(direction string, origin string, target string, port string, networkName string) error {
	if networkName == "" {
		networkName = "network"
	}
	body := new(bytes.Buffer)
	err := json.NewEncoder(body).Encode(federation.CreateAndStartProxyRequest{
		Direction:     direction,
		TrafficOrigin: origin,
		TrafficTarget: target,
		Port:          port,
		Kind:          "tcp",
		NetworkName:   networkName,
	})
	if err != nil {
		log.Printf("Error encoding data: %v", err)
		return err
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/api/v1/federation/nodes/proxy", c.host), body)
	if err != nil {
		log.Printf("Error creating request: %v", err)
		return err
	}

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

	fmt.Println("Proxy created!!")

	return nil
}

func (c *Client) SolveACMEChallenge(hostname string, keyPrefix string) (error, string) {
	body := new(bytes.Buffer)
	err := json.NewEncoder(body).Encode(tls.SolveACMEChallengeRequest{
		Hostname:  hostname,
		KeyPrefix: keyPrefix,
	})
	if err != nil {
		log.Printf("Error encoding data: %v", err)
		return err, ""
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/api/v1/tls/acme/solve", c.host), body)
	if err != nil {
		log.Printf("Error creating request: %v", err)
		return err, ""
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", c.host)
	req.Header.Set("Cookie", fmt.Sprintf("session_id=%s", c.sessionId))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error sending request: %v", err)
		return err, ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Error response: %v", resp.Status), ""
	}

	return nil, ""
}

func (c *Client) RequestSessionOnRemoteNode(username string, password string, peerId string) (error, string) {

	body := new(bytes.Buffer)
	loginData := map[string]string{
		"email":    username,
		"password": password,
	}
	err := json.NewEncoder(body).Encode(loginData)
	if err != nil {
		log.Printf("Erroror encoding data: %v", err)
		return err, ""
	}

	err, resp := c.RequestNodeByPeerId(peerId, federation.RequestNode{
		Method:  "POST",
		Path:    "/api/v1/user/login",
		Headers: map[string]string{},
		Body:    string(body.Bytes()),
	})
	if err != nil {
		return err, ""
	}

	cookieHeader := resp.Header.Get("Set-Cookie")
	// fmt.Println("Cookie header:", cookieHeader)
	re := regexp.MustCompile(`session_id=([^;]+)`)

	match := re.FindStringSubmatch(cookieHeader)
	if match != nil && len(match) > 1 {
		return nil, match[1]
	}
	return fmt.Errorf("No session id found"), ""
}

func (c *Client) RequestNodeByPeerId(peerId string, data federation.RequestNode) (error, *http.Response) {
	body := new(bytes.Buffer)
	err := json.NewEncoder(body).Encode(data)
	if err != nil {
		log.Printf("Error encoding data: %v", err)
		return err, nil
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/api/v1/federation/nodes/peer/%s/request", c.host, peerId), body)
	if err != nil {
		log.Printf("Error creating request: %v", err)
		return err, nil
	}

	// req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", c.host)
	req.Header.Set("Cookie", fmt.Sprintf("session_id=%s", c.sessionId))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error sending request: %v", err)
		return err, nil
	}

	if resp.StatusCode != http.StatusOK {
		// parse the body as text and print it
		defer resp.Body.Close()
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Printf("Error reading body: %v", err)
			return fmt.Errorf("Error response: %v", resp.Status), nil
		}
		return fmt.Errorf("Error response: %v", string(bodyBytes)), nil
	}

	return nil, resp
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

func (c *Client) GetKeyNames() (error, []string) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/keys/names", c.host), nil)
	if err != nil {
		log.Printf("Error creating request: %v", err)
		return err, []string{}
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", c.host)
	req.Header.Set("Cookie", fmt.Sprintf("session_id=%s", c.sessionId))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error sending request: %v", err)
		return err, []string{}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Error response: %v", resp.Status), []string{}
	}

	var keys []string
	err = json.NewDecoder(resp.Body).Decode(&keys)
	if err != nil {
		log.Printf("Error decoding response: %v", err)
		return err, []string{}
	}

	return nil, keys
}

func (c *Client) CreateKey(keyName string, keyType string, keyContent []byte, sealed bool) error {
	if sealed {
		encrypted, err := util.Encrypt(keyContent, []byte(c.sealKey))
		if err != nil {
			log.Printf("Error encrypting key: %v", err)
			return err
		}
		fmt.Println("Sealed key with user password!")
		keyContent = encrypted
	}

	body := new(bytes.Buffer)
	err := json.NewEncoder(body).Encode(tls.CreateKeyRequest{
		KeyName:    keyName,
		KeyType:    keyType,
		KeyContent: keyContent,
		Sealed:     sealed,
	})
	if err != nil {
		log.Printf("Error encoding data: %v", err)
		return err
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/api/v1/keys/create", c.host), body)
	if err != nil {
		log.Printf("Error creating request: %v", err)
		return err
	}

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

	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("Error response: %v", resp.Status)
	}

	return nil
}

func (c *Client) RetrieveKey(keyName string) (error, *database.Key) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/keys/%s/get", c.host, keyName), nil)
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
		return fmt.Errorf("Error response: %v", resp.Status), nil
	}

	var key database.Key
	err = json.NewDecoder(resp.Body).Decode(&key)
	if err != nil {
		log.Printf("Error decoding response: %v", err)
		return err, nil
	}

	if key.Sealed {
		if c.sealKey == nil {
			fmt.Println("No seal key found, unable to decrypt key!, please set the seal key with `backend client seal-key <key>`")
			return nil, &key
		}
		decrypted, err := util.Decrypt(key.KeyContent, []byte(c.sealKey))
		if err != nil {
			log.Printf("Error decrypting key: %v", err)
			return err, nil
		}
		key.KeyContent = decrypted
	}

	return nil, &key
}

func (c *Client) ListProxies(index int64, limit int64) (error, federation.PaginatedProxies) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/federation/proxies/list", c.host), nil)
	if err != nil {
		log.Printf("Error creating request: %v", err)
		return err, federation.PaginatedProxies{}
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", c.host)
	req.Header.Set("Cookie", fmt.Sprintf("session_id=%s", c.sessionId))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error sending request: %v", err)
		return err, federation.PaginatedProxies{}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Error response: %v", resp.Status), federation.PaginatedProxies{}
	}

	var proxies federation.PaginatedProxies
	err = json.NewDecoder(resp.Body).Decode(&proxies)
	if err != nil {
		log.Printf("Error decoding response: %v", err)
		return err, federation.PaginatedProxies{}
	}

	return nil, proxies
}

func (c *Client) DeleteKey(keyName string) error {
	req, err := http.NewRequest("DELETE", fmt.Sprintf("%s/api/v1/tls/keys/%s", c.host, keyName), nil)
	if err != nil {
		log.Printf("Error creating request: %v", err)
		return err
	}

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
