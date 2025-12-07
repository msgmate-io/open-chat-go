package cmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"backend/secretstore"
)

// passwordTestProvider is a minimal password-based provider that mirrors the
// tvault provider's session-key semantics: owner-less (config) blocks resolve
// their key from secretstore.BootSessionKey, while an explicit session key
// attached to the context takes precedence.
type passwordTestProvider struct {
	cache *secretstore.AuthCache
}

func (p *passwordTestProvider) Name() string           { return "rekey-test-provider" }
func (p *passwordTestProvider) Enabled() bool          { return true }
func (p *passwordTestProvider) Priority() int          { return 1000 }
func (p *passwordTestProvider) Algorithms() []string   { return []string{"test-v1"} }
func (p *passwordTestProvider) RequiresPassword() bool { return true }

func (p *passwordTestProvider) passwordFor(ctx context.Context, owner string) ([]byte, bool) {
	sessionKey := secretstore.BootSessionKey
	if key, ok := secretstore.SessionKeyFromContext(ctx); ok {
		sessionKey = key
	} else if owner != "" {
		return nil, false
	}
	material, _, ok := p.cache.Get(sessionKey)
	return material, ok
}

func (p *passwordTestProvider) Seal(ctx context.Context, params secretstore.SealParams) (secretstore.Block, error) {
	password, ok := p.passwordFor(ctx, params.OwnerUUID)
	if !ok {
		return secretstore.Block{}, secretstore.ErrAuthorizationRequired
	}
	key := sha256.Sum256(password)
	data := make([]byte, 0, len(params.Plaintext)+sha256.Size)
	for i, b := range params.Plaintext {
		data = append(data, b^key[i%len(key)])
	}
	tag := sha256.Sum256(append(key[:], data...))
	data = append(data, tag[:]...)
	ref, err := secretstore.NewRef()
	if err != nil {
		return secretstore.Block{}, err
	}
	return secretstore.Block{
		Ref:       ref,
		OwnerUUID: params.OwnerUUID,
		Scope:     params.Scope,
		Label:     params.Label,
		Algorithm: "test-v1",
		Data:      data,
	}, nil
}

func (p *passwordTestProvider) Unseal(ctx context.Context, b secretstore.Block) ([]byte, error) {
	password, ok := p.passwordFor(ctx, b.OwnerUUID)
	if !ok {
		return nil, secretstore.ErrAuthorizationRequired
	}
	if len(b.Data) < sha256.Size {
		return nil, secretstore.ErrBlockDataRequired
	}
	key := sha256.Sum256(password)
	ciphertext := b.Data[:len(b.Data)-sha256.Size]
	tag := b.Data[len(b.Data)-sha256.Size:]
	want := sha256.Sum256(append(key[:], ciphertext...))
	if !bytes.Equal(tag, want[:]) {
		return nil, secretstore.ErrAuthorizationRequired
	}
	out := make([]byte, len(ciphertext))
	for i, c := range ciphertext {
		out[i] = c ^ key[i%len(key)]
	}
	return out, nil
}

func (p *passwordTestProvider) Authorize(_ context.Context, params secretstore.AuthParams) (secretstore.AuthResult, error) {
	expiresAt := p.cache.Put(params.SessionKey, []byte(params.Password), params.OwnerUUID, time.Now().Add(secretstore.DefaultAuthTTL))
	return secretstore.AuthResult{ExpiresAt: expiresAt, OwnerUUID: params.OwnerUUID}, nil
}

func (p *passwordTestProvider) Status(_ context.Context, params secretstore.AuthParams) (secretstore.AuthResult, bool) {
	expiresAt, ok := p.cache.Status(params.SessionKey)
	return secretstore.AuthResult{ExpiresAt: expiresAt}, ok
}

func (p *passwordTestProvider) Revoke(_ context.Context, params secretstore.AuthParams) {
	p.cache.Delete(params.SessionKey)
}

func TestRekeySecretBlocksChangesKey(t *testing.T) {
	provider := &passwordTestProvider{cache: secretstore.NewAuthCache()}
	secretstore.RegisterProvider(provider)
	ctx := context.Background()

	// Mimic the boot resolver: authorize under BootSessionKey with the old
	// password, then seal a config (owner-less) block with it.
	if _, err := provider.Authorize(ctx, secretstore.AuthParams{SessionKey: secretstore.BootSessionKey, Password: "old-password"}); err != nil {
		t.Fatalf("authorize old password: %v", err)
	}
	const plaintext = "super-secret-plaintext"
	block, err := provider.Seal(ctx, secretstore.SealParams{Scope: "config", Plaintext: []byte(plaintext)})
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	blocks := map[string]secretstore.Block{block.Ref: block}
	rekeyed, err := rekeySecretBlocks(ctx, blocks, "old-password", "new-password")
	if err != nil {
		t.Fatalf("rekeySecretBlocks: %v", err)
	}
	if len(rekeyed) != 1 {
		t.Fatalf("expected 1 rekeyed block, got %d", len(rekeyed))
	}
	newBlock := rekeyed[block.Ref]

	// The new block must unseal with the new password ...
	if _, err := provider.Authorize(ctx, secretstore.AuthParams{SessionKey: secretstore.BootSessionKey, Password: "new-password"}); err != nil {
		t.Fatalf("authorize new password: %v", err)
	}
	got, err := provider.Unseal(ctx, newBlock)
	if err != nil {
		t.Fatalf("unseal with new password: %v", err)
	}
	if !bytes.Equal(got, []byte(plaintext)) {
		t.Fatalf("plaintext mismatch: got %q", got)
	}

	// ... and must NOT unseal with the old password anymore.
	if _, err := provider.Authorize(ctx, secretstore.AuthParams{SessionKey: secretstore.BootSessionKey, Password: "old-password"}); err != nil {
		t.Fatalf("authorize old password: %v", err)
	}
	if _, err := provider.Unseal(ctx, newBlock); err == nil {
		t.Fatalf("expected unseal with the old password to fail after rekey")
	}
}
