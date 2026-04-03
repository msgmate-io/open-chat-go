package msgmate

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// ci#630 finding 7: message `tool_calls` payloads reached ~1.8 MB because the
// raw result of a tool call (whole-file dumps, full command output) was
// persisted verbatim on the call row and replayed verbatim into the next
// request's context.
//
// The per-model context budget (ci#680) truncates what the model *sees* inside
// a single turn, but nothing capped what got *stored* (DB/UI bloat) nor what a
// stored result added back to the context when the turn was rebuilt from
// history on the next request.
//
// PersistedToolResultMaxChars is the ceiling applied to a tool result before it
// is written into the message `tool_calls`/details payload and before a stored
// result is replayed from storage into the model context. It is deliberately
// larger than the live per-tool context cap: storage keeps enough for the UI and
// a follow-up turn to stay useful, while removing the pathological
// multi-megabyte rows.
const PersistedToolResultMaxChars = 16000

// PersistedToolResultMaxCharsEnvKey lets deployments tune the persisted/replayed
// tool-result ceiling. A value of 0 (or a negative one) disables the cap, which
// restores the old verbatim behaviour for operators who need full payloads.
const PersistedToolResultMaxCharsEnvKey = "OCI_MSGMATE_CODER_PERSISTED_TOOL_RESULT_MAX_CHARS"

// persistedToolResultLimit resolves the effective character ceiling. An
// unparsable value falls back to the compiled-in default rather than silently
// disabling the guard.
func persistedToolResultLimit() int {
	raw := strings.TrimSpace(os.Getenv(PersistedToolResultMaxCharsEnvKey))
	if raw == "" {
		return PersistedToolResultMaxChars
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		return PersistedToolResultMaxChars
	}
	return parsed
}

// persistedTruncationMarker is inserted between the kept head and the kept tail
// so both the UI and the model can tell the payload was elided and by how much.
const persistedTruncationMarker = "\n\n[... tool result truncated by msgmate: %d of %d chars elided ...]\n\n"

// persistedToolResultElisionNotice is the marker plus a hint that names the
// remedy, mirroring the live context-budget marker so the model is not left
// guessing why output ends mid-stream.
const persistedToolResultElisionNotice = "Re-issue the tool call with a narrower scope (targeted read, path/include filter, or redirect output to a file) if you need the missing part."

// truncatePersistedToolResult caps a tool result before it is persisted or
// replayed into context. Small results pass through untouched. A large result
// keeps a head and a tail (the tail often carries the actionable error/summary
// line) with an explicit elision marker in between.
//
// Both the budget and the cut are measured in runes so the returned payload is
// always valid UTF-8: slicing bytes would split a multi-byte rune and corrupt
// the stored JSON for the UI.
func truncatePersistedToolResult(result string) string {
	limit := persistedToolResultLimit()
	if limit <= 0 {
		return result
	}

	runes := []rune(result)
	if len(runes) <= limit {
		return result
	}

	// Keep a bit more than half in the head: the beginning of a tool result
	// (a file's imports, a command's first lines) is usually the most useful.
	headBudget := limit * 3 / 5
	if headBudget < 1 {
		headBudget = 1
	}
	tailBudget := limit - headBudget
	if tailBudget < 0 {
		tailBudget = 0
	}

	elided := len(runes) - headBudget - tailBudget
	marker := fmt.Sprintf(persistedTruncationMarker, elided, len(runes))
	marker += persistedToolResultElisionNotice

	head := string(runes[:headBudget])
	tail := ""
	if tailBudget > 0 {
		tail = string(runes[len(runes)-tailBudget:])
	}
	return head + marker + tail
}
