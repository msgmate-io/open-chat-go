package msgmate

import (
	"errors"
	"regexp"
	"strings"
)

// Leaked context-compaction transcripts (ci#562).
//
// Provider-side coding models volunteer their own "conversation compaction"
// transcript when they lose track of the task: an <analysis>...</analysis>
// block followed by a numbered <summary>...</summary> block ("Primary Request
// and Intent", "...", "Optional Next Step"). The private `tags=coder` sandbox
// benchmark `msgmate_coder_repo_changes_summary` caught exactly that emission
// as the final reply of a turn: the summary file was never written and the
// bot's answer was the compaction blob, which the harness correctly reported as
// "interaction failed or produced no text: state=finished error=false" with the
// `<analysis> ... <summary> ...` text attached.
//
// The blob is model bookkeeping, never a user-facing answer, so it must not be
// stored as a turn's assistant text (a chat consumer would show it as the reply
// — and a caller like the benchmark, which posts the reply verbatim, would post
// it). It is detected here and stripped; when it was the turn's *only* output
// the round is flagged so the tool loop retries it once, the same retry an
// empty post-confirmation continuation already gets (ci#428).

// compactionBlockRe matches one tagged compaction block (<analysis ...> or
// <summary ...>). An unterminated trailing block is matched to the end of the
// text: a truncated emission is blob material too. `(?s)` makes `.` span lines
// while `$` stays end-of-text, so a block is never cut at a mere line end.
var compactionBlockRe = regexp.MustCompile(`(?is)<(analysis|summary)\b[^>]*>.*?(?:</(?:analysis|summary)>|$)`)

// compactionFenceRe matches a markdown code-fence line, which models sometimes
// wrap the blob in.
var compactionFenceRe = regexp.MustCompile("(?m)^[ \t]*```.*$")

// compactionOutputSuspected reports whether the accumulated assistant content
// has opened a compaction block (or is still a strict prefix of one). Content
// that opens with `<analysis`/`<summary` is never user-facing, so the streaming
// loop withholds it instead of forwarding the transcript to the client. A
// legitimate answer only trips this while its opening characters are still
// ambiguous; diverging content resumes streaming from the next delta (the
// accumulated text, and therefore the persisted message, stays complete).
func compactionOutputSuspected(accumulated string) bool {
	trimmed := strings.TrimLeft(accumulated, " \t\r\n`")
	if trimmed == "" {
		return false
	}
	if trimmed[0] != '<' {
		return false
	}
	lower := strings.ToLower(trimmed)
	for _, tag := range []string{"<analysis", "<summary"} {
		// Either the tag is already present, or the content so far is still a
		// prefix of it (eg "<anal") and may yet become one.
		if strings.HasPrefix(lower, tag) || strings.HasPrefix(tag, lower) {
			return true
		}
	}
	return false
}

// errLeakedCompactionTranscript marks a turn whose assistant output consisted
// only of a leaked context-compaction transcript and whose single retry
// produced another one: the turn has no usable answer and retrying again is not
// going to help, so it is reported as a generation failure with its own reason
// (the work already done is kept; ci#562).
var errLeakedCompactionTranscript = errors.New(
	"model produced only a context-compaction transcript and no usable answer",
)

// sanitizeCompactionOutput removes a leaked compaction transcript from an
// assistant message. It returns the message without the transcript and whether
// the message consisted *solely* of compaction material — in which case the
// caller treats the round as having produced no answer at all and retries it.
//
// Only a message that *opens* with a compaction tag (optionally after
// whitespace or a fence) is treated as a leak: that is the shape a model emits
// when it volunteers its own bookkeeping instead of an answer. A legitimate
// answer that merely mentions such a tag inside real prose is returned
// unchanged — stripping part of a real reply would be worse than leaving it.
func sanitizeCompactionOutput(content string) (string, bool) {
	if strings.TrimSpace(content) == "" {
		return content, false
	}
	trimmed := strings.TrimLeft(content, " \t\r\n")
	// Skip a leading fence line, which models sometimes wrap the blob in.
	for strings.HasPrefix(trimmed, "```") {
		newline := strings.Index(trimmed, "\n")
		if newline < 0 {
			return content, false
		}
		trimmed = strings.TrimLeft(trimmed[newline+1:], " \t\r\n")
	}
	lower := strings.ToLower(trimmed)
	if !strings.HasPrefix(lower, "<analysis") && !strings.HasPrefix(lower, "<summary") {
		return content, false
	}
	stripped := compactionFenceRe.ReplaceAllString(compactionBlockRe.ReplaceAllString(trimmed, ""), "")
	if strings.TrimSpace(stripped) == "" {
		return "", true
	}
	// The blob was followed by real text: keep that text and drop the blob.
	return strings.TrimSpace(stripped), false
}
