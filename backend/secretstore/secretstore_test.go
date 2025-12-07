package secretstore

import (
	"bytes"
	"context"
	"testing"
	"time"
)

func testProvider(t *testing.T, master string) *LocalMasterProvider {
	t.Helper()
	return NewLocalMasterProvider(LocalMasterOptions{
		EnvKey:    "TEST_SEAL_KEY",
		Namespace: "test",
		LookupEnv: func(key string) (string, bool) {
			if key == "TEST_SEAL_KEY" && master != "" {
				return master, true
			}
			return "", false
		},
	})
}

func TestLocalMasterRoundTrip(t *testing.T) {
	provider := testProvider(t, "super-secret-master")
	ctx := context.Background()

	block, err := provider.Seal(ctx, SealParams{
		OwnerUUID: "user-1",
		Scope:     "integration:git",
		Label:     "token",
		Plaintext: []byte("ghp_example_token"),
	})
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if block.Ref == "" {
		t.Fatalf("expected a block ref")
	}
	if block.Algorithm != AlgoLocalV1 {
		t.Fatalf("expected algorithm %q, got %q", AlgoLocalV1, block.Algorithm)
	}
	if bytes.Contains(block.Data, []byte("ghp_example_token")) {
		t.Fatalf("ciphertext must not contain plaintext")
	}

	plain, err := provider.Unseal(ctx, block)
	if err != nil {
		t.Fatalf("unseal: %v", err)
	}
	if string(plain) != "ghp_example_token" {
		t.Fatalf("unexpected plaintext %q", plain)
	}
}

func TestLocalMasterWrongMasterFails(t *testing.T) {
	ctx := context.Background()
	sealer := testProvider(t, "master-a")
	block, err := sealer.Seal(ctx, SealParams{OwnerUUID: "user-1", Plaintext: []byte("value")})
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	opener := testProvider(t, "master-b")
	if _, err := opener.Unseal(ctx, block); err == nil {
		t.Fatalf("expected unseal to fail with a different master key")
	}
}

func TestLocalMasterFallbackDerivation(t *testing.T) {
	provider := testProvider(t, "")
	if provider.Enabled() {
		t.Fatalf("provider must be disabled without a master key")
	}
	// The fallback is never selected implicitly, but the scheme itself must
	// still round-trip when forced.
	block, err := provider.encrypt("owner-1", []byte("value"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	plain, err := provider.decrypt("owner-1", block)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if string(plain) != "value" {
		t.Fatalf("unexpected plaintext %q", plain)
	}
	// A different owner must not decrypt (owner is bound into the fallback).
	if _, err := provider.decrypt("owner-2", block); err == nil {
		t.Fatalf("expected owner-scoped fallback key mismatch")
	}
}

func TestLocalMasterSealDisabled(t *testing.T) {
	provider := testProvider(t, "")
	if _, err := provider.Seal(context.Background(), SealParams{Plaintext: []byte("x")}); err != ErrNoProvider {
		t.Fatalf("expected ErrNoProvider, got %v", err)
	}
}

func TestLocalMasterAuthorizeNoPassword(t *testing.T) {
	provider := testProvider(t, "master")
	result, err := provider.Authorize(context.Background(), AuthParams{SessionKey: "s1"})
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if !result.ExpiresAt.IsZero() {
		t.Fatalf("key-based provider should not set an expiry")
	}
	if _, ok := provider.Status(context.Background(), AuthParams{SessionKey: "s1"}); !ok {
		t.Fatalf("expected key-based provider to always report authorized")
	}
}

type namedProvider struct {
	*LocalMasterProvider
	name     string
	priority int
}

func (n namedProvider) Name() string  { return n.name }
func (n namedProvider) Priority() int { return n.priority }

func TestRegistryActivePrefersPriority(t *testing.T) {
	restoreRegistry(t)
	low := namedProvider{LocalMasterProvider: testProvider(t, "low"), name: "local-master", priority: 10}
	high := namedProvider{LocalMasterProvider: testProvider(t, "high"), name: "tvault", priority: 100}
	RegisterProvider(low)
	RegisterProvider(high)

	active := Active()
	if active == nil || active.Name() != "tvault" {
		t.Fatalf("expected highest-priority provider, got %v", active)
	}
}

func TestRegistryResolveAndUnseal(t *testing.T) {
	restoreRegistry(t)
	provider := testProvider(t, "master")
	RegisterProvider(provider)

	resolved, err := Resolve(AlgoLocalV1)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.Name() != provider.Name() {
		t.Fatalf("resolved wrong provider %q", resolved.Name())
	}
	if _, err := Resolve("does-not-exist"); err != ErrUnknownAlgorithm {
		t.Fatalf("expected ErrUnknownAlgorithm, got %v", err)
	}

	ctx := context.Background()
	block, err := Seal(ctx, SealParams{OwnerUUID: "u", Plaintext: []byte("v")})
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	plain, err := Unseal(ctx, block)
	if err != nil {
		t.Fatalf("unseal: %v", err)
	}
	if string(plain) != "v" {
		t.Fatalf("unexpected plaintext %q", plain)
	}
}

func TestAuthCacheTTLAndRevoke(t *testing.T) {
	cache := NewAuthCache()
	key := []byte("derived-key-material")

	cache.Put("session-a", key, "owner-1", time.Now().UTC().Add(time.Minute))
	if got, owner, ok := cache.Get("session-a"); !ok || owner != "owner-1" || !bytes.Equal(got, key) {
		t.Fatalf("expected cached key for session-a")
	}
	if _, ok := cache.Status("session-a"); !ok {
		t.Fatalf("expected active status")
	}

	cache.Delete("session-a")
	if _, _, ok := cache.Get("session-a"); ok {
		t.Fatalf("expected key to be removed after delete")
	}

	cache.mu.Lock()
	cache.entries["session-b"] = authEntry{key: []byte("stale"), owner: "owner-2", expiresAt: time.Now().UTC().Add(-time.Second)}
	cache.mu.Unlock()
	if _, _, ok := cache.Get("session-b"); ok {
		t.Fatalf("expected expired entry to be reported as missing")
	}
}

func TestAuthCacheWipeZeroesKeys(t *testing.T) {
	cache := NewAuthCache()
	key := []byte("wipe-me-please")
	cache.Put("session", key, "owner", time.Now().UTC().Add(time.Minute))
	cache.mu.RLock()
	stored := cache.entries["session"].key
	cache.mu.RUnlock()
	cache.Wipe()
	for _, b := range stored {
		if b != 0 {
			t.Fatalf("expected wiped buffer to be zeroed, got %v", stored)
		}
	}
}

func TestNewRefUnique(t *testing.T) {
	seen := map[string]struct{}{}
	for i := 0; i < 64; i++ {
		ref, err := NewRef()
		if err != nil {
			t.Fatalf("new ref: %v", err)
		}
		if len(ref) != 32 {
			t.Fatalf("expected 32 hex chars, got %d", len(ref))
		}
		if _, ok := seen[ref]; ok {
			t.Fatalf("duplicate ref %q", ref)
		}
		seen[ref] = struct{}{}
	}
}

func restoreRegistry(t *testing.T) {
	t.Helper()
	registryMu.Lock()
	saved := providers
	providers = nil
	registryMu.Unlock()
	t.Cleanup(func() {
		registryMu.Lock()
		providers = saved
		registryMu.Unlock()
	})
}
