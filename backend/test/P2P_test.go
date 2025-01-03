package test

import (
	"backend/api/federation"
	"backend/api/user"
	"encoding/json"
	"fmt"
	"log"
	"testing"
	"time"
)

// 'go test -v ./... -run "^Test_P2P$"'
func Test_P2P(t *testing.T) {
	err, host1, cancel := startTestServer([]string{"backend", "-b", "0.0.0.0", "-p", "1984", "-pp2p", "1985", "-dp", "test1.db"})

	if err != nil {
		t.Fatalf("Error starting test server: %v", err)
	}

	log.Printf("Host1: %s", host1)

	err, host2, cancel2 := startTestServer([]string{"backend", "-b", "0.0.0.0", "-p", "1986", "-pp2p", "1987", "-dp", "test2.db"})

	if err != nil {
		t.Fatalf("Error starting test server: %v", err)
	}

	log.Printf("Host2: %s", host2)

	// Test 1: Login to both nodes
	time.Sleep(4 * time.Second)

	// login admin 1
	err, admin1Session := loginUser(host1, user.UserLogin{
		Email:    "admin",
		Password: "password",
	})

	if err != nil {
		t.Fatalf("Error logging in admin: %v", err)
	}

	log.Printf("Admin1: %v", admin1Session)

	// login admin 2
	err, admin2Session := loginUser(host2, user.UserLogin{
		Email:    "admin",
		Password: "password",
	})

	if err != nil {
		t.Fatalf("Error logging in admin: %v", err)
	}

	log.Printf("Admin2: %v", admin2Session)

	// now fetch each nodes federation info
	err, identity1 := getFederationIdentity(host1, admin1Session)
	if err != nil {
		t.Fatalf("Error getting federation info: %v", err)
	}
	prettyIdentity1, _ := json.MarshalIndent(*identity1, "", "  ")

	log.Printf("Identity1: %v", string(prettyIdentity1))

	err, identity2 := getFederationIdentity(host2, admin2Session)
	if err != nil {
		t.Fatalf("Error getting federation info: %v", err)
	}
	prettyIdentity2, _ := json.MarshalIndent(*identity2, "", "  ")

	log.Printf("Identity2: %v", string(prettyIdentity2))

	node1Multiaddr := identity1.ConnectMultiadress
	node2Multiaddr := identity2.ConnectMultiadress

	// Register each other with the other node
	err, node1 := registerNode(host1, admin1Session, federation.RegisterNode{
		Name:      "Node2",
		Addresses: node2Multiaddr,
	})

	if err != nil {
		t.Fatalf("Error registering node: %v", err)
	}

	prettyNode1, _ := json.MarshalIndent(*node1, "", "  ")

	log.Printf("Node1: %v", string(prettyNode1))

	err, node2 := registerNode(host2, admin2Session, federation.RegisterNode{
		Name:      "Node1",
		Addresses: node1Multiaddr,
	})

	prettyNode2, _ := json.MarshalIndent(*node2, "", "  ")

	log.Printf("Node2: %v", string(prettyNode2))

	if err != nil {
		t.Fatalf("Error registering node: %v", err)
	}

	log.Printf("Node2: %v", string(prettyNode2))

	log.Printf("Node1 UUID: %v", (*node1).UUID)

	time.Sleep(5 * time.Second)

	// start the real testing
	// Test 2 request node1's identity trough node2
	err, _ = requestNode(host1, admin1Session, (*node1).UUID, federation.RequestNode{
		Method: "GET",
		Path:   "/api/v1/user/self",
		Headers: map[string]string{
			"Origin": host1,
			"Cookie": fmt.Sprintf("session_id=%s", admin2Session),
		},
		Body: "",
	})

	if err != nil {
		t.Fatalf("Error requesting node: %v", err)
	}

	// cancel the servers
	defer cancel()
	defer cancel2()
}
