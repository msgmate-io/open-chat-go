package msgmate

import (
	"bufio"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// orderedRecordingTool records every decoded input it is run with, so a test
// can assert that each reassembled tool call ran exactly once, with the full
// (not partial) arguments, in call order.
type orderedRecordingTool struct {
	*BaseTool
	inputs []string
}

func (t *orderedRecordingTool) RunTool(input interface{}) (string, error) {
	encoded, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	t.inputs = append(t.inputs, string(encoded))
	return "result:" + string(encoded), nil
}

// interleavedToolCallChunk renders one delta carrying a single tool-call entry.
func interleavedToolCallChunk(entry map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"choices": []map[string]interface{}{{
			"index": 0,
			"delta": map[string]interface{}{
				"tool_calls": []interface{}{entry},
			},
		}},
	}
}

func entryWithIndex(index int, id, name, arguments string) map[string]interface{} {
	entry := map[string]interface{}{"index": index}
	if id != "" {
		entry["id"] = id
	}
	fn := map[string]interface{}{}
	if name != "" {
		fn["name"] = name
	}
	if arguments != "" {
		fn["arguments"] = arguments
	}
	if len(fn) > 0 {
		entry["function"] = fn
	}
	return entry
}

// TestProcessStreamingResponseReaderReassemblesInterleavedBatch is the ci#655
// regression: a batch of tool calls whose argument deltas are interleaved must
// be reassembled per call (by index), each executed exactly once with its full
// arguments, and surfaced in call order — not run as each partial delta lands.
func TestProcessStreamingResponseReaderReassemblesInterleavedBatch(t *testing.T) {
	tool := &orderedRecordingTool{BaseTool: &BaseTool{ToolName: "coder_multi"}}
	toolMap := map[string]Tool{"coder_multi": tool}

	sse := sseFromToolCallChunks([]map[string]interface{}{
		interleavedToolCallChunk(entryWithIndex(0, "call-0", "coder_multi", `{"path":"a`)),
		interleavedToolCallChunk(entryWithIndex(1, "call-1", "coder_multi", `{"path":"b`)),
		interleavedToolCallChunk(entryWithIndex(0, "", "", `.txt"}`)),
		interleavedToolCallChunk(entryWithIndex(1, "", "", `.txt"}`)),
	})

	chunkChan := make(chan string, 8)
	usageChan := make(chan *TokenUsage, 2)
	toolChan := make(chan ToolCall, 16)

	result, err := processStreamingResponseReader(
		context.Background(),
		bufio.NewReader(strings.NewReader(sse)),
		toolMap,
		map[string]string{},
		chunkChan,
		usageChan,
		toolChan,
	)
	if err != nil {
		t.Fatalf("stream processing failed: %v", err)
	}
	if len(tool.inputs) != 2 {
		t.Fatalf("expected each interleaved call to run exactly once, got %d runs: %v", len(tool.inputs), tool.inputs)
	}
	if tool.inputs[0] != `{"path":"a.txt"}` {
		t.Fatalf("first call ran with incomplete arguments: %q", tool.inputs[0])
	}
	if tool.inputs[1] != `{"path":"b.txt"}` {
		t.Fatalf("second call ran with incomplete arguments: %q", tool.inputs[1])
	}
	if len(result.calls) != 2 || result.calls[0].id != "call-0" || result.calls[1].id != "call-1" {
		t.Fatalf("expected the round's calls in emission order with their ids, got %+v", result.calls)
	}
	if result.calls[0].arguments != `{"path":"a.txt"}` || result.calls[1].arguments != `{"path":"b.txt"}` {
		t.Fatalf("expected complete arguments per call, got %+v", result.calls)
	}

	close(toolChan)
	var finalIDs []string
	for tc := range toolChan {
		if tc.Status != ToolCallStatusSucceeded {
			continue
		}
		finalIDs = append(finalIDs, tc.Id)
	}
	if len(finalIDs) != 2 || finalIDs[0] != "call-0" || finalIDs[1] != "call-1" {
		t.Fatalf("expected completed tool calls surfaced in order, got %v", finalIDs)
	}
}

// TestProcessStreamingResponseReaderExecutesSplitArgumentsOnce guards the other
// half of ci#655: a single call whose arguments arrive across several deltas
// (no index) must run once with the full arguments, not once per delta.
func TestProcessStreamingResponseReaderExecutesSplitArgumentsOnce(t *testing.T) {
	tool := &orderedRecordingTool{BaseTool: &BaseTool{ToolName: "coder_bash"}}
	toolMap := map[string]Tool{"coder_bash": tool}

	sse := sseFromToolCallChunks([]map[string]interface{}{
		interleavedToolCallChunk(entryWithIndex(0, "call-x", "coder_bash", `{"command":"echo `)),
		// Continuation delta: no index, no id, arguments only.
		interleavedToolCallChunk(map[string]interface{}{
			"function": map[string]interface{}{"arguments": `hello"}`},
		}),
	})

	chunkChan := make(chan string, 8)
	usageChan := make(chan *TokenUsage, 2)
	toolChan := make(chan ToolCall, 16)

	result, err := processStreamingResponseReader(
		context.Background(),
		bufio.NewReader(strings.NewReader(sse)),
		toolMap,
		map[string]string{},
		chunkChan,
		usageChan,
		toolChan,
	)
	if err != nil {
		t.Fatalf("stream processing failed: %v", err)
	}
	if len(tool.inputs) != 1 {
		t.Fatalf("expected a split-argument call to run exactly once, got %d runs: %v", len(tool.inputs), tool.inputs)
	}
	if tool.inputs[0] != `{"command":"echo hello"}` {
		t.Fatalf("expected the full assembled arguments, got %q", tool.inputs[0])
	}
	if len(result.calls) != 1 || result.calls[0].id != "call-x" {
		t.Fatalf("expected one call attributed to call-x, got %+v", result.calls)
	}
}

// TestAppendExecutedToolCallsOrdersAndAttributesResults pins the transcript the
// model reads back: one assistant tool_calls message immediately followed by its
// matching tool result, per call, in emission order, and never twice.
func TestAppendExecutedToolCallsOrdersAndAttributesResults(t *testing.T) {
	calls := []executedToolCall{
		{id: "call-a", name: "coder_read", arguments: `{"path":"a"}`, result: "ra", status: ToolCallStatusSucceeded},
		{id: "call-b", name: "coder_bash", arguments: `{"command":"ls"}`, result: "rb", status: ToolCallStatusSucceeded},
	}
	processed := map[string]bool{}

	appended, messages, truncated := appendExecutedToolCalls(nil, calls, processed, ResolveToolResultLimits(nil))
	if len(appended) != 2 {
		t.Fatalf("expected both calls to be appended, got %d", len(appended))
	}
	if truncated != 0 {
		t.Fatalf("small results must not be truncated, got %d", truncated)
	}
	if len(messages) != 4 {
		t.Fatalf("expected assistant+tool message per call (4 total), got %d", len(messages))
	}
	for i, wantID := range []string{"call-a", "call-b"} {
		assistant := messages[i*2]
		toolMsg := messages[i*2+1]
		if assistant["role"] != "assistant" {
			t.Fatalf("message %d role = %v, want assistant", i*2, assistant["role"])
		}
		if toolMsg["role"] != "tool" || toolMsg["tool_call_id"] != wantID {
			t.Fatalf("message %d = %+v, want tool result for %s", i*2+1, toolMsg, wantID)
		}
		payload, ok := assistant["tool_calls"].([]map[string]interface{})
		if !ok || len(payload) != 1 {
			t.Fatalf("assistant message %d missing its single tool call: %+v", i*2, assistant)
		}
		if payload[0]["id"] != wantID {
			t.Fatalf("assistant tool call id = %v, want %s", payload[0]["id"], wantID)
		}
	}

	// A replayed round must not feed the same call back twice.
	appendedAgain, messagesAgain, _ := appendExecutedToolCalls(messages, calls, processed, ResolveToolResultLimits(nil))
	if len(appendedAgain) != 0 || len(messagesAgain) != len(messages) {
		t.Fatalf("duplicate calls must be skipped, got appended=%d messages=%d", len(appendedAgain), len(messagesAgain))
	}
}
