package chatstate

import "testing"

func TestBackendInterruptRegistry(t *testing.T) {
	if _, ok := LookupBackendInterruptHandler("missing-backend"); ok {
		t.Fatalf("expected no handler for an unregistered backend")
	}

	called := ""
	RegisterBackendInterruptHandler(" OpenCode ", func(chatUUID string) bool {
		called = chatUUID
		return true
	})
	handler, ok := LookupBackendInterruptHandler("opencode")
	if !ok {
		t.Fatalf("expected handler lookup to be case/space normalized")
	}
	if !handler("chat-1") || called != "chat-1" {
		t.Fatalf("expected handler to be invoked with the chat uuid, got %q", called)
	}

	RegisterBackendInterruptHandler("", func(string) bool { return true })
	RegisterBackendInterruptHandler("nil-fn", nil)
	if _, ok := LookupBackendInterruptHandler(""); ok {
		t.Fatalf("empty backend name must not register a handler")
	}
	if _, ok := LookupBackendInterruptHandler("nil-fn"); ok {
		t.Fatalf("nil handler must not register")
	}
}

func TestChatBackendNameFromConfig(t *testing.T) {
	tests := []struct {
		name   string
		config map[string]interface{}
		want   string
	}{
		{name: "nil config", config: nil, want: ""},
		{name: "empty config", config: map[string]interface{}{}, want: ""},
		{name: "explicit chat_backend", config: map[string]interface{}{"chat_backend": "opencode", "backend": "deepinfra"}, want: "opencode"},
		{name: "chat_backend case and space normalized", config: map[string]interface{}{"chat_backend": " OpenCode "}, want: "opencode"},
		{name: "legacy backend fallback", config: map[string]interface{}{"backend": "opencode"}, want: "opencode"},
		{name: "empty chat_backend falls back to backend", config: map[string]interface{}{"chat_backend": "  ", "backend": "opencode"}, want: "opencode"},
		{name: "llm provider backend passes through", config: map[string]interface{}{"backend": "deepinfra"}, want: "deepinfra"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ChatBackendNameFromConfig(tt.config); got != tt.want {
				t.Fatalf("ChatBackendNameFromConfig() = %q, want %q", got, tt.want)
			}
		})
	}
}
