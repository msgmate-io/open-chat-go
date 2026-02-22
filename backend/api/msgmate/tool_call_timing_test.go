package msgmate

import "testing"

// A completed call must report its duration even when it was zero: a cached
// (already executed) call finishes instantly, and without the explicit zero the
// widget would fall back to ticking from `started_at` and show a fabricated
// elapsed time (ci#569).
func TestToolCallTimingFieldsReportsZeroDurationForCompletedCall(t *testing.T) {
	fields := toolCallTimingFields(ToolCallStatusSucceeded, 1_700_000_000_000, 0)

	if got, ok := fields["started_at"]; !ok || got != int64(1_700_000_000_000) {
		t.Fatalf("started_at = %#v (present=%v), want the start stamp", got, ok)
	}
	duration, ok := fields["duration_ms"]
	if !ok {
		t.Fatal("a completed call must report duration_ms, even when it is zero")
	}
	if duration != int64(0) {
		t.Fatalf("duration_ms = %#v, want 0", duration)
	}
}

// An ongoing call must not report a duration: its duration field is still zero
// and would freeze the live timer at "0ms" instead of ticking.
func TestToolCallTimingFieldsOmitsDurationWhileOngoing(t *testing.T) {
	fields := toolCallTimingFields(ToolCallStatusOngoing, 1_700_000_000_000, 0)

	if _, ok := fields["duration_ms"]; ok {
		t.Fatalf("an ongoing call must not report duration_ms: %#v", fields)
	}
	if _, ok := fields["started_at"]; !ok {
		t.Fatal("an ongoing call must report started_at so the UI can tick")
	}
}

func TestToolCallTimingFieldsFailedCallKeepsDurationAndError(t *testing.T) {
	fields := toolCallTimingFields(ToolCallStatusFailed, 1_700_000_000_000, 820)
	if fields["duration_ms"] != int64(820) {
		t.Fatalf("duration_ms = %#v, want 820", fields["duration_ms"])
	}
}

// A call with no start stamp (older message, or a backend that did not report
// timing) contributes nothing, so the widget renders no timing row at all.
func TestToolCallTimingFieldsEmptyWithoutStartStamp(t *testing.T) {
	if fields := toolCallTimingFields(ToolCallStatusSucceeded, 0, 0); len(fields) != 0 {
		t.Fatalf("expected no timing fields, got %#v", fields)
	}
}

func TestIsTerminalToolCallStatus(t *testing.T) {
	terminal := []string{ToolCallStatusSucceeded, ToolCallStatusFailed, ToolCallStatusPendingConfirmation}
	for _, status := range terminal {
		if !IsTerminalToolCallStatus(status) {
			t.Fatalf("status %q must be terminal", status)
		}
	}
	if IsTerminalToolCallStatus(ToolCallStatusOngoing) {
		t.Fatal("an ongoing call must not be terminal")
	}
	if IsTerminalToolCallStatus("") {
		t.Fatal("an unknown status must not be terminal")
	}
}
