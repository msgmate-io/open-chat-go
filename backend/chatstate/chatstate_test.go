package chatstate

import "testing"

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
