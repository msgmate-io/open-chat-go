package main

import (
	"context"
	"testing"

	"backend/secretstore"
)

func TestConfigSecretResolution(t *testing.T) {
	t.Setenv("OPEN_CHAT_SEAL_KEY", "test-master-key")
	provider := secretstore.Active()
	if provider == nil {
		t.Skip("no secret provider is active in this build")
	}

	password := ""
	if secretstore.ProviderRequiresPassword(provider) {
		password = "test-secret-password"
	}

	ctx := context.Background()
	if _, err := provider.Authorize(ctx, secretstore.AuthParams{
		SessionKey: secretstore.BootSessionKey,
		Password:   password,
	}); err != nil {
		t.Fatalf("authorize: %v", err)
	}
	block, err := provider.Seal(ctx, secretstore.SealParams{
		Scope:     "config",
		Label:     "OPENAI_API_KEY",
		Plaintext: []byte("sk-test-value"),
	})
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	cfg := openChatConfig{
		Env: map[string]interface{}{
			"OPENAI_API_KEY": map[string]interface{}{tvaultRefKey: block.Ref},
			"PLAIN_VALUE":    "plain",
		},
		Secrets: map[string]secretstore.Block{block.Ref: block},
	}

	args := []string{"open-chat", "server"}
	if password != "" {
		args = append(args, "--secret-password", password)
	}
	secretValues, err := resolveConfigSecretValues(cfg, args)
	if err != nil {
		t.Fatalf("resolve config secrets: %v", err)
	}
	updates, err := flattenConfigEnv(cfg, secretValues)
	if err != nil {
		t.Fatalf("flatten: %v", err)
	}
	if updates["OPENAI_API_KEY"] != "sk-test-value" {
		t.Fatalf("expected decrypted value, got %q", updates["OPENAI_API_KEY"])
	}
	if updates["PLAIN_VALUE"] != "plain" {
		t.Fatalf("expected plain value, got %q", updates["PLAIN_VALUE"])
	}
}

func TestFlattenConfigEnvRejectsDanglingRef(t *testing.T) {
	cfg := openChatConfig{
		Env: map[string]interface{}{
			"OPENAI_API_KEY": map[string]interface{}{tvaultRefKey: "missing-ref"},
		},
	}
	if _, err := flattenConfigEnv(cfg, nil); err == nil {
		t.Fatalf("expected a dangling $tvault reference to fail")
	}
}

func TestExtractSecretRef(t *testing.T) {
	if ref, ok := extractSecretRef(map[string]interface{}{tvaultRefKey: "abc"}); !ok || ref != "abc" {
		t.Fatalf("expected marker map to resolve, got %q ok=%v", ref, ok)
	}
	if ref, ok := extractSecretRef("tvault://abc"); !ok || ref != "abc" {
		t.Fatalf("expected tvault:// string to resolve, got %q ok=%v", ref, ok)
	}
	if _, ok := extractSecretRef(map[string]interface{}{tvaultRefKey: "abc", "extra": "x"}); ok {
		t.Fatalf("expected multi-key map to be ignored")
	}
	if _, ok := extractSecretRef("plain"); ok {
		t.Fatalf("expected plain string to be ignored")
	}
}
