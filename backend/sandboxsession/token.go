package sandboxsession

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// This file implements the short-lived attach token a provider returns in
// Session.AttachToken and the terminal attach endpoint verifies, so a shell
// session URL cannot be replayed or guessed (ci#690).

// ErrInvalidAttachToken is returned when an attach token does not verify.
var ErrInvalidAttachToken = errors.New("sandboxsession: invalid or expired attach token")

// TokenIssuer mints and verifies attach tokens with a server-held HMAC key.
type TokenIssuer struct {
	key []byte
	now func() time.Time
}

// NewTokenIssuer returns an issuer over key. An empty key is rejected: callers
// must supply a random, server-side secret.
func NewTokenIssuer(key []byte) (*TokenIssuer, error) {
	if len(key) == 0 {
		return nil, fmt.Errorf("%w: attach token key is empty", ErrInvalidRequest)
	}
	derived := sha256.Sum256(append([]byte("sandboxsession-attach-v1:"), key...))
	return &TokenIssuer{key: derived[:], now: time.Now}, nil
}

// NewRandomKey returns a fresh 32-byte attach token key.
func NewRandomKey() ([]byte, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("sandboxsession: generate attach token key: %w", err)
	}
	return key, nil
}

// Mint returns a token binding sandboxUUID/sessionID to ownerUUID with an
// expiry ttl from now. The token carries no secrets: it is
// base64(nonce.expiry.signature) over a sha256 HMAC.
func (t *TokenIssuer) Mint(sandboxUUID, sessionID, ownerUUID string, ttl time.Duration) (string, error) {
	if t == nil {
		return "", fmt.Errorf("%w: nil token issuer", ErrInvalidRequest)
	}
	if strings.TrimSpace(sandboxUUID) == "" || strings.TrimSpace(sessionID) == "" {
		return "", fmt.Errorf("%w: sandbox and session ids are required for an attach token", ErrInvalidRequest)
	}
	if ttl <= 0 {
		ttl = DefaultSessionTTL
	}

	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("sandboxsession: generate attach token nonce: %w", err)
	}

	expiry := t.clock().Add(ttl).Unix()
	payload := strconv.FormatInt(expiry, 10) + ":" + strings.Join([]string{sandboxUUID, sessionID, ownerUUID}, "\x00")
	sum := t.sign(payload)

	return hex.EncodeToString(nonce) + "." + base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + hex.EncodeToString(sum), nil
}

// AttachClaims are the verified contents of an attach token.
type AttachClaims struct {
	SandboxUUID string
	SessionID   string
	OwnerUUID   string
	ExpiresAt   time.Time
}

// Verify checks the signature and expiry of token and returns its claims.
func (t *TokenIssuer) Verify(sandboxUUID, sessionID, ownerUUID, token string) (AttachClaims, error) {
	if t == nil {
		return AttachClaims{}, fmt.Errorf("%w: nil token issuer", ErrInvalidAttachToken)
	}

	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 {
		return AttachClaims{}, ErrInvalidAttachToken
	}
	if _, err := hex.DecodeString(parts[0]); err != nil {
		return AttachClaims{}, ErrInvalidAttachToken
	}

	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return AttachClaims{}, ErrInvalidAttachToken
	}
	payload := string(raw)

	sig, err := hex.DecodeString(parts[2])
	if err != nil {
		return AttachClaims{}, ErrInvalidAttachToken
	}
	if subtle.ConstantTimeCompare(sig, t.sign(payload)) != 1 {
		return AttachClaims{}, ErrInvalidAttachToken
	}

	expiryPart, rest, ok := strings.Cut(payload, ":")
	if !ok {
		return AttachClaims{}, ErrInvalidAttachToken
	}
	expiry, err := strconv.ParseInt(expiryPart, 10, 64)
	if err != nil {
		return AttachClaims{}, ErrInvalidAttachToken
	}

	fields := strings.Split(rest, "\x00")
	if len(fields) != 3 {
		return AttachClaims{}, ErrInvalidAttachToken
	}
	claims := AttachClaims{
		SandboxUUID: fields[0],
		SessionID:   fields[1],
		OwnerUUID:   fields[2],
		ExpiresAt:   time.Unix(expiry, 0).UTC(),
	}

	if !t.clock().Before(claims.ExpiresAt) {
		return AttachClaims{}, ErrInvalidAttachToken
	}
	if claims.SandboxUUID != sandboxUUID || claims.SessionID != sessionID || claims.OwnerUUID != ownerUUID {
		return AttachClaims{}, ErrInvalidAttachToken
	}
	return claims, nil
}

func (t *TokenIssuer) clock() time.Time {
	if t.now != nil {
		return t.now()
	}
	return time.Now()
}

func (t *TokenIssuer) sign(payload string) []byte {
	mac := hmac.New(sha256.New, t.key)
	mac.Write([]byte(payload))
	return mac.Sum(nil)
}
