package msgmate

import (
	"bufio"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// recordingConfirmableTool is a fake confirmable tool that records how many
// times it ran inline. Confirmable tools are never executed while the stream is
// parsed (execution is deferred to the confirmable-action approval), so the
// counter must stay zero during processStreamingResponseReader.
type recordingConfirmableTool struct {
	*BaseTool
	runCount int
}

func (t *recordingConfirmableTool) RunTool(input interface{}) (string, error) {
	t.runCount++
	return "confirmable-ran", nil
}

// recordingNonConfirmableTool records how often a regular (non-confirmable)
// tool executes inline.
type recordingNonConfirmableTool struct {
	*BaseTool
	runCount  int
	lastInput string
}

func (t *recordingNonConfirmableTool) RunTool(input interface{}) (string, error) {
	t.runCount++
	if encoded, err := json.Marshal(input); err == nil {
		t.lastInput = string(encoded)
	}
	return "non-confirmable-ran", nil
}

func sseFromToolCallChunks(chunks []map[string]interface{}) string {
	var builder strings.Builder
	for _, chunk := range chunks {
		encoded, err := json.Marshal(chunk)
		if err != nil {
			panic(err)
		}
		builder.WriteString("data: ")
		builder.Write(encoded)
		builder.WriteString("\n\n")
	}
	builder.WriteString(`data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14}}`)
	builder.WriteString("\n\n")
	builder.WriteString("data: [DONE]\n\n")
	return builder.String()
}

func toolCallChunk(id, name, arguments string) map[string]interface{} {
	return map[string]interface{}{
		"choices": []map[string]interface{}{{
			"index": 0,
			"delta": map[string]interface{}{
				"tool_calls": []map[string]interface{}{{
					"id":   id,
					"type": "function",
					"function": map[string]interface{}{
						"name":      name,
						"arguments": arguments,
					},
				}},
			},
		}},
	}
}

// TestProcessStreamingResponseReaderStopsAfterFirstConfirmableBatchedToolCall is
// the regression test for ci#405: when a single assistant turn streams a batch
// of confirmable calls, only the first one may be surfaced. Before the fix the
// parser kept reading the response and turned every batched call into its own
// confirmable action, so approving the batch re-executed already-applied
// mutations.
func TestProcessStreamingResponseReaderStopsAfterFirstConfirmableBatchedToolCall(t *testing.T) {
	tool := &recordingConfirmableTool{BaseTool: &BaseTool{
		ToolName:                       "coder_edit",
		RequiresConfirmation:           true,
		StopOnFirstConfirmableToolCall: true,
	}}
	toolMap := map[string]Tool{"coder_edit": tool}

	editArgs := `{"path":"f3_batch_probe.txt","old_string":"BBB","new_string":"BBB-EDITED"}`
	sse := sseFromToolCallChunks([]map[string]interface{}{
		toolCallChunk("call-1", "coder_edit", editArgs),
		toolCallChunk("call-2", "coder_edit", editArgs),
	})

	chunkChan := make(chan string, 4)
	usageChan := make(chan *TokenUsage, 2)
	toolChan := make(chan ToolCall, 8)

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
	if result == nil {
		t.Fatalf("expected non-nil result")
	}
	if !result.usedTool {
		t.Fatalf("expected tool usage")
	}
	if !result.stopAfterTool {
		t.Fatalf("expected stopAfterTool for the confirmable batched call")
	}
	if result.toolName != "coder_edit" {
		t.Fatalf("unexpected tool name: %s", result.toolName)
	}
	if result.status != ToolCallStatusPendingConfirmation {
		t.Fatalf("expected %s status, got %s", ToolCallStatusPendingConfirmation, result.status)
	}

	close(toolChan)
	confirmableActions := 0
	seenIDs := map[string]bool{}
	for tc := range toolChan {
		seenIDs[tc.Id] = true
		if tc.Status == ToolCallStatusPendingConfirmation {
			confirmableActions++
		}
	}
	if confirmableActions != 1 {
		t.Fatalf("expected exactly one confirmable action, got %d", confirmableActions)
	}
	if len(seenIDs) != 1 || !seenIDs["call-1"] {
		t.Fatalf("expected only the first batched tool call to be surfaced, got %v", seenIDs)
	}
	if tool.runCount != 0 {
		t.Fatalf("confirmable tool must not run while parsing, got %d executions", tool.runCount)
	}
}

// TestProcessStreamingResponseReaderKeepsNonConfirmableBatchBehavior guards that
// the early stop only applies to tools declaring StopOnFirstConfirmableToolCall:
// a batch of regular tool calls still all executes.
func TestProcessStreamingResponseReaderKeepsNonConfirmableBatchBehavior(t *testing.T) {
	tool := &recordingNonConfirmableTool{BaseTool: &BaseTool{ToolName: "coder_read"}}
	toolMap := map[string]Tool{"coder_read": tool}

	sse := sseFromToolCallChunks([]map[string]interface{}{
		toolCallChunk("call-a", "coder_read", `{"path":"a.txt"}`),
		toolCallChunk("call-b", "coder_read", `{"path":"b.txt"}`),
	})

	chunkChan := make(chan string, 4)
	usageChan := make(chan *TokenUsage, 2)
	toolChan := make(chan ToolCall, 8)

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
	if result == nil {
		t.Fatalf("expected non-nil result")
	}
	if result.stopAfterTool {
		t.Fatalf("non-confirmable tools must not trigger stopAfterTool")
	}
	if tool.runCount != 2 {
		t.Fatalf("expected both non-confirmable batched calls to execute, got %d", tool.runCount)
	}
}
