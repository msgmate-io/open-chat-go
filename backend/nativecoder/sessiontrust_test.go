package nativecoder

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func trustClock(step int) time.Time {
	return time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC).Add(time.Duration(step) * time.Minute)
}

func TestNormalizeCoderTrustClasses(t *testing.T) {
	got, err := NormalizeCoderTrustClasses([]string{" SHELL ", "write", "shell"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Join(got, ",") != "write,shell" {
		t.Fatalf("expected canonical order write,shell, got %v", got)
	}
	all, err := NormalizeCoderTrustClasses([]string{"all"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(all) != len(CoderSessionTrustClasses()) {
		t.Fatalf("expected all to expand to %d classes, got %d", len(CoderSessionTrustClasses()), len(all))
	}
	for _, class := range all {
		if class == CoderTrustClassAll {
			t.Fatalf("the all alias must never be a stored class: %v", all)
		}
	}
	if _, err := NormalizeCoderTrustClasses([]string{"root"}); !errors.Is(err, ErrCoderTrustUnknownClass) {
		t.Fatalf("unknown class must be rejected, got %v", err)
	}
	if _, err := NormalizeCoderTrustClasses(nil); !errors.Is(err, ErrCoderTrustEmptyClasses) {
		t.Fatalf("an empty grant must be rejected, got %v", err)
	}
}

func TestCoderTrustClassForTool(t *testing.T) {
	for tool, want := range map[string]string{
		"coder_read":      CoderTrustClassRead,
		"coder_grep":      CoderTrustClassRead,
		"coder_write":     CoderTrustClassWrite,
		"coder_edit":      CoderTrustClassWrite,
		"coder_patch":     CoderTrustClassWrite,
		"coder_bash":      CoderTrustClassShell,
		"coder_git":       CoderTrustClassGit,
		"coder_websearch": CoderTrustClassNetwork,
		"coder_webfetch":  CoderTrustClassNetwork,
	} {
		if got := CoderTrustClassForTool(tool); got != want {
			t.Fatalf("%s: expected class %q, got %q", tool, want, got)
		}
	}
	if got := CoderTrustClassForTool("coder_something_new"); got != "" {
		t.Fatalf("an unknown tool must have no class, got %q", got)
	}
	if CoderToolNeedsSessionTrust("coder_read") {
		t.Fatal("read-only tools must not consume trust")
	}
	for _, tool := range []string{"coder_write", "coder_edit", "coder_bash", "coder_git"} {
		if !CoderToolNeedsSessionTrust(tool) {
			t.Fatalf("%s is mutating and must be coverable by trust", tool)
		}
	}
	if CoderToolNeedsSessionTrust("coder_unknown") {
		t.Fatal("a tool without a class must never be trusted")
	}
}

func TestCoderSessionTrustStoreGrantRevoke(t *testing.T) {
	store := NewCoderSessionTrustStore()
	if !store.State("chat-1").Empty() {
		t.Fatal("a fresh session must trust nothing")
	}
	state, err := store.Grant("chat-1", []string{CoderTrustClassShell}, trustClock(0))
	if err != nil {
		t.Fatalf("grant failed: %v", err)
	}
	if state.Revision != 1 || !state.Allows(CoderTrustClassShell) || state.Allows(CoderTrustClassWrite) {
		t.Fatalf("unexpected state after grant: %+v", state)
	}
	if !store.AllowsTool("chat-1", "coder_bash") {
		t.Fatal("a trusted shell class must cover coder_bash")
	}
	if store.AllowsTool("chat-1", "coder_write") {
		t.Fatal("write must stay confirmed while only shell is trusted")
	}
	if store.AllowsTool("chat-1", "coder_read") {
		t.Fatal("read-only tools never need trust")
	}
	if store.AllowsTool("chat-2", "coder_bash") {
		t.Fatal("trust must not leak to another chat")
	}
	state2, err := store.Grant("chat-1", []string{CoderTrustClassWrite}, trustClock(1))
	if err != nil {
		t.Fatalf("grant failed: %v", err)
	}
	if state2.Revision != 2 || !state2.AllowsTool("coder_write") || !state2.AllowsTool("coder_bash") {
		t.Fatalf("grants must be additive: %+v", state2)
	}
	if want := trustClock(1).UTC().Format(time.RFC3339); state2.UpdatedAt != want {
		t.Fatalf("expected updated_at %q, got %q", want, state2.UpdatedAt)
	}
	after, err := store.Revoke("chat-1", []string{CoderTrustClassShell}, trustClock(2))
	if err != nil {
		t.Fatalf("revoke failed: %v", err)
	}
	if after.AllowsTool("coder_bash") || !after.AllowsTool("coder_write") {
		t.Fatalf("revoke must drop only the named class: %+v", after)
	}
	if after.Revision != 3 {
		t.Fatalf("revoke must bump the revision, got %d", after.Revision)
	}
	if got := store.RevokeAll("chat-1", trustClock(3)); !got.Empty() {
		t.Fatalf("revoke all must clear every class: %+v", got)
	}
	if _, err := store.Grant("chat-1", nil, trustClock(4)); !errors.Is(err, ErrCoderTrustEmptyClasses) {
		t.Fatalf("an empty grant must fail: %v", err)
	}
	if _, err := store.Grant("  ", []string{"all"}, trustClock(5)); !errors.Is(err, ErrCoderTrustEmptyChat) {
		t.Fatalf("a chat id is required: %v", err)
	}
	store.Grant("chat-9", []string{CoderTrustClassAll}, trustClock(6))
	store.Clear("chat-9")
	if !store.State("chat-9").Empty() {
		t.Fatal("Clear must forget the session trust")
	}
}

func TestCoderSessionTrustStateRendering(t *testing.T) {
	empty := CoderSessionTrustState{Classes: []string{}, Version: CoderSessionTrustVersion}
	if meta := empty.Metadata(); meta["trusted"] != false || meta["revision"] != 0 {
		t.Fatalf("unexpected empty metadata: %+v", meta)
	}
	state := CoderSessionTrustState{Classes: []string{CoderTrustClassShell}, Revision: 2, Version: CoderSessionTrustVersion}
	meta := state.Metadata()
	if meta["shell"] != true || meta["git"] != false || meta["trusted"] != true {
		t.Fatalf("unexpected metadata: %+v", meta)
	}
	if _, ok := meta["classes"].([]string); !ok {
		t.Fatalf("classes must render as a list, got %T", meta["classes"])
	}
	if line := RenderCoderSessionTrust(state); !strings.Contains(line, CoderTrustClassShell) {
		t.Fatalf("rendered trust must name the classes: %q", line)
	}
	encoded, err := CoderSessionTrustJSON(empty)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if !strings.Contains(encoded, `"classes":[]`) {
		t.Fatalf("an empty trust must encode as an empty list, got %s", encoded)
	}
	for _, leak := range []string{"token", "secret", "prompt"} {
		if strings.Contains(strings.ToLower(encoded), leak) {
			t.Fatalf("the trust state must not carry %q: %s", leak, encoded)
		}
	}
}

func TestCoderSessionTrustGapIsTracked(t *testing.T) {
	gap, ok := OpenCodeParityGap("session_trust")
	if !ok {
		t.Fatal("the session_trust gap must stay in the parity registry")
	}
	if gap.Status == ParityStatusClosed {
		t.Fatal("session_trust must not be closed while coder_trust is not exposed")
	}
	if !gap.BlocksRetirement {
		t.Fatal("session_trust is a retirement blocker until the tool ships")
	}
}
