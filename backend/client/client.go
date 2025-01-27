package client

import (
	"backend/api/chats"
	"backend/api/federation"
	"backend/api/tls"
	"backend/client/raw"
	"backend/database"
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"github.com/libp2p/go-libp2p/core/peer"
	"io"
	"log"
	"math/big"
	"net/http"
	"regexp"
	"strconv"
)

type SendMessage struct {
	Text string `json:"text"`
}

type Client struct {
	host      string
	sessionId string
}

func NewClient(host string) *Client {
	return &Client{
		host:      host,
		sessionId: "",
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
	return nil, sessionId
}

type PaginatedChats struct {
	database.Pagination
	Rows []chats.ListedChat `json:"rows"`
}

func (c *Client) SetSessionId(sessionId string) {
	c.sessionId = sessionId
}

func (c *Client) RegisterNode(name string, addresses []string, requestRegistration bool, addToNetwork string) (error, *database.Node) {
	body := new(bytes.Buffer)
	err := json.NewEncoder(body).Encode(federation.RegisterNode{
		Name:                name,
		Addresses:           addresses,
		RequestRegistration: requestRegistration,
		AddToNetwork:        addToNetwork,
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
	fmt.Println("Cookie header:", cookieHeader)
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
		// parse the body as text and print it
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
