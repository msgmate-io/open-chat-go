package msgmate

import (
	"strings"
	"unicode"
)

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
//
// detectRepeatedLineRatio covers the sibling failure mode the byte-periodic
// check cannot see: a stream that loops one *line shape* while mutating a small
// embedded detail ("Writing chunk 812 of the output file…"). That class also
// persisted hundreds of KB before cancellation, so it is caught by a
// rolling-window line repetition ratio over the tail.

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

	// repetitionRatioWindow is how many trailing lines the ratio guard examines
	// per delta, which bounds the per-delta cost.
	repetitionRatioWindow = 64
	// repetitionRatioMinRepeats is how many of those lines must repeat an
	// earlier line's shape before the tail counts as degenerate.
	repetitionRatioMinRepeats = 12
	// repetitionRatioMinCovered is the minimum number of bytes the repeated
	// lines must span, so a short numbered list of similar-looking steps is not
	// mistaken for a loop.
	repetitionRatioMinCovered = 480
	// repetitionRatioMinLineLen is the minimum length of a line's normalized
	// shape for that line to count at all, so short labels and separators never
	// contribute to the ratio.
	repetitionRatioMinLineLen = 12
	// repetitionRatioMinLetters is the minimum number of letters a normalized
	// line shape must contain, and repetitionRatioMinLetterShare the minimum
	// share of its characters those letters must be. Both keep pure decoration
	// (ASCII rules, `----`, `====`, banner art) out of the ratio.
	repetitionRatioMinLetters     = 6
	repetitionRatioMinLetterShare = 0.5
	// repetitionRatioThreshold is the share of the window's bytes that must
	// belong to repeated lines. Conservative on purpose: prose, bulleted or
	// numbered lists, tables, diffs and ASCII rules all stay well below it.
	repetitionRatioThreshold = 0.75
	// repetitionGuardMaxKeptBytes bounds the real work kept ahead of a cut-off
	// loop, so the persisted message stays bounded even when the turn narrated
	// a lot before degenerating (ci#670).
	repetitionGuardMaxKeptBytes = 8192
	// repetitionLineRunawayBytes is how far a *structural* line repeat — a
	// markdown table row, an ASCII rule, a banner — may grow before it counts as
	// a runaway instead of formatting. Short junk token loops (`Ok.`,
	// `</invoke>`) are not structural and trip the guard as soon as they form.
	repetitionLineRunawayBytes = 4096
	// repetitionStructuralMinUnitLen is the shortest repeated line that may be
	// exempted as formatting; anything shorter that repeats back to back is the
	// degenerate-token failure mode of ci#670.
	repetitionStructuralMinUnitLen = 16
	// repetitionLoopWalkLimit bounds how far back the ratio guard walks to find
	// the true start of a degenerate run, so a truncated message cannot cost
	// more than a bounded scan.
	repetitionLoopWalkLimit = 1 << 20
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

// detectDegenerateTail reports a degenerate repetition at the end of the
// accumulated assistant text. The exact byte-periodic loop is tried first (it is
// unambiguous and cheap); the rolling-window line repetition ratio then covers
// the near-duplicate class the exact check cannot see.
//
// A repeated *structural* line — a markdown table row, a horizontal rule, banner
// art — is tolerated until it is clearly a runaway (repetitionLineRunawayBytes):
// emitting a dozen identical rows is how a markdown table looks, while a table (or
// rule) that keeps growing with the same line is the ci#670 failure mode. Short
// repeated tokens (`Ok.`, `</invoke>`) are not structural and trip the guard as
// soon as they form.
func detectDegenerateTail(text string) (repetitionLoop, bool) {
	if loop, ok := detectRepetitionLoop(text); ok {
		if !isStructuralLineUnit(loop.unit) || loop.covered >= repetitionLineRunawayBytes {
			return loop, true
		}
	}
	return detectRepeatedLineRatio(text)
}

// isStructuralLineUnit reports whether a repeated unit is whole-line formatting
// that repeats by design: a markdown table row or decoration with no prose in it
// (an ASCII rule, a run of `=`, banner art). Such a repeat only counts as
// degenerate once detectDegenerateTail's runaway bound is exceeded.
func isStructuralLineUnit(unit string) bool {
	line := strings.TrimSpace(unit)
	if len(line) < repetitionStructuralMinUnitLen {
		return false
	}
	if strings.HasPrefix(line, "|") {
		return true
	}
	return !lineShapeIsSubstantive(normalizeLineForRatio(line))
}

// repetitionTailLines returns up to max non-blank trailing lines of text in
// document order, together with the byte offset of each line inside text.
func repetitionTailLines(text string, max int) ([]string, []int) {
	if max <= 0 {
		return nil, nil
	}
	lines := make([]string, 0, max)
	offsets := make([]int, 0, max)
	end := len(text)
	for end > 0 && len(lines) < max {
		lineStart := strings.LastIndexByte(text[:end], '\n') + 1
		line := strings.TrimRight(text[lineStart:end], "\r")
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
			offsets = append(offsets, lineStart)
		}
		if lineStart == 0 {
			break
		}
		end = lineStart - 1
	}
	// Collected backwards (last line first): restore document order.
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
		offsets[i], offsets[j] = offsets[j], offsets[i]
	}
	return lines, offsets
}

// normalizeLineForRatio reduces a line to its shape: whitespace is collapsed and
// every run of digits becomes '#'. A loop that only mutates an embedded counter
// ("Writing chunk 812 of the output file") therefore normalizes to one repeated
// shape instead of staying invisible to a byte-equality check.
func normalizeLineForRatio(line string) string {
	var b strings.Builder
	b.Grow(len(line))
	prevDigit := false
	prevSpace := false
	for _, r := range line {
		switch {
		case r >= '0' && r <= '9':
			if !prevDigit {
				b.WriteByte('#')
			}
			prevDigit, prevSpace = true, false
		case r == ' ' || r == '\t' || r == '\r':
			prevSpace = true
			prevDigit = false
		default:
			if prevSpace && b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteRune(unicode.ToLower(r))
			prevDigit, prevSpace = false, false
		}
	}
	return strings.TrimSpace(b.String())
}

// lineShapeIsSubstantive reports whether a normalized line shape carries enough
// letters to be meaningful prose/code, as opposed to decoration such as an ASCII
// rule (`+---+---+`), a Run of `=====`, or banner art. Decoration lines repeat
// perfectly by design and must never feed the repetition ratio.
func lineShapeIsSubstantive(shape string) bool {
	if len(shape) < repetitionRatioMinLineLen {
		return false
	}
	letters := 0
	for _, r := range shape {
		if unicode.IsLetter(r) {
			letters++
		}
	}
	if letters < repetitionRatioMinLetters {
		return false
	}
	return float64(letters)/float64(len(shape)) >= repetitionRatioMinLetterShare
}

// detectRepeatedLineRatio is the rolling-window line repetition ratio guard: it
// examines a bounded window of trailing lines and reports a loop when most of
// the window's bytes are lines whose normalized shape already appeared earlier
// in the same window (ci#670).
func detectRepeatedLineRatio(text string) (repetitionLoop, bool) {
	if len(text) < repetitionRatioMinCovered {
		return repetitionLoop{}, false
	}
	lines, offsets := repetitionTailLines(text, repetitionRatioWindow)

	seen := make(map[string]struct{}, len(lines))
	repeated := make(map[string]struct{}, len(lines))
	total := 0
	duplicated := 0
	duplicateRepeats := 0
	runStart := -1
	for i, line := range lines {
		normalized := normalizeLineForRatio(line)
		if !lineShapeIsSubstantive(normalized) {
			continue
		}
		// A markdown table row is a legitimately repeated shape: skip it.
		if strings.HasPrefix(strings.TrimSpace(line), "|") {
			continue
		}
		total += len(line)
		if _, ok := seen[normalized]; ok {
			duplicated += len(line)
			duplicateRepeats++
			repeated[normalized] = struct{}{}
			if runStart < 0 {
				runStart = i
			}
			continue
		}
		seen[normalized] = struct{}{}
	}

	if duplicateRepeats < repetitionRatioMinRepeats || total < repetitionRatioMinCovered {
		return repetitionLoop{}, false
	}
	if float64(duplicated)/float64(total) < repetitionRatioThreshold {
		return repetitionLoop{}, false
	}
	return repetitionLoop{
		unit:    lines[len(lines)-1],
		repeats: duplicateRepeats,
		covered: duplicated,
		// The window only shows that the tail is degenerate; walk the loop back
		// to where it actually starts so truncation cuts the whole junk run
		// instead of only the last repetitionRatioWindow lines of it.
		start: loopRunStart(text, repeated, runStart, offsets),
	}, true
}

// loopRunStart extends a detected degenerate tail backwards to the true start of
// the loop: it walks lines backwards from the end of text while each line's
// normalized shape belongs to the loop's repeated shapes, and returns the byte
// offset of that line (falling back to the windowed start when it cannot walk).
// Blank lines inside the loop are skipped rather than ending it.
func loopRunStart(text string, repeated map[string]struct{}, runStart int, offsets []int) int {
	fallback := offsets[runStart]
	end := len(text)
	start := fallback
	walked := 0
	for end > 0 && walked < repetitionLoopWalkLimit {
		lineStart := strings.LastIndexByte(text[:end], '\n') + 1
		line := strings.TrimRight(text[lineStart:end], "\r")
		if strings.TrimSpace(line) != "" {
			if _, ok := repeated[normalizeLineForRatio(line)]; !ok {
				break
			}
			start = lineStart
		}
		walked += end - lineStart
		if lineStart == 0 {
			break
		}
		end = lineStart - 1
	}
	if walked > 0 && start < fallback {
		return start
	}
	return fallback
}

// applyRepetitionLoopGuard inspects the accumulated assistant text and, when it
// has collapsed into a repetition loop, truncates the loop and appends a clear
// one-line note. It returns the (possibly shortened) text and whether a loop
// was cut off, so the streaming reader can stop consuming the rest of the
// degenerate stream (ci#670).
//
// The repeated run is cut off entirely (with the note in its place) so a turn
// that degenerated after real work still keeps the work it narrated, instead of
// persisting repeat #13,000. The kept prefix is itself bounded, so even a turn
// that narrated a lot before degenerating cannot persist an unbounded blob.
func applyRepetitionLoopGuard(text string) (string, bool) {
	loop, ok := detectDegenerateTail(text)
	if !ok {
		return text, false
	}
	kept := strings.TrimRight(text[:loop.start], " \t\r\n")
	if len(kept) > repetitionGuardMaxKeptBytes {
		kept = "…" + kept[len(kept)-repetitionGuardMaxKeptBytes:]
	}
	return kept + repetitionLoopNote, true
}
