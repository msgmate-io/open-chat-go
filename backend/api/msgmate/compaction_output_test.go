package msgmate

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const leakedCompactionTranscript = `<analysis>
Let me chronologically analyze the conversation.

**User's single message (the task):** Write the summary file.
**My actions:** I ran the layout detection command.
</analysis>

<summary>
1. Primary Request and Intent:
   - Detect the frontend layout and write the summary file.
7. Pending Tasks:
   - Write /workspace/open-chat-go/recent-repo-changes-summary.md
9. Optional Next Step:
   Write the file with coder_write.
</summary>`

// casingCompactionTranscript covers a model that upper-cases its tags; the
// comparison is case-insensitive, so such a blob is caught too.
const casingCompactionTranscript = `<ANALYSIS>
The turn ran out of budget.
</ANALYSIS>

<SUMMARY>
Pending: write the file.
</SUMMARY>`

// TestSanitizeCompactionOutputStripsBlobOnly asserts that a message consisting
// solely of a leaked compaction transcript is reported as blob-only and
// stripped, so it can never be stored (or posted) as a turn's answer (ci#562).
func TestSanitizeCompactionOutputStripsBlobOnly(t *testing.T) {
	for name, content := range map[string]string{
		"plain":  leakedCompactionTranscript,
		"fenced": "```\n" + leakedCompactionTranscript + "\n```",
		"casing": casingCompactionTranscript,
		"only":   `<summary>7. Pending Tasks: write the file.</summary>`,
	} {
		sanitized, onlyBlob := sanitizeCompactionOutput(content)
		if !onlyBlob {
			t.Fatalf("%s: expected the transcript to be detected as the round's only output", name)
		}
		if strings.TrimSpace(sanitized) != "" {
			t.Fatalf("%s: expected the transcript to be stripped, got %q", name, sanitized)
		}
	}
}

// TestSanitizeCompactionOutputKeepsProse asserts that a real answer is never
// mistaken for a transcript: the message is returned unchanged and the round is
// not treated as empty.
func TestSanitizeCompactionOutputKeepsProse(t *testing.T) {
	cases := map[string]string{
		"prose":            "Done - the summary file is written.",
		"prose mentioning": "I put the notes in <analysis> notes.\n\nActually, here is the summary:\n\n- one\n- two",
		"fenced code":      "Here is the snippet:\n\n```go\nfmt.Println(\"<summary>\")\n```\n\nThat is all.",
		"empty":            "   \n",
		"markdown link":    "See [backend/api/metrics/handler.go](https://github.com/msgmate-io/open-chat-go/blob/abc/backend/api/metrics/handler.go).",
	}
	for name, content := range cases {
		sanitized, onlyBlob := sanitizeCompactionOutput(content)
		if onlyBlob {
			t.Fatalf("%s: legitimate content must not be treated as a leaked transcript", name)
		}
		if sanitized != content {
			t.Fatalf("%s: content should be unchanged, got %q want %q", name, sanitized, content)
		}
	}
}

// TestCompactionOutputSuspected guards the streaming suppression: an opening
// that may become an <analysis>/<summary> block is withheld (even mid-tag,
// because deltas split it), while ordinary text streams normally.
func TestCompactionOutputSuspected(t *testing.T) {
	suspected := []string{
		"<", "<a", "<ana", "<analysis", "<analysis>", "<summary", "<SUMM",
		"```\n<anal", "\n  <summary>",
	}
	for _, content := range suspected {
		if !compactionOutputSuspected(content) {
			t.Fatalf("%q should be withheld from the stream", content)
		}
	}
	passThrough := []string{
		"", "D", "Done", "<div>", "<strong>", "<s>", "  ", "```", "Here is <analysis> inline",
	}
	for _, content := range passThrough {
		if compactionOutputSuspected(content) {
			t.Fatalf("%q should stream through", content)
		}
	}
}

// TestProcessStreamingResponseReaderDropsCompactionTranscript is the ci#562
// regression at the parser level: a stream whose whole assistant content is a
// compaction transcript must not surface it as the round's answer; the round is
// flagged so the tool loop retries it instead of finishing on the blob.
func TestProcessStreamingResponseReaderDropsCompactionTranscript(t *testing.T) {
	chunkChan := make(chan string, 16)
	usageChan := make(chan *TokenUsage, 2)
	toolChan := make(chan ToolCall, 8)

	// Split the transcript across deltas the way a provider streams it, so the
	// suppression must work on a partial opening too.
	var chunks []map[string]interface{}
	for _, part := range []string{"<ana", "lysis>\nthe turn ", "lost track", "\n</analysis>\n", "<summary>\nPending: write ", "the file.\n</summary>"} {
		chunks = append(chunks, map[string]interface{}{
			"choices": []map[string]interface{}{{"index": 0, "delta": map[string]interface{}{"content": part}}},
		})
	}
	chunks = append(chunks, map[string]interface{}{
		"choices": []map[string]interface{}{{"index": 0, "delta": map[string]interface{}{}, "finish_reason": "stop"}},
	})

	result, err := processStreamingResponseReader(
		context.Background(),
		bufio.NewReader(strings.NewReader(sseFromToolCallChunks(chunks))),
		map[string]Tool{},
		map[string]string{},
		chunkChan,
		usageChan,
		toolChan,
	)
	if err != nil {
		t.Fatalf("processStreamingResponseReader returned error: %v", err)
	}
	if !result.compactionOnly {
		t.Fatalf("expected the blob-only round to be flagged for a retry")
	}
	if got := strings.TrimSpace(result.aiResponse); got != "" {
		t.Fatalf("expected the transcript to be stripped from the round's answer, got %q", got)
	}

	close(chunkChan)
	var streamed strings.Builder
	for chunk := range chunkChan {
		streamed.WriteString(chunk)
	}
	if strings.Contains(strings.ToLower(streamed.String()), "<analysis") || strings.Contains(strings.ToLower(streamed.String()), "<summary") {
		t.Fatalf("the leaked transcript must not be streamed to the client, got %q", streamed.String())
	}
}

// TestBracketToolCallPlaceholderIsRecovered covers the bracket placeholder
// serialization some models fall back to: it must be withheld from the client
// and recovered as a call instead of being stored as the assistant text.
func TestBracketToolCallPlaceholderIsRecovered(t *testing.T) {
	tool := &recordingNonConfirmableTool{BaseTool: &BaseTool{ToolName: "coder_write"}}
	other := &recordingNonConfirmableTool{BaseTool: &BaseTool{ToolName: "coder_read"}}
	toolMap := map[string]Tool{"coder_write": tool, "coder_read": other}

	if !containsInlineToolCall(`[[coder_write path="a.md" content="hi"]]`, toolMap) {
		t.Fatalf("a bracket placeholder for a known tool must be detected")
	}
	// Plain markdown double brackets are not a tool call.
	if containsInlineToolCall("see [[path]](url) in the notes", toolMap) {
		t.Fatalf("a markdown double bracket must not be mistaken for a tool call")
	}

	calls := parseBracketToolCalls(`[[coder_write path="notes/a.md" content="hello"]]`, toolMap)
	if len(calls) != 1 {
		t.Fatalf("expected one recovered call, got %d", len(calls))
	}
	if calls[0].ToolName != "coder_write" {
		t.Fatalf("unexpected tool name %q", calls[0].ToolName)
	}
	if !strings.Contains(calls[0].Arguments, `"path":"notes/a.md"`) || !strings.Contains(calls[0].Arguments, `"content":"hello"`) {
		t.Fatalf("unexpected recovered arguments %q", calls[0].Arguments)
	}
}

// compactionTranscriptSSE is the provider stream a model produces when it
// volunteers its compaction bookkeeping instead of an answer: the whole
// assistant content is one <analysis>/<summary> pair, split across deltas.
func compactionTranscriptSSE() string {
	var b strings.Builder
	for _, part := range []string{
		"<ana", "lysis>\nLet me chronologically ", "analyze.\n</analysis>\n\n",
		"<summary>\n7. Pending Tasks:\n   - write the file\n</summary>",
	} {
		b.WriteString(`data: {"choices":[{"index":0,"delta":{"content":` + mustJSONString(part) + `}}]}` + "\n\n")
	}
	b.WriteString(`data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n")
	b.WriteString("data: [DONE]\n\n")
	return b.String()
}

func mustJSONString(s string) string {
	encoded, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

// TestStreamChatCompletionFailsTurnOnRepeatedCompactionTranscript is the ci#562
// regression end to end: when the provider answers every round with nothing but
// a compaction transcript, the loop retries once and then fails the turn with a
// dedicated, classifiable error — it never finishes on the blob and never
// streams it to the client (the user gets the failure guidance instead).
func TestStreamChatCompletionFailsTurnOnRepeatedCompactionTranscript(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, compactionTranscriptSSE())
	}))
	defer server.Close()

	chunks, usage, toolCalls, errs := streamChatCompletion(
		context.Background(),
		server.URL, "some-model", "litellm", 4, 3,
		[]map[string]interface{}{{"role": "user", "content": "write the summary file"}},
		nil, map[string]Tool{}, "test-key", nil, nil, nil, SamplingParams{},
		ProviderRetryOptions{Enabled: false},
		providerRequestMeta{ChatUUID: "chat-1"},
	)

	var streamed strings.Builder
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for chunk := range chunks {
			streamed.WriteString(chunk)
		}
	}()
	for range usage {
	}
	for range toolCalls {
	}
	streamErr := <-errs
	<-drained

	if streamErr == nil {
		t.Fatal("expected the turn to fail when the model keeps emitting compaction transcripts")
	}
	if !errors.Is(streamErr, errLeakedCompactionTranscript) {
		t.Fatalf("expected errLeakedCompactionTranscript, got %v", streamErr)
	}
	// The turn gets exactly one retry, then fails: the blob is never treated as
	// an answer, so it cannot be persisted as the reply.
	if got := requests.Load(); got != 2 {
		t.Fatalf("expected 2 provider requests (initial + one retry), got %d", got)
	}
	if lower := strings.ToLower(streamed.String()); strings.Contains(lower, "<analysis") || strings.Contains(lower, "<summary") {
		t.Fatalf("the leaked transcript must never be streamed to the client, got %q", streamed.String())
	}
	// The failure is classified on its own reason so the chat explains it with
	// the "work is kept, send another message" guidance instead of a generic
	// internal error (ci#562).
	if got := ClassifyGenerationFailure(streamErr).Reason; got != FailureReasonLeakedCompaction {
		t.Fatalf("expected failure reason %q, got %q", FailureReasonLeakedCompaction, got)
	}
}
