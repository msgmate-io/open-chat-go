package msgmate

import (
	"errors"
	"testing"
)

// TestCI950GenericStreamFailureClassifiesAsInternal pins the `no_agent_error`
// gate of the ci#622 native-coder self-check drive (tracker: open-chat-go-ci#950).
// A role turn fails that gate whenever the classification lands on the
// internal-error surface ("Response stopped due to an internal error"). The
// classification default must keep mapping a stream error that is not a
// provider HTTP status, not a transient network fault, not
// context-window/leaked-compaction, not a timeout, and not provider
// back-pressure straight to FailureReasonInternal, so the aggregate
// no_agent_error self-check stays faithful to internal faults and a generic
// failure is never relabelled as a provider/network reason.
func TestCI950GenericStreamFailureClassifiesAsInternal(t *testing.T) {
	cls := ClassifyGenerationFailure(errors.New("boom: unstructured internal fault"))
	if cls.Reason != FailureReasonInternal {
		t.Fatalf("ClassifyGenerationFailure(generic) Reason = %q, want %q (ci#622 no_agent_error internal surface)", cls.Reason, FailureReasonInternal)
	}
}
