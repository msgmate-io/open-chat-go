package client

import (
	"backend/api"
	"backend/api/chats"
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

	"backend/api/contacts"
	"github.com/libp2p/go-libp2p/core/peer"
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

func NewClient(host string) *Client {
	sealKey := os.Getenv("OPEN_CHAT_SEAL_KEY")
	if sealKey == "" {
		sealKey = ""
	}
	apiKeys := map[string]string{
		"deepinfra": os.Getenv("DEEPINFRA_API_KEY"),
		"openai":    os.Getenv("OPENAI_API_KEY"),
		"groq":      os.Getenv("GROQ_API_KEY"),
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

func (c *Client) GetFederationIdentity() (error, *api.IdentityResponse) {
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

	var identity api.IdentityResponse
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
	err := json.NewEncoder(body).Encode(api.RegisterNodeRequest{
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

func (c *Client) GetNodes(index int64, limit int64) (error, api.PaginatedNodes) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/federation/nodes/list", c.host), nil)
	if err != nil {
		log.Printf("Error creating request: %v", err)
		return err, api.PaginatedNodes{}
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
		return err, api.PaginatedNodes{}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Error response: %v", resp.Status), api.PaginatedNodes{}
	}

	var paginatedNodes api.PaginatedNodes
	err = json.NewDecoder(resp.Body).Decode(&paginatedNodes)
	if err != nil {
		log.Printf("Error decoding response: %v", err)
		return err, api.PaginatedNodes{}
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
	return c.CreateProxyWithExpiration(direction, origin, target, port, networkName, 5) // Default 5 minutes
}

// CreateProxyWithExpiration creates a proxy with custom expiration time
func (c *Client) CreateProxyWithExpiration(direction string, origin string, target string, port string, networkName string, expiresInMinutes int) error {
	if networkName == "" {
		networkName = "network"
	}
	body := new(bytes.Buffer)
	err := json.NewEncoder(body).Encode(api.CreateAndStartProxyRequest{
		Direction:     direction,
		TrafficOrigin: origin,
		TrafficTarget: target,
		Port:          port,
		Kind:          "tcp",
		NetworkName:   networkName,
		ExpiresIn:     expiresInMinutes,
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

	if expiresInMinutes > 0 {
		fmt.Printf("Proxy created!! (expires in %d minutes)\n", expiresInMinutes)
	} else {
		fmt.Println("Proxy created!! (persistent)")
	}

	return nil
}

// CreateDomainProxy creates a domain-based proxy with separate TLS certificate
func (c *Client) CreateDomainProxy(domain string, certPrefix string, backendPort string, useTLS bool) error {
	body := new(bytes.Buffer)
	err := json.NewEncoder(body).Encode(api.CreateAndStartProxyRequest{
		Direction:     "egress",
		TrafficOrigin: domain,                                        // Domain name
		TrafficTarget: fmt.Sprintf("%s:%s", certPrefix, backendPort), // cert_prefix:backend_port
		Port:          "443",                                         // Default HTTPS port
		Kind:          "domain",                                      // New domain proxy kind
		NetworkName:   "network",
		UseTLS:        useTLS,
		KeyPrefix:     certPrefix,
		ExpiresIn:     0, // Domain proxies are persistent (no expiration)
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

	fmt.Printf("Domain proxy created for %s -> localhost:%s (cert: %s) - PERSISTENT\n", domain, backendPort, certPrefix)

	return nil
}

// DeleteProxy deletes a proxy by UUID
func (c *Client) DeleteProxy(proxyUUID string) error {
	req, err := http.NewRequest("DELETE", fmt.Sprintf("%s/api/v1/federation/proxies/%s", c.host, proxyUUID), nil)
	if err != nil {
		log.Printf("Error creating request: %v", err)
		return err
	}

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
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to delete proxy: %s", string(body))
	}

	fmt.Printf("Proxy %s deleted successfully\n", proxyUUID)
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

func (c *Client) RenewTLSCertificate(hostname string, keyPrefix string) (error, string) {
	body := new(bytes.Buffer)
	err := json.NewEncoder(body).Encode(tls.RenewTLSCertificateRequest{
		Hostname:  hostname,
		KeyPrefix: keyPrefix,
	})
	if err != nil {
		log.Printf("Error encoding data: %v", err)
		return err, ""
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/api/v1/tls/acme/renew", c.host), body)
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

	err, resp := c.RequestNodeByPeerId(peerId, api.RequestNode{
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

func (c *Client) RequestNodeByPeerId(peerId string, data api.RequestNode) (error, *http.Response) {
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

func (c *Client) ListProxies(index int64, limit int64) (error, api.PaginatedProxies) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/federation/proxies/list", c.host), nil)
	if err != nil {
		log.Printf("Error creating request: %v", err)
		return err, api.PaginatedProxies{}
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", c.host)
	req.Header.Set("Cookie", fmt.Sprintf("session_id=%s", c.sessionId))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error sending request: %v", err)
		return err, api.PaginatedProxies{}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Error response: %v", resp.Status), api.PaginatedProxies{}
	}

	var proxies api.PaginatedProxies
	err = json.NewDecoder(resp.Body).Decode(&proxies)
	if err != nil {
		log.Printf("Error decoding response: %v", err)
		return err, api.PaginatedProxies{}
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

func (c *Client) InstallSignalIntegration(alias string, phoneNumber string, port int, mode string) error {
	body := new(bytes.Buffer)
	requestData := map[string]interface{}{
		"alias":        alias,
		"phone_number": phoneNumber,
		"port":         port,
		"mode":         mode,
	}

	err := json.NewEncoder(body).Encode(requestData)
	if err != nil {
		log.Printf("Error encoding data: %v", err)
		return err
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/api/v1/integrations/signal/install", c.host), body)
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
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to install Signal integration: %s", string(bodyBytes))
	}

	return nil
}

func (c *Client) UninstallSignalIntegration(alias string) error {
	body := new(bytes.Buffer)
	requestData := map[string]interface{}{
		"alias": alias,
	}

	err := json.NewEncoder(body).Encode(requestData)
	if err != nil {
		log.Printf("Error encoding data: %v", err)
		return err
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/api/v1/integrations/signal/uninstall", c.host), body)
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
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to uninstall Signal integration: %s", string(bodyBytes))
	}

	return nil
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
func (c *Client) CreateChatWithAttachments(contactToken string, firstMessage string, attachments []chats.FileAttachment, sharedConfig json.RawMessage, chatType string) (error, chats.ListedChat) {
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

// AddToSignalWhitelist adds a phone number to the whitelist for a Signal integration
func (c *Client) AddToSignalWhitelist(alias string, phoneNumber string) error {
	body := new(bytes.Buffer)
	requestData := map[string]interface{}{
		"alias":        alias,
		"phone_number": phoneNumber,
	}

	err := json.NewEncoder(body).Encode(requestData)
	if err != nil {
		log.Printf("Error encoding data: %v", err)
		return err
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/api/v1/integrations/signal/whitelist/add", c.host), body)
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
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to add number to Signal whitelist: %s", string(bodyBytes))
	}

	return nil
}

// RemoveFromSignalWhitelist removes a phone number from the whitelist for a Signal integration
func (c *Client) RemoveFromSignalWhitelist(alias string, phoneNumber string) error {
	body := new(bytes.Buffer)
	requestData := map[string]interface{}{
		"alias":        alias,
		"phone_number": phoneNumber,
	}

	err := json.NewEncoder(body).Encode(requestData)
	if err != nil {
		log.Printf("Error encoding data: %v", err)
		return err
	}

	req, err := http.NewRequest("DELETE", fmt.Sprintf("%s/api/v1/integrations/signal/whitelist/remove", c.host), body)
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
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to remove number from Signal whitelist: %s", string(bodyBytes))
	}

	return nil
}

// GetSignalWhitelist retrieves the current whitelist for a Signal integration
func (c *Client) GetSignalWhitelist(alias string) (error, []string) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/integrations/signal/whitelist", c.host), nil)
	if err != nil {
		log.Printf("Error creating request: %v", err)
		return err, nil
	}

	query := req.URL.Query()
	query.Add("alias", alias)
	req.URL.RawQuery = query.Encode()

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
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to get Signal whitelist: %s", string(bodyBytes)), nil
	}

	var response map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&response)
	if err != nil {
		log.Printf("Error decoding response: %v", err)
		return err, nil
	}

	// Extract the whitelist from the response
	whitelist := []string{}
	if whitelistInterface, ok := response["whitelist"].([]interface{}); ok {
		for _, item := range whitelistInterface {
			if str, ok := item.(string); ok {
				whitelist = append(whitelist, str)
			}
		}
	}

	return nil, whitelist
}
