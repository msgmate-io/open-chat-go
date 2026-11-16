package suspend

import (
	"testing"
	"time"
)

// fakeClock lets the tests move the registry's clock without sleeping.
type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time          { return c.now }
func (c *fakeClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

func newTestRegistry(t *testing.T, ttl time.Duration) (*Registry, *fakeClock) {
	t.Helper()
	clock := &fakeClock{now: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	return NewWithTTL(ttl, clock.Now), clock
}

// A parked turn whose decision arrives must be resumed with that decision:
// this is the ci#1116 happy path where the executed tool result is delivered
// back into the same generation instead of queueing a new one.
func TestParkResumeDeliversDecision(t *testing.T) {
	reg, _ := newTestRegistry(t, time.Minute)

	decisions := reg.Park("chat-1", "call-1")
	if !reg.Parked("chat-1", "call-1") {
		t.Fatalf("expected the turn to be parked")
	}

	want := Decision{Result: "wrote 12 files", Reason: "looks good"}
	if !reg.Resume("chat-1", "call-1", want) {
		t.Fatalf("Resume reported no live suspension")
	}

	select {
	case got := <-decisions:
		if got != want {
			t.Fatalf("decision mismatch: got %+v want %+v", got, want)
		}
	default:
		t.Fatalf("no decision was delivered into the parked generation")
	}
	if reg.Parked("chat-1", "call-1") {
		t.Fatalf("a resumed suspension must not stay parked")
	}
}

// A rejection travels the same path; the record must stay distinguishable so
// the resumed generation can phrase the tool result as a rejection.
func TestParkResumeRejection(t *testing.T) {
	reg, _ := newTestRegistry(t, time.Minute)
	decisions := reg.Park("chat-1", "call-1")
	want := Decision{Result: "rejected", Rejected: true, Reason: "wrong directory"}

	if !reg.Resume("chat-1", "call-1", want) {
		t.Fatalf("Resume reported no live suspension")
	}
	got := <-decisions
	if !got.Rejected || got.Reason != "wrong directory" {
		t.Fatalf("rejection not preserved: %+v", got)
	}
}

// No suspension means the caller must fall back to the queued continuation.
func TestResumeWithoutParkFallsBack(t *testing.T) {
	reg, _ := newTestRegistry(t, time.Minute)
	if reg.Resume("chat-1", "call-1", Decision{Result: "x"}) {
		t.Fatalf("Resume must report false for an unknown key")
	}
}

// An expired suspension must not resume: the parked generation already gave up
// and the queued continuation owns the turn now.
func TestResumeAfterExpiryFallsBack(t *testing.T) {
	reg, clock := newTestRegistry(t, 30*time.Second)
	decisions := reg.Park("chat-1", "call-1")
	clock.Advance(31 * time.Second)

	if reg.Resume("chat-1", "call-1", Decision{Result: "late"}) {
		t.Fatalf("Resume must report false for an expired suspension")
	}
	select {
	case d := <-decisions:
		t.Fatalf("expired suspension must not deliver a decision, got %+v", d)
	default:
	}
	if reg.Len() != 0 {
		t.Fatalf("expired suspension must be swept, Len=%d", reg.Len())
	}
}

// A double click must not resume the generation twice; the first decision wins.
func TestResumeTwiceKeepsSuspensionResumed(t *testing.T) {
	reg, _ := newTestRegistry(t, time.Minute)
	decisions := reg.Park("chat-1", "call-1")

	if !reg.Resume("chat-1", "call-1", Decision{Result: "first"}) {
		t.Fatalf("first Resume must report true")
	}
	if !reg.Resume("chat-1", "call-1", Decision{Result: "second"}) {
		// The handler still answered an action it owns; it must not fall back
		// to queueing a second continuation for the same turn.
		t.Fatalf("duplicate Resume must not degrade to the queued fallback")
	}
	if got := <-decisions; got.Result != "first" {
		t.Fatalf("first decision must win, got %q", got.Result)
	}
	select {
	case d := <-decisions:
		t.Fatalf("second decision must not be buffered, got %+v", d)
	default:
	}
}

// Keys are scoped by chat: the same provider tool-call id in another chat must
// not be resumed by mistake.
func TestResumeIsScopedByChat(t *testing.T) {
	reg, _ := newTestRegistry(t, time.Minute)
	reg.Park("chat-1", "call-1")

	if reg.Resume("chat-2", "call-1", Decision{Result: "x"}) {
		t.Fatalf("a suspension must not be resumable from another chat")
	}
	if !reg.Parked("chat-1", "call-1") {
		t.Fatalf("the parked chat must still wait")
	}
}

// Forget is the stop/shutdown path: nothing is delivered, nothing leaks.
func TestForgetDropsSuspension(t *testing.T) {
	reg, _ := newTestRegistry(t, time.Minute)
	decisions := reg.Park("chat-1", "call-1")
	reg.Forget("chat-1", "call-1")

	if reg.Parked("chat-1", "call-1") || reg.Len() != 0 {
		t.Fatalf("Forget must drop the suspension")
	}
	if reg.Resume("chat-1", "call-1", Decision{Result: "x"}) {
		t.Fatalf("a forgotten suspension must not resume")
	}
	select {
	case d := <-decisions:
		t.Fatalf("a forgotten suspension must not deliver a decision, got %+v", d)
	default:
	}
}

// Parked drives the "waiting for confirmation" status, so it must report the
// TTL expiry without waiting for a sweep.
func TestParkedReflectsExpiry(t *testing.T) {
	reg, clock := newTestRegistry(t, 10*time.Second)
	reg.Park("chat-1", "call-1")
	if !reg.Parked("chat-1", "call-1") {
		t.Fatalf("expected parked")
	}
	clock.Advance(10 * time.Second)
	if reg.Parked("chat-1", "call-1") {
		t.Fatalf("a suspension at its deadline must not report parked")
	}
}

// WaitDecided is how the parked generation blocks: it must return the decision
// when one arrives and report expiry otherwise.
func TestWaitDecided(t *testing.T) {
	t.Run("decision", func(t *testing.T) {
		reg, _ := newTestRegistry(t, time.Minute)
		decisions := reg.Park("chat-1", "call-1")
		if !reg.Resume("chat-1", "call-1", Decision{Result: "done"}) {
			t.Fatalf("Resume reported no live suspension")
		}
		decision, ok, expired := reg.WaitDecided(decisions, time.Now().Add(time.Second))
		if !ok || expired || decision.Result != "done" {
			t.Fatalf("got (%+v, %v, %v)", decision, ok, expired)
		}
	})

	t.Run("deadline", func(t *testing.T) {
		reg, _ := newTestRegistry(t, time.Minute)
		decisions := reg.Park("chat-1", "call-1")
		_, ok, expired := reg.WaitDecided(decisions, time.Now().Add(10*time.Millisecond))
		if ok || !expired {
			t.Fatalf("deadline must report expiry: ok=%v expired=%v", ok, expired)
		}
	})
}

// The process-wide registry is what both the generation and the confirmation
// handler reach for; it must be non-nil and share one TTL.
func TestDefaultRegistry(t *testing.T) {
	reg := Default()
	if reg == nil {
		t.Fatalf("Default must not be nil")
	}
	if reg.TTL() != DefaultTTL {
		t.Fatalf("unexpected TTL: %v", reg.TTL())
	}
	if Default() != reg {
		t.Fatalf("Default must return the same registry")
	}
}
