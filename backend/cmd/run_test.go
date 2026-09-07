package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResolveRunChatConfigSources(t *testing.T) {
	t.Run("omitted", func(t *testing.T) {
		config, err := resolveRunChatConfig("", nil)
		if err != nil {
			t.Fatalf("resolveRunChatConfig returned error: %v", err)
		}
		if config.SharedConfig != nil || config.ToolInit != nil || config.ConfigOverrides != nil {
			t.Fatalf("expected empty config, got %#v", config)
		}
	})

	assertConfig := func(t *testing.T, config runChatConfig) {
		t.Helper()
		if !config.ToolInitProvided {
			t.Fatalf("expected tool_init presence to be retained")
		}
		tool, ok := config.ToolInit["opencode_select_project"].(map[string]interface{})
		if !ok || tool["project_uuid"] != "project-123" {
			t.Fatalf("unexpected tool_init: %#v", config.ToolInit)
		}
		if config.ConfigOverrides["opencode_model_source"] != "runtime_default" {
			t.Fatalf("unexpected config overrides: %#v", config.ConfigOverrides)
		}
		if _, exists := config.ConfigOverrides["tool_init"]; exists {
			t.Fatalf("tool_init must be extracted from config overrides")
		}
		if _, exists := config.SharedConfig["tool_init"]; !exists {
			t.Fatalf("legacy shared config must retain tool_init")
		}
	}

	const raw = `{"tool_init":{"opencode_select_project":{"project_uuid":"project-123"}},"opencode_model_source":"runtime_default"}`
	t.Run("inline", func(t *testing.T) {
		config, err := resolveRunChatConfig(raw, nil)
		if err != nil {
			t.Fatalf("resolveRunChatConfig returned error: %v", err)
		}
		assertConfig(t, config)
	})

	t.Run("file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "chat-config.json")
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatalf("write chat config: %v", err)
		}
		config, err := resolveRunChatConfig(path, nil)
		if err != nil {
			t.Fatalf("resolveRunChatConfig returned error: %v", err)
		}
		assertConfig(t, config)
	})

	t.Run("stdin", func(t *testing.T) {
		config, err := resolveRunChatConfig("-", strings.NewReader(raw))
		if err != nil {
			t.Fatalf("resolveRunChatConfig returned error: %v", err)
		}
		assertConfig(t, config)
	})
}

func TestResolveRunChatConfigRejectsInvalidShapes(t *testing.T) {
	tests := []struct {
		name  string
		spec  string
		stdin string
		want  string
	}{
		{name: "array", spec: `[]`, want: "expected one object"},
		{name: "null", spec: "-", stdin: `null`, want: "expected one object"},
		{name: "trailing", spec: `{} {}`, want: "unexpected trailing JSON"},
		{name: "invalid tool init", spec: `{"tool_init":"project-123"}`, want: "tool_init must be an object"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdin *strings.Reader
			if tc.stdin != "" {
				stdin = strings.NewReader(tc.stdin)
			}
			_, err := resolveRunChatConfig(tc.spec, stdin)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestCreateRunInteractionForwardsModernChatConfig(t *testing.T) {
	var received map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/bots/bot-123/interactions" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"chat_uuid": "chat-modern"})
	}))
	defer server.Close()

	client, err := newRunHTTPClient(server.URL, "", time.Second)
	if err != nil {
		t.Fatalf("newRunHTTPClient: %v", err)
	}
	config, err := resolveRunChatConfig(
		`{"tool_init":{"opencode_select_project":{"project_uuid":"project-123"}},"opencode_model_source":"runtime_default"}`,
		nil,
	)
	if err != nil {
		t.Fatalf("resolveRunChatConfig: %v", err)
	}
	chatUUID, err := createRunInteraction(
		context.Background(),
		client,
		runBotLookup{Identifier: "bot-123"},
		"inspect the project",
		config,
	)
	if err != nil {
		t.Fatalf("createRunInteraction: %v", err)
	}
	if chatUUID != "chat-modern" {
		t.Fatalf("unexpected chat uuid %q", chatUUID)
	}
	if received["message"] != "inspect the project" {
		t.Fatalf("unexpected request: %#v", received)
	}
	if _, ok := received["tool_init"].(map[string]interface{}); !ok {
		t.Fatalf("expected tool_init in modern request: %#v", received)
	}
	overrides, ok := received["config_overrides"].(map[string]interface{})
	if !ok || overrides["opencode_model_source"] != "runtime_default" {
		t.Fatalf("unexpected config_overrides: %#v", received["config_overrides"])
	}
}

func TestCreateRunInteractionOmitsEmptyModernChatConfig(t *testing.T) {
	var received map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"chat_uuid": "chat-modern"})
	}))
	defer server.Close()

	client, err := newRunHTTPClient(server.URL, "", time.Second)
	if err != nil {
		t.Fatalf("newRunHTTPClient: %v", err)
	}
	if _, err := createRunInteraction(context.Background(), client, runBotLookup{Identifier: "bot-123"}, "hello", runChatConfig{}); err != nil {
		t.Fatalf("createRunInteraction: %v", err)
	}
	if _, exists := received["tool_init"]; exists {
		t.Fatalf("empty tool_init must be omitted: %#v", received)
	}
	if _, exists := received["config_overrides"]; exists {
		t.Fatalf("empty config_overrides must be omitted: %#v", received)
	}
}

func TestCreateRunInteractionForwardsExplicitEmptyToolInit(t *testing.T) {
	var received map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"chat_uuid": "chat-modern"})
	}))
	defer server.Close()

	client, err := newRunHTTPClient(server.URL, "", time.Second)
	if err != nil {
		t.Fatalf("newRunHTTPClient: %v", err)
	}
	config, err := resolveRunChatConfig(`{"tool_init":{}}`, nil)
	if err != nil {
		t.Fatalf("resolveRunChatConfig: %v", err)
	}
	if _, err := createRunInteraction(context.Background(), client, runBotLookup{Identifier: "bot-123"}, "hello", config); err != nil {
		t.Fatalf("createRunInteraction: %v", err)
	}
	toolInit, exists := received["tool_init"].(map[string]interface{})
	if !exists || len(toolInit) != 0 {
		t.Fatalf("explicit empty tool_init must be forwarded: %#v", received)
	}
}

func TestCreateRunInteractionForwardsLegacySharedConfig(t *testing.T) {
	var received map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/chats/create" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"uuid": "chat-legacy"})
	}))
	defer server.Close()

	client, err := newRunHTTPClient(server.URL, "", time.Second)
	if err != nil {
		t.Fatalf("newRunHTTPClient: %v", err)
	}
	config, err := resolveRunChatConfig(`{"tool_init":{"some_tool":{}},"temperature":0.1}`, nil)
	if err != nil {
		t.Fatalf("resolveRunChatConfig: %v", err)
	}
	chatUUID, err := createRunInteraction(
		context.Background(),
		client,
		runBotLookup{ContactToken: "contact-123", Legacy: true},
		"legacy prompt",
		config,
	)
	if err != nil {
		t.Fatalf("createRunInteraction: %v", err)
	}
	if chatUUID != "chat-legacy" {
		t.Fatalf("unexpected chat uuid %q", chatUUID)
	}
	shared, ok := received["shared_config"].(map[string]interface{})
	if !ok || shared["temperature"] != 0.1 {
		t.Fatalf("unexpected legacy shared_config: %#v", received["shared_config"])
	}
	if _, ok := shared["tool_init"].(map[string]interface{}); !ok {
		t.Fatalf("legacy shared_config must retain tool_init: %#v", shared)
	}
}
