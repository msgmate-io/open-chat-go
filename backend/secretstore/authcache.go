package secretstore

import (
	"sync"
	"time"
)

// DefaultAuthTTL is how long a derived key stays cached after Authorize when a
// provider does not override it.
const DefaultAuthTTL = 15 * time.Minute

type authEntry struct {
	key       []byte
	owner     string
	expiresAt time.Time
}

// AuthCache is a small TTL cache for derived keys, keyed by an opaque session
// string. It deliberately stores the derived key, never the raw password, and
// wipes the key material on expiry/removal (best effort).
type AuthCache struct {
	mu      sync.RWMutex
	entries map[string]authEntry
}

// NewAuthCache returns an empty cache.
func NewAuthCache() *AuthCache {
	return &AuthCache{entries: map[string]authEntry{}}
}

// Put stores a derived key for sessionKey until expiresAt. A non-positive
// expiresAt falls back to DefaultAuthTTL.
func (c *AuthCache) Put(sessionKey string, key []byte, owner string, expiresAt time.Time) time.Time {
	if sessionKey == "" || len(key) == 0 {
		return time.Time{}
	}
	if expiresAt.IsZero() || !expiresAt.After(time.Now().UTC()) {
		expiresAt = time.Now().UTC().Add(DefaultAuthTTL)
	}
	stored := append([]byte(nil), key...)
	c.mu.Lock()
	c.entries[sessionKey] = authEntry{key: stored, owner: owner, expiresAt: expiresAt}
	c.mu.Unlock()
	return expiresAt
}

// Get returns the cached derived key for sessionKey. Expired entries are
// dropped and reported as missing.
func (c *AuthCache) Get(sessionKey string) ([]byte, string, bool) {
	if sessionKey == "" {
		return nil, "", false
	}
	c.mu.RLock()
	entry, ok := c.entries[sessionKey]
	c.mu.RUnlock()
	if !ok {
		return nil, "", false
	}
	if time.Now().UTC().After(entry.expiresAt) {
		c.Delete(sessionKey)
		return nil, "", false
	}
	return append([]byte(nil), entry.key...), entry.owner, true
}

// Status reports the expiry for an active sessionKey.
func (c *AuthCache) Status(sessionKey string) (time.Time, bool) {
	if sessionKey == "" {
		return time.Time{}, false
	}
	c.mu.RLock()
	entry, ok := c.entries[sessionKey]
	c.mu.RUnlock()
	if !ok || time.Now().UTC().After(entry.expiresAt) {
		return time.Time{}, false
	}
	return entry.expiresAt, true
}

// Delete removes and wipes the entry for sessionKey.
func (c *AuthCache) Delete(sessionKey string) {
	if sessionKey == "" {
		return
	}
	c.mu.Lock()
	entry, ok := c.entries[sessionKey]
	if ok {
		wipe(entry.key)
		delete(c.entries, sessionKey)
	}
	c.mu.Unlock()
}

// Wipe removes and wipes every cached entry.
func (c *AuthCache) Wipe() {
	c.mu.Lock()
	for key, entry := range c.entries {
		wipe(entry.key)
		delete(c.entries, key)
	}
	c.mu.Unlock()
}

func wipe(buf []byte) {
	for i := range buf {
		buf[i] = 0
	}
}
