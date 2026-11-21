package msgmate

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestCI1160WrapTerminalKeepsTypedError pins the ci#1160 fix: a classified
// terminal error must survive the "response already sent" marker as a typed,
// inspectable error. The historic %v wrap flattened it into a string, so the
// queue task re-classifying the turn saw an opaque error and rendered the
// generic "internal error" instead of the real reason.
func TestCI1160WrapTerminalKeepsTypedError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		as   func(error) bool // typed / sentinel match that must survive
		// want is a fragment of the real reason that must reach the text.
		want string
	}{
		{
			name: "provider saturation",
			err: &ProviderSaturationError{
				Key:            "coder_sandbox",
				MaxConcurrency: 2,
				InFlight:       2,
				QueueWaiting:   1,
			},
			as: func(err error) bool {
				var sat *ProviderSaturationError
				return errors.As(err, &sat)
			},
			want: "saturated",
		},
		{
			name: "leaked compaction transcript",
			err:  errLeakedCompactionTranscript,
			as: func(err error) bool {
				return errors.Is(err, errLeakedCompactionTranscript)
			},
			want: "compaction",
		},
	}

	for _, tc := range cases {
		wrapped := wrapTerminalResponseAlreadySent(tc.err)
		if !errors.Is(wrapped, ErrResponseAlreadySent) {
			t.Fatalf("%s: wrap lost ErrResponseAlreadySent: %v", tc.name, wrapped)
		}
		if !tc.as(wrapped) {
			t.Fatalf("%s: wrap lost the typed error (%v); %%v wrapping flattens it", tc.name, wrapped)
		}
		if !errors.Is(wrapped, tc.err) {
			t.Fatalf("%s: wrap lost errors.Is identity: %v", tc.name, wrapped)
		}
		if !strings.Contains(wrapped.Error(), tc.want) {
			t.Fatalf("%s: wrapped text = %q, want it to carry %q", tc.name, wrapped.Error(), tc.want)
		}
		if strings.Contains(strings.ToLower(wrapped.Error()), "internal error") {
			t.Fatalf("%s: wrapped text = %q, want the real reason instead of internal-error wording", tc.name, wrapped.Error())
		}
	}

	// Cancelled turns are classified too (ci#1160): the sentinel must stay
	// reachable so the turn reports the cancellation, not an internal fault.
	cancelled := wrapTerminalResponseAlreadySent(context.Canceled)
	if !errors.Is(cancelled, context.Canceled) {
		t.Fatalf("wrap lost context.Canceled: %v", cancelled)
	}
}
