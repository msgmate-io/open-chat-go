package client

import (
	"backend/api/chats"
	"backend/api/federation"
	"backend/api/tls"
	"backend/api/user"
	"backend/database"
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/libp2p/go-libp2p/core/peer"
	"log"
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
	body := new(bytes.Buffer)
	err := json.NewEncoder(body).Encode(user.UserLogin{
		Email:    username,
		Password: password,
	})
	if err != nil {
		log.Printf("Erroror encoding data: %v", err)
		return err, ""
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/api/v1/user/login", c.host), body)
	if err != nil {
		log.Printf("Error creating request: %v", err)
		return err, ""
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", c.host)

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

	cookieHeader := resp.Header.Get("Set-Cookie")
	re := regexp.MustCompile(`session_id=([^;]+)`)

	// Find the first match
	// e.g.:  session_id=877a0b36a59391125d133ba73e9edeba; Path=/; Domain=localhost; Expires=Tue, 24 Dec 2024 14:54:51 GMT; Max-Age=86400; HttpOnly; Secure; SameSite=Strict
	match := re.FindStringSubmatch(cookieHeader)
	if match != nil && len(match) > 1 {
		c.sessionId = match[1]
		return nil, match[1]
	}
	return fmt.Errorf("No session id found"), ""
}

type PaginatedChats struct {
	database.Pagination
	Rows []chats.ListedChat `json:"rows"`
}

func (c *Client) SetSessionId(sessionId string) {
	c.sessionId = sessionId
}

func (c *Client) RegisterNode(name string, addresses []string) (error, *database.Node) {
	body := new(bytes.Buffer)
	err := json.NewEncoder(body).Encode(federation.RegisterNode{
		Name:      name,
		Addresses: addresses,
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

	return nil, nil
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
