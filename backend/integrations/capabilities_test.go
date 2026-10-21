package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type recordingSink struct {
	kind    string
	payload []byte
	err     error
}

func (s *recordingSink) HandleIntegrationEvent(_ context.Context, kind string, payload json.RawMessage) (any, error) {
	s.kind = kind
	s.payload = append([]byte(nil), payload...)
	return map[string]string{"ok": "true"}, s.err
}

func TestRegisterEventSinkIsOptionalAndNormalized(t *testing.T) {
	ClearEventSinks()
	t.Cleanup(ClearEventSinks)

	// Integrations that do not implement the capability are unaffected: the
	// lookup simply misses and nothing panics.
	if _, ok := EventSinkFor("msgmate-coder"); ok {
		t.Fatal("expected no event sink before registration")
	}
	if _, ok := EventSinkFor(""); ok {
		t.Fatal("expected no event sink for an empty integration name")
	}

	sink := &recordingSink{}
	RegisterEventSink("  Runtime-Selector ", sink)
	got, ok := EventSinkFor("runtime-selector")
	if !ok || got != sink {
		t.Fatalf("expected the registered sink for the normalized name, got %v (ok=%v)", got, ok)
	}

	// Registering a nil sink and a blank name are explicit no-ops.
	RegisterEventSink("runtime-selector", nil)
	RegisterEventSink("   ", sink)
	if got, ok := EventSinkFor("runtime-selector"); !ok || got != sink {
		t.Fatal("a nil sink must not remove or replace a registered sink")
	}

	ClearEventSinks()
	if _, ok := EventSinkFor("runtime-selector"); ok {
		t.Fatal("expected ClearEventSinks to drop the registration")
	}
}

func TestEventSinkReceivesRawKindPayload(t *testing.T) {
	ClearEventSinks()
	t.Cleanup(ClearEventSinks)

	sink := &recordingSink{}
	RegisterEventSink("runtime-selector", sink)

	var s EventSink = sink
	payload := json.RawMessage(`{"resource":"example","count":42}`)
	res, err := s.HandleIntegrationEvent(context.Background(), "example_event", payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sink.kind != "example_event" || string(sink.payload) != string(payload) {
		t.Fatalf("sink did not receive the raw payload: kind=%q payload=%s", sink.kind, sink.payload)
	}
	if res == nil {
		t.Fatal("expected the sink result to be passed through")
	}

	sink.err = errors.New("boom")
	if _, err := s.HandleIntegrationEvent(context.Background(), "example_event", payload); err == nil {
		t.Fatal("expected the sink error to be returned")
	}
}
