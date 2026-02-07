package tools

import (
	"errors"
	"strings"
	"testing"
)

// TestConfirmableFailureTextIncludesReason guards ci#525: a failed confirmable
// action must explain why it failed, not just that it did.
func TestConfirmableFailureTextIncludesReason(t *testing.T) {
	got := confirmableFailureText("coder_bash", errors.New("no workspace selected for this chat; call coder_select_workspace first"))
	if !strings.Contains(got, "Confirmed action `coder_bash` failed.") {
		t.Fatalf("missing failure headline: %q", got)
	}
	if !strings.Contains(got, "no workspace selected for this chat") {
		t.Fatalf("missing failure reason: %q", got)
	}
}

func TestConfirmableFailureTextWithoutReason(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"nil error", nil},
		{"empty error", errors.New("   ")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := confirmableFailureText("coder_bash", tc.err)
			if got != "Confirmed action `coder_bash` failed." {
				t.Fatalf("expected bare failure text, got %q", got)
			}
		})
	}
}

func TestClipConfirmableErrorTruncates(t *testing.T) {
	short := "boom"
	if clipConfirmableError(short) != short {
		t.Fatalf("short error must be unchanged")
	}
	long := strings.Repeat("x", 5000)
	clipped := clipConfirmableError(long)
	if !strings.HasSuffix(clipped, "… (truncated)") {
		t.Fatalf("expected truncation marker, got suffix %q", clipped[len(clipped)-20:])
	}
	if len([]rune(strings.TrimSuffix(clipped, "\n… (truncated)"))) != 4000 {
		t.Fatalf("expected 4000 runes before the marker")
	}
}
