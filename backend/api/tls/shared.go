package tls

import (
	"log"
	"sync"
)

// Global challenge store for ACME HTTP-01 challenges
var challengeStore = make(map[string]string)
var challengeMutex sync.RWMutex

// SetChallenge stores an ACME challenge token and its response
func SetChallenge(token, keyAuth string) {
	challengeMutex.Lock()
	defer challengeMutex.Unlock()
	challengeStore[token] = keyAuth
}

// GetChallenge retrieves an ACME challenge response
func GetChallenge(token string) (string, bool) {
	challengeMutex.RLock()
	defer challengeMutex.RUnlock()
	keyAuth, exists := challengeStore[token]
	return keyAuth, exists
}

// ClearChallenge removes a challenge after it's been used
func ClearChallenge(token string) {
	challengeMutex.Lock()
	defer challengeMutex.Unlock()
	delete(challengeStore, token)
}

// CustomHTTP01Provider implements the lego HTTP-01 challenge provider
// that integrates with our existing HTTP server instead of starting a new one
type CustomHTTP01Provider struct{}

func (p *CustomHTTP01Provider) Present(domain, token, keyAuth string) error {
	// Store the challenge in our global store
	SetChallenge(token, keyAuth)
	log.Printf("Stored ACME challenge for domain %s, token %s", domain, token)
	return nil
}

func (p *CustomHTTP01Provider) CleanUp(domain, token, keyAuth string) error {
	// Clean up the challenge
	ClearChallenge(token)
	log.Printf("Cleaned up ACME challenge for domain %s, token %s", domain, token)
	return nil
}
