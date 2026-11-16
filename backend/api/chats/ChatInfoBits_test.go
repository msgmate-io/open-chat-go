package chats

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestChatSettingsViewCarriesInfoBits pins the ci#1027 contract: the chat
// settings projection carries the title AND the information bits an integration
// attached, so the widget can render a link strip below the chat header.
func TestChatSettingsViewCarriesInfoBits(t *testing.T) {
	raw := json.RawMessage(`{
		"title": "Issue #920",
		"info_bits": [
			{"label": "Issue", "value": "a-little-world/little-world-backend#920", "href": "https://github.com/a-little-world/little-world-backend/issues/920"},
			{"label": "Trigger", "value": "mention"}
		]
	}`)
	view := chatSettingsViewFromRaw(raw)
	if view == nil {
		t.Fatalf("expected a settings view")
	}
	if view.Title != "Issue #920" {
		t.Fatalf("title = %q", view.Title)
	}
	if len(view.InfoBits) != 2 {
		t.Fatalf("info bits = %d, want 2 (%+v)", len(view.InfoBits), view.InfoBits)
	}
	first := view.InfoBits[0]
	if first.Label != "Issue" || !strings.HasSuffix(first.Href, "/issues/920") {
		t.Fatalf("first bit = %+v", first)
	}
	if view.InfoBits[1].Href != "" {
		t.Fatalf("expected a link-less bit, got %+v", view.InfoBits[1])
	}
}

// TestChatSettingsViewInfoBitsOnly covers chats that carry no title but do carry
// bits: the projection must still be returned.
func TestChatSettingsViewInfoBitsOnly(t *testing.T) {
	view := chatSettingsViewFromRaw(json.RawMessage(`{"info_bits":[{"label":"Repo","value":"msgmate-io/open-chat-go"}]}`))
	if view == nil {
		t.Fatalf("expected a settings view for info-bits-only settings")
	}
	if view.Title != "" {
		t.Fatalf("unexpected title %q", view.Title)
	}
	if len(view.InfoBits) != 1 {
		t.Fatalf("info bits = %+v", view.InfoBits)
	}
}

// TestChatSettingsViewEmpty covers the pre-ci#1027 rows: no title, no bits.
func TestChatSettingsViewEmpty(t *testing.T) {
	for _, raw := range []string{`{}`, `{"info_bits":[]}`, `{"info_bits":[{"value":"orphan"}]}`} {
		if view := chatSettingsViewFromRaw(json.RawMessage(raw)); view != nil {
			t.Fatalf("raw %s produced %+v, want nil", raw, view)
		}
	}
}

// TestNormalizeChatInfoBits checks the sanitising rules.
func TestNormalizeChatInfoBits(t *testing.T) {
	bits := normalizeChatInfoBits([]ChatInfoBit{
		{Label: "  Issue  ", Href: "  https://github.com/o/r/issues/1  "},
		{Label: "Script", Value: "click me", Href: "javascript:alert(1)"},
		{Label: "", Value: "no label"},
		{Label: "Relative", Href: "/chat/abc"},
		{Label: "Proto", Href: "//evil.example/x"},
	})
	if len(bits) != 3 {
		t.Fatalf("bits = %+v", bits)
	}
	if bits[0].Label != "Issue" || bits[0].Value != "https://github.com/o/r/issues/1" {
		t.Fatalf("value should fall back to the trimmed href: %+v", bits[0])
	}
	if bits[1].Href != "" || bits[1].Value != "click me" {
		t.Fatalf("unsafe href must be dropped, value kept: %+v", bits[1])
	}
	if bits[2].Href != "/chat/abc" {
		t.Fatalf("site-relative href must survive: %+v", bits[2])
	}
}

// TestNormalizeChatInfoBitsLimits caps the strip so a chat header stays compact.
func TestNormalizeChatInfoBitsLimits(t *testing.T) {
	in := make([]ChatInfoBit, 0, maxChatInfoBits+5)
	for i := 0; i < maxChatInfoBits+5; i++ {
		in = append(in, ChatInfoBit{Label: "l", Value: "v"})
	}
	if got := len(normalizeChatInfoBits(in)); got != maxChatInfoBits {
		t.Fatalf("bits = %d, want %d", got, maxChatInfoBits)
	}
	long := normalizeChatInfoBits([]ChatInfoBit{{Label: strings.Repeat("x", 500), Value: strings.Repeat("y", 500)}})
	if len([]rune(long[0].Label)) != maxChatInfoBitLabel || len([]rune(long[0].Value)) != maxChatInfoBitValue {
		t.Fatalf("length caps not applied: %d/%d", len([]rune(long[0].Label)), len([]rune(long[0].Value)))
	}
}
