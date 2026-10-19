package nativecoder

import (
	"reflect"
	"testing"
)

// TestDefaultInteractionTagsCoversPerBotAndPerRepo pins ci#650's contract: a
// native coder interaction is labelled with the marker tag plus a stable
// per-bot and per-repo tag, while non-coder bots keep exactly the tags their
// caller asked for.
func TestDefaultInteractionTagsCoversPerBotAndPerRepo(t *testing.T) {
	cases := []struct {
		name     string
		config   map[string]interface{}
		bot      string
		explicit []string
		want     []string
	}{
		{
			name:     "non-coder config is untouched",
			config:   map[string]interface{}{"model": "gpt-4o"},
			bot:      "assistant-bot",
			explicit: []string{"project:open-chat"},
			want:     []string{"project:open-chat"},
		},
		{
			name:     "coder chat gets marker and per-bot tag",
			config:   map[string]interface{}{"coder_interaction": "autonomous"},
			bot:      "msgmate coder",
			explicit: []string{"issue:650"},
			want:     []string{"issue:650", "msgmate-coder", "bot:msgmate-coder"},
		},
		{
			name:     "repo is derived from the shared config",
			config:   map[string]interface{}{"coder_mode": "sandbox", "repo": "msgmate-io/open-chat-go"},
			bot:      "coder",
			explicit: nil,
			want:     []string{"msgmate-coder", "bot:coder", "repo:msgmate-io/open-chat-go"},
		},
		{
			name:     "an explicit repo tag wins over the config",
			config:   map[string]interface{}{"coder_mode": "sandbox", "repo": "msgmate-io/other"},
			bot:      "coder",
			explicit: []string{"repo:open-chat-go"},
			want:     []string{"repo:open-chat-go", "msgmate-coder", "bot:coder"},
		},
		{
			name:     "an explicit bot tag wins over the bot name",
			config:   map[string]interface{}{"coder_mode": "sandbox"},
			bot:      "coder",
			explicit: []string{"bot:custom"},
			want:     []string{"bot:custom", "msgmate-coder"},
		},
		{
			name:     "a nameless bot still gets a per-bot tag",
			config:   map[string]interface{}{"coder_mode": "sandbox"},
			bot:      "   ",
			explicit: nil,
			want:     []string{"msgmate-coder", "bot:coder"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DefaultInteractionTags(tc.config, tc.bot, tc.explicit)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("DefaultInteractionTags() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestNormalizeRepo moved to repo_test.go together with the shared
// implementation (ci#650 + ci#608 both declared it; see ci#730).
