package secretstore

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"os"
	"strings"
	"time"

	"backend/runtimecfg"
)

// LocalMasterOptions configures a LocalMasterProvider.
type LocalMasterOptions struct {
	// EnvKey is the runtime env var holding the master key (e.g.
	// OCI_GIT_TOKEN_ENCRYPTION_KEY or OPEN_CHAT_SEAL_KEY).
	EnvKey string
	// Namespace is mixed into the derived key (e.g. "git-integration").
	Namespace string
	// FallbackPrefix is used when no master key is configured: the key is then
	// derived from this prefix plus the owner UUID. It protects values from
	// casual reads but NOT from an attacker with full database access.
	FallbackPrefix string
	// TTL is the authorization cache TTL (unused by this provider, which needs
	// no password, but kept for interface symmetry).
	TTL time.Duration
	// LookupEnv resolves the master key. Defaults to runtimecfg then os env.
	LookupEnv func(string) (string, bool)
}

// LocalMasterProvider implements the historical per-integration AES-256-GCM
// scheme (SHA-256 derived key, random nonce prefix). It is a key-based fallback
// provider that needs no password authorization.
type LocalMasterProvider struct {
	opts  LocalMasterOptions
	cache *AuthCache
}

// NewLocalMasterProvider builds a provider, applying defaults.
func NewLocalMasterProvider(opts LocalMasterOptions) *LocalMasterProvider {
	if strings.TrimSpace(opts.EnvKey) == "" {
		opts.EnvKey = "OPEN_CHAT_SEAL_KEY"
	}
	if strings.TrimSpace(opts.Namespace) == "" {
		opts.Namespace = "open-chat"
	}
	if strings.TrimSpace(opts.FallbackPrefix) == "" {
		opts.FallbackPrefix = "open-chat-local-fallback"
	}
	if opts.LookupEnv == nil {
		opts.LookupEnv = defaultLookupEnv
	}
	if opts.TTL <= 0 {
		opts.TTL = DefaultAuthTTL
	}
	return &LocalMasterProvider{opts: opts, cache: NewAuthCache()}
}

func defaultLookupEnv(key string) (string, bool) {
	if value, ok := runtimecfg.GetAll()[key]; ok && strings.TrimSpace(value.Value) != "" {
		return strings.TrimSpace(value.Value), true
	}
	value, ok := os.LookupEnv(key)
	return strings.TrimSpace(value), ok
}

// Name implements Provider.
func (p *LocalMasterProvider) Name() string { return "local-master" }

// Priority implements prioritized.
func (p *LocalMasterProvider) Priority() int { return 10 }

// Enabled reports whether a master key is configured. Without one the provider
// is not used (the insecure per-owner fallback is never selected implicitly).
func (p *LocalMasterProvider) Enabled() bool {
	value, ok := p.opts.LookupEnv(p.opts.EnvKey)
	return ok && strings.TrimSpace(value) != ""
}

// Algorithms implements Provider.
func (p *LocalMasterProvider) Algorithms() []string { return []string{AlgoLocalV1} }

// RequiresPassword implements PasswordProvider. This provider is key-based.
func (p *LocalMasterProvider) RequiresPassword() bool { return false }

// Cache exposes the (empty) auth cache for symmetry and testing.
func (p *LocalMasterProvider) Cache() *AuthCache { return p.cache }

func (p *LocalMasterProvider) deriveKey(owner string) []byte {
	master := ""
	if value, ok := p.opts.LookupEnv(p.opts.EnvKey); ok {
		master = strings.TrimSpace(value)
	}
	if master == "" {
		master = p.opts.FallbackPrefix + ":" + owner
	}
	sum := sha256.Sum256([]byte(p.opts.Namespace + ":" + master))
	return sum[:]
}

// Seal implements Provider.
func (p *LocalMasterProvider) Seal(_ context.Context, params SealParams) (Block, error) {
	if !p.Enabled() {
		return Block{}, ErrNoProvider
	}
	ref, err := NewRef()
	if err != nil {
		return Block{}, err
	}
	ciphertext, err := p.encrypt(params.OwnerUUID, params.Plaintext)
	if err != nil {
		return Block{}, err
	}
	return Block{
		Ref:       ref,
		OwnerUUID: params.OwnerUUID,
		Scope:     params.Scope,
		Label:     params.Label,
		Algorithm: AlgoLocalV1,
		Data:      ciphertext,
		CreatedAt: time.Now().UTC().Unix(),
	}, nil
}

// Unseal implements Provider. This provider never requires authorization.
func (p *LocalMasterProvider) Unseal(_ context.Context, b Block) ([]byte, error) {
	if len(b.Data) == 0 {
		return nil, ErrBlockDataRequired
	}
	return p.decrypt(b.OwnerUUID, b.Data)
}

// Authorize implements Provider. Key-based providers need no password.
func (p *LocalMasterProvider) Authorize(_ context.Context, _ AuthParams) (AuthResult, error) {
	return AuthResult{}, nil
}

// Status implements Provider.
func (p *LocalMasterProvider) Status(_ context.Context, _ AuthParams) (AuthResult, bool) {
	return AuthResult{}, true
}

// Revoke implements Provider.
func (p *LocalMasterProvider) Revoke(_ context.Context, _ AuthParams) {}

func (p *LocalMasterProvider) encrypt(owner string, plaintext []byte) ([]byte, error) {
	key := p.deriveKey(owner)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func (p *LocalMasterProvider) decrypt(owner string, ciphertext []byte) ([]byte, error) {
	if len(ciphertext) < 28 { // 12-byte nonce + 16-byte tag
		return nil, errors.New("secretstore: ciphertext too short")
	}
	key := p.deriveKey(owner)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce, sealed := ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():]
	return gcm.Open(nil, nonce, sealed, nil)
}
