package msgmate

import "strings"

// Degenerating provider streams (ci#670).
//
// A native-coder turn can collapse into an unbounded repetition loop: the model
// re-emits the same short line or phrase (eg `</invoke>`, `Ok.`, `Write.`)
// thousands of times. Nothing upstream of the reader stops it, so a single turn
// used to persist 150–255 KB of identical junk into the chat, pollute the next
// turn's context, burn the whole completion budget and only then be cancelled
// (feeding the bogus "I paused this response" noise of ci#642/#643).
//
// detectRepetitionLoop watches a bounded tail of the assistant text for a short
// unit repeated many times in a row, so the streaming reader can cut such a
// turn off early and finish with a clear one-line note. The thresholds are
// deliberately conservative: ordinary prose (a bulleted list, a table, an ASCII
// rule, a diff) is never mistaken for a loop.

const (
	// repetitionLoopMinRepeats is how many times a unit must repeat back to back
	// before the stream counts as degenerate.
	repetitionLoopMinRepeats = 12
	// repetitionLoopMinCovered is the minimum number of characters the
	// repetition must cover, so a dozen repeats of a single character do not
	// trip the guard.
	repetitionLoopMinCovered = 300
	// repetitionLoopMaxUnit bounds the unit length; a repeated paragraph is not
	// the failure mode this guard targets.
	repetitionLoopMaxUnit = 240
	// repetitionLoopTailWindow bounds how much text is scanned on each delta, so
	// the per-delta cost stays constant no matter how long the response grows.
	repetitionLoopTailWindow = 8192
	// repetitionLoopNote is appended to the truncated turn so the persisted
	// message states plainly why it stops.
	repetitionLoopNote = "\n\n[stopped: repeated output detected]"
)

// repetitionLoop describes a detected degenerate repetition in accumulated
// assistant text.
type repetitionLoop struct {
	// unit is the repeated unit.
	unit string
	// repeats is how many times it repeats in a row at the end of the text.
	repeats int
	// covered is how many bytes the repeated run spans.
	covered int
	// start is the byte offset, into the text handed to detectRepetitionLoop,
	// at which the repeated run begins.
	start int
}

// detectRepetitionLoop reports whether text ends in a short unit repeated many
// times in a row. Only a bounded tail is examined, so the cost per streaming
// delta stays constant regardless of how long the response has grown. start is
// absolute within the text, so the caller can truncate the run off.
func detectRepetitionLoop(text string) (repetitionLoop, bool) {
	if len(text) < repetitionLoopMinCovered {
		return repetitionLoop{}, false
	}
	base := 0
	if len(text) > repetitionLoopTailWindow {
		base = len(text) - repetitionLoopTailWindow
		text = text[base:]
	}

	maxUnit := repetitionLoopMaxUnit
	if capped := len(text) / repetitionLoopMinRepeats; maxUnit > capped {
		maxUnit = capped
	}
	for unit := 1; unit <= maxUnit; unit++ {
		// Walk backwards from the end while the text stays periodic with period
		// `unit` (tail[i] == tail[i-unit]). The smallest period that holds over
		// the tail is the repeated unit.
		i := len(text) - 1
		for i-unit >= 0 && text[i] == text[i-unit] {
			i--
		}
		covered := len(text) - i - 1 + unit
		if covered < repetitionLoopMinCovered {
			continue
		}
		repeats := covered / unit
		if repeats < repetitionLoopMinRepeats {
			continue
		}
		return repetitionLoop{
			unit:    text[i+1 : i+1+unit],
			repeats: repeats,
			covered: covered,
			start:   base + i + 1 - unit,
		}, true
	}
	return repetitionLoop{}, false
}

// applyRepetitionLoopGuard inspects the accumulated assistant text and, when it
// has collapsed into a repetition loop, truncates the loop and appends a clear
// one-line note. It returns the (possibly shortened) text and whether a loop
// was cut off, so the streaming reader can stop consuming the rest of the
// degenerate stream (ci#670).
//
// The repeated run is cut off entirely (with the note in its place) so a turn
// that degenerated after real work still keeps the work it narrated, instead of
// persisting repeat #13,000.
func applyRepetitionLoopGuard(text string) (string, bool) {
	loop, ok := detectRepetitionLoop(text)
	if !ok {
		return text, false
	}
	trimmed := strings.TrimRight(text[:loop.start], " \t\r\n")
	return trimmed + repetitionLoopNote, true
}
