package tools

import (
	"errors"
	"strings"
	"testing"
)

func TestConfirmableActionFailureTextIncludesReason(t *testing.T) {
	text := confirmableActionFailureText("coder_bash", errors.New("no workspace selected for this chat"))
	if !strings.Contains(text, "coder_bash") {
		t.Fatalf("expected the tool name in the failure text, got %q", text)
	}
	if !strings.Contains(text, "no workspace selected for this chat") {
		t.Fatalf("expected the failure reason in the failure text, got %q", text)
	}
}

func TestConfirmableActionFailureTextWithoutReason(t *testing.T) {
	text := confirmableActionFailureText("coder_write", nil)
	if text != "Confirmed action `coder_write` failed." {
		t.Fatalf("expected the bare failure text without a reason, got %q", text)
	}
	blank := confirmableActionFailureText("coder_write", errors.New("   "))
	if blank != "Confirmed action `coder_write` failed." {
		t.Fatalf("expected the bare failure text for a blank reason, got %q", blank)
	}
}

func TestConfirmableActionFailureTextTruncatesHugeReason(t *testing.T) {
	text := confirmableActionFailureText("coder_bash", errors.New(strings.Repeat("x", confirmableActionErrorLimit+500)))
	if !strings.HasSuffix(text, "…") {
		t.Fatalf("expected a truncation marker on a huge reason, got suffix %q", text[len(text)-8:])
	}
	if len(text) > confirmableActionErrorLimit+len("Confirmed action `coder_bash` failed.\n\n")+4 {
		t.Fatalf("expected the reason to be truncated, got length %d", len(text))
	}
}
