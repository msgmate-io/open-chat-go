package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

// fakeRecallRunner records the request it receives so the tests can assert the
// bounds and the trusted runtime the tool layer hands to the core.
type fakeRecallRunner struct {
	got     CoderRecallRequest
	calls   int
	payload map[string]interface{}
	err     error
}

func (f *fakeRecallRunner) RecallCoderInteractions(req CoderRecallRequest) (map[string]interface{}, error) {
	f.calls++
	f.got = req
	if f.err != nil {
		return nil, f.err
	}
	if f.payload != nil {
		return f.payload, nil
	}
	return map[string]interface{}{}, nil
}

func recallInit() map[string]interface{} {
	return map[string]interface{}{
		"chat_uuid": "chat-running",
		"_runtime": map[string]interface{}{
			"chat_uuid":     "chat-running",
			"bot_user_uuid": "bot-1",
			"sender_uuid":   "user-1",
		},
	}
}

func TestRunCoderRecallRequiresChatIdentityAndRunner(t *testing.T) {
	if _, err := runCoderRecall(CoderRecallInput{}, map[string]interface{}{}, &fakeRecallRunner{}); err == nil {
		t.Fatal("expected an error when the running chat identity is missing")
	}
	runner := &fakeRecallRunner{}
	if _, err := runCoderRecall(CoderRecallInput{}, recallInit(), nil); err == nil {
		t.Fatal("expected an error when no recall runner is published")
	}
	if runner.calls != 0 {
		t.Fatal("runner must not be called without validation")
	}
}

func TestRunCoderRecallPassesTrustedRuntimeAndDefaults(t *testing.T) {
	runner := &fakeRecallRunner{}
	out, err := runCoderRecall(CoderRecallInput{}, recallInit(), runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if runner.got.ChatUUID != "chat-running" {
		t.Fatalf("chat uuid = %q, want chat-running", runner.got.ChatUUID)
	}
	if runner.got.BotUserUUID != "bot-1" || runner.got.SenderUUID != "user-1" {
		t.Fatalf("scope = (%q,%q), want (bot-1,user-1)", runner.got.BotUserUUID, runner.got.SenderUUID)
	}
	if runner.got.Limit != coderRecallDefaultLimit {
		t.Fatalf("limit = %d, want %d", runner.got.Limit, coderRecallDefaultLimit)
	}
	if runner.got.ExcerptBytes != coderRecallDefaultExcerptBytes {
		t.Fatalf("excerpt = %d, want %d", runner.got.ExcerptBytes, coderRecallDefaultExcerptBytes)
	}
	if !runner.got.IncludeCurrent {
		t.Fatal("include_current must default to true")
	}
	if runner.got.Query != "" || runner.got.ChatFilter != "" {
		t.Fatalf("query/filter must stay empty, got (%q,%q)", runner.got.Query, runner.got.ChatFilter)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("output is not JSON: %v", err)
	}
	if decoded["mode"] != "interactions" {
		t.Fatalf("mode = %v, want interactions", decoded["mode"])
	}
}

func TestRunCoderRecallBoundsAndSearchMode(t *testing.T) {
	runner := &fakeRecallRunner{}
	includeCurrent := false
	_, err := runCoderRecall(CoderRecallInput{
		Query:          "  deploy target  ",
		ChatUUID:       "chat-past",
		Limit:          500,
		ExcerptBytes:   999999,
		IncludeCurrent: &includeCurrent,
	}, recallInit(), runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if runner.got.Query != "deploy target" {
		t.Fatalf("query = %q, want trimmed value", runner.got.Query)
	}
	if runner.got.ChatFilter != "chat-past" {
		t.Fatalf("chat filter = %q, want chat-past", runner.got.ChatFilter)
	}
	if runner.got.Limit != coderRecallMaxLimit {
		t.Fatalf("limit = %d, want cap %d", runner.got.Limit, coderRecallMaxLimit)
	}
	if runner.got.ExcerptBytes != coderRecallMaxExcerptBytes {
		t.Fatalf("excerpt = %d, want cap %d", runner.got.ExcerptBytes, coderRecallMaxExcerptBytes)
	}
	if runner.got.IncludeCurrent {
		t.Fatal("include_current=false must be forwarded")
	}
}

func TestRunCoderRecallRejectsInvalidInput(t *testing.T) {
	runner := &fakeRecallRunner{}
	if _, err := runCoderRecall(CoderRecallInput{Limit: -1}, recallInit(), runner); err == nil {
		t.Fatal("expected an error for a negative limit")
	}
	if _, err := runCoderRecall(CoderRecallInput{ExcerptBytes: -1}, recallInit(), runner); err == nil {
		t.Fatal("expected an error for a negative excerpt budget")
	}
	longQuery := strings.Repeat("a", coderRecallMaxQueryChars+1)
	if _, err := runCoderRecall(CoderRecallInput{Query: longQuery}, recallInit(), runner); err == nil {
		t.Fatal("expected an error for an over-long query")
	}
	if runner.calls != 0 {
		t.Fatalf("runner must not be called for invalid input, got %d calls", runner.calls)
	}
}

func TestCoderRecallInputOfAcceptsPointerAndValue(t *testing.T) {
	want := CoderRecallInput{Query: "q", Limit: 3}
	if got := coderRecallInputOf(want); got != want {
		t.Fatalf("value form = %+v, want %+v", got, want)
	}
	if got := coderRecallInputOf(&want); got != want {
		t.Fatalf("pointer form = %+v, want %+v", got, want)
	}
	if got := coderRecallInputOf(nil); got != (CoderRecallInput{}) {
		t.Fatalf("nil input = %+v, want zero value", got)
	}
}
