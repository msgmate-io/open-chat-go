package secretstore

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

// SealParams describes a value to seal. OwnerUUID is empty for server-global
// (config) blocks. Scope groups blocks ("config", "integration:git", ...).
type SealParams struct {
	OwnerUUID string
	Scope     string
	Label     string
	// Algorithm optionally pins the algorithm/provider. When empty the active
	// provider's default algorithm is used.
	Algorithm string
	Plaintext []byte
}

// AuthParams carries an authorization attempt for a provider. SessionKey is the
// cache key (a session id, a bearer-token hash, or BootSessionKey). OwnerUUID
// scopes the authorization for per-user providers.
type AuthParams struct {
	OwnerUUID  string
	SessionKey string
	Password   string
}

// AuthResult reports an active authorization.
type AuthResult struct {
	ExpiresAt time.Time
	OwnerUUID string
}

// Provider is implemented by each backing secret store (tvault, local-master).
type Provider interface {
	// Name is the stable provider identifier.
	Name() string
	// Enabled reports whether the provider is configured and usable.
	Enabled() bool
	// Algorithms lists the algorithm ids the provider can unseal; the first is
	// the default used for new seals.
	Algorithms() []string
	// Seal encrypts plaintext and returns a Block (persistence is the caller's
	// job). Implementations must set Ref and Algorithm.
	Seal(ctx context.Context, p SealParams) (Block, error)
	// Unseal decrypts a block. It returns ErrAuthorizationRequired when the
	// caller is not authorized for the block owner.
	Unseal(ctx context.Context, b Block) ([]byte, error)
	// Authorize validates a password and caches the derived key for
	// p.SessionKey for the configured TTL.
	Authorize(ctx context.Context, p AuthParams) (AuthResult, error)
	// Status reports whether an authorization is currently active for
	// p.SessionKey.
	Status(ctx context.Context, p AuthParams) (AuthResult, bool)
	// Revoke drops any cached authorization for p.SessionKey.
	Revoke(ctx context.Context, p AuthParams)
}

// prioritized lets a provider opt into ordering when several are enabled. The
// highest priority enabled provider is returned by Active.
type prioritized interface {
	Priority() int
}

// PasswordProvider is implemented by providers that require a password (rather
// than a configured key) to unseal. It lets callers fail closed with a clear
// message before attempting to authorize.
type PasswordProvider interface {
	RequiresPassword() bool
}

// ProviderRequiresPassword reports whether p needs a password to unseal.
func ProviderRequiresPassword(p Provider) bool {
	if p == nil {
		return false
	}
	if passwordProvider, ok := p.(PasswordProvider); ok {
		return passwordProvider.RequiresPassword()
	}
	return false
}

// BootSessionKey is the authorization cache key used for server-global config
// blocks at startup (process lifetime).
const BootSessionKey = "boot"

var (
	registryMu sync.RWMutex
	providers  []Provider
)

// RegisterProvider adds a provider to the registry. Registering the same name
// twice replaces the earlier entry (last registration wins), which keeps tests
// and re-initialization predictable.
func RegisterProvider(p Provider) {
	if p == nil {
		return
	}
	name := strings.TrimSpace(p.Name())
	if name == "" {
		return
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	for idx, existing := range providers {
		if strings.EqualFold(existing.Name(), name) {
			providers[idx] = p
			return
		}
	}
	providers = append(providers, p)
}

// Providers returns the registered providers ordered by descending priority.
func Providers() []Provider {
	registryMu.RLock()
	out := make([]Provider, len(providers))
	copy(out, providers)
	registryMu.RUnlock()
	sort.SliceStable(out, func(i, j int) bool {
		return providerPriority(out[i]) > providerPriority(out[j])
	})
	return out
}

func providerPriority(p Provider) int {
	if pr, ok := p.(prioritized); ok {
		return pr.Priority()
	}
	return 0
}

// Active returns the highest priority enabled provider, or nil when none is
// usable.
func Active() Provider {
	for _, p := range Providers() {
		if p.Enabled() {
			return p
		}
	}
	return nil
}

// Resolve returns the provider that can unseal the given algorithm.
func Resolve(algorithm string) (Provider, error) {
	wanted := strings.TrimSpace(algorithm)
	if wanted == "" {
		return nil, ErrUnknownAlgorithm
	}
	for _, p := range Providers() {
		for _, algo := range p.Algorithms() {
			if strings.EqualFold(strings.TrimSpace(algo), wanted) {
				return p, nil
			}
		}
	}
	return nil, ErrUnknownAlgorithm
}

// Seal is a convenience wrapper around the active provider.
func Seal(ctx context.Context, p SealParams) (Block, error) {
	provider := Active()
	if provider == nil {
		return Block{}, ErrNoProvider
	}
	return provider.Seal(ctx, p)
}

// Unseal resolves the provider for the block's algorithm and decrypts it.
func Unseal(ctx context.Context, b Block) ([]byte, error) {
	provider, err := Resolve(b.Algorithm)
	if err != nil {
		return nil, err
	}
	return provider.Unseal(ctx, b)
}
