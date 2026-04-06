package msgmate

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// ci#630 finding 7: a tool result must be capped before it is written onto the
// message `tool_calls` row / replayed from storage, so one message can no longer
// reach megabytes of raw file or command output.

func TestTruncatePersistedToolResultPassesSmallResultThrough(t *testing.T) {
	t.Setenv(PersistedToolResultMaxCharsEnvKey, "1000")
	result := "short tool output"
	if got := truncatePersistedToolResult(result); got != result {
		t.Fatalf("expected small result to pass through unchanged, got %q", got)
	}
}

func TestTruncatePersistedToolResultKeepsExactlyAtLimit(t *testing.T) {
	t.Setenv(PersistedToolResultMaxCharsEnvKey, "10")
	result := strings.Repeat("a", 10)
	if got := truncatePersistedToolResult(result); got != result {
		t.Fatalf("expected result at the limit to pass through unchanged, got %q", got)
	}
}

func TestTruncatePersistedToolResultKeepsHeadAndTailWithMarker(t *testing.T) {
	t.Setenv(PersistedToolResultMaxCharsEnvKey, "100")
	result := strings.Repeat("H", 500) + strings.Repeat("T", 500)

	got := truncatePersistedToolResult(result)

	if len([]rune(got)) >= len([]rune(result)) {
		t.Fatalf("expected a truncated payload, got %d runes", len([]rune(got)))
	}
	if !strings.HasPrefix(got, strings.Repeat("H", 60)) {
		t.Fatalf("expected the head to be kept, got prefix %q", got[:10])
	}
	if !strings.HasSuffix(got, strings.Repeat("T", 40)) {
		t.Fatalf("expected the tail to be kept, got suffix %q", got[len(got)-10:])
	}
	if !strings.Contains(got, "truncated by msgmate") {
		t.Fatalf("expected an elision marker in the payload, got %q", got)
	}
	if !strings.Contains(got, "900 of 1000 chars elided") {
		t.Fatalf("expected the marker to report the elided size, got %q", got)
	}
	// The marker names the remedy so the model does not silently lose output.
	if !strings.Contains(got, "narrower scope") {
		t.Fatalf("expected the marker to hint at the remedy, got %q", got)
	}
	// Budget is respected: exactly `limit` original runes survive, 3/5 head
	// and 2/5 tail, so the marker is the only extra content.
	head := strings.Repeat("H", 60)
	tail := strings.Repeat("T", 40)
	if !strings.HasPrefix(got, head) {
		t.Fatalf("expected exactly %d head runes, got %q", len(head), got[:70])
	}
	if !strings.HasSuffix(got, tail) {
		t.Fatalf("expected exactly %d tail runes, got %q", len(tail), got[len(got)-50:])
	}
	kept := len([]rune(got)) - len([]rune(strings.TrimPrefix(strings.TrimSuffix(got, tail), head)))
	if kept != 100 {
		t.Fatalf("expected 100 kept original runes, got %d", kept)
	}
}

func TestTruncatePersistedToolResultStaysValidUTF8(t *testing.T) {
	t.Setenv(PersistedToolResultMaxCharsEnvKey, "101")
	// Multi-byte runes straddling both cut points must not be split.
	result := strings.Repeat("é", 500) + strings.Repeat("漢", 500)

	got := truncatePersistedToolResult(result)

	if !utf8.ValidString(got) {
		t.Fatalf("expected valid UTF-8 after truncation")
	}
	if strings.ContainsRune(got, utf8.RuneError) {
		t.Fatalf("expected no replacement runes after truncation")
	}
}

func TestTruncatePersistedToolResultZeroLimitDisablesCap(t *testing.T) {
	t.Setenv(PersistedToolResultMaxCharsEnvKey, "0")
	result := strings.Repeat("x", 100000)
	if got := truncatePersistedToolResult(result); got != result {
		t.Fatalf("expected a zero limit to disable the cap")
	}
}

func TestTruncatePersistedToolResultInvalidEnvFallsBackToDefault(t *testing.T) {
	t.Setenv(PersistedToolResultMaxCharsEnvKey, "not-a-number")
	if got := persistedToolResultLimit(); got != PersistedToolResultMaxChars {
		t.Fatalf("expected the compiled-in default for an unparsable limit, got %d", got)
	}
}

func TestTruncatePersistedToolResultEmptyAndTinyLimits(t *testing.T) {
	t.Setenv(PersistedToolResultMaxCharsEnvKey, "1")
	result := strings.Repeat("y", 50)
	got := truncatePersistedToolResult(result)
	if !strings.HasPrefix(got, "y") {
		t.Fatalf("expected at least one kept rune, got %q", got)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("expected valid UTF-8 for a tiny limit")
	}
}

func TestPersistedToolResultEnvKeyIsNamespaced(t *testing.T) {
	if PersistedToolResultMaxCharsEnvKey != "OCI_MSGMATE_CODER_PERSISTED_TOOL_RESULT_MAX_CHARS" {
		t.Fatalf("unexpected env key %q", PersistedToolResultMaxCharsEnvKey)
	}
	if PersistedToolResultMaxChars <= DefaultToolResultMaxCharsForModel {
		t.Fatalf(
			"persisted cap (%d) must exceed the live model cap (%d) so storage keeps the UI useful",
			PersistedToolResultMaxChars, DefaultToolResultMaxCharsForModel,
		)
	}
}
