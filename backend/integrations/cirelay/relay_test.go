package cirelay

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"backend/nativecoder"
)

type fakeSource struct {
	interactions []nativecoder.LeadInteraction
	err          error
	repos        []string
}

func (f *fakeSource) RepoInteractions(_ context.Context, repo string) ([]nativecoder.LeadInteraction, error) {
	f.repos = append(f.repos, repo)
	if f.err != nil {
		return nil, f.err
	}
	return f.interactions, nil
}

type fakeDeliverer struct {
	appended   []string
	dispatched []string
	err        error
}

func (f *fakeDeliverer) AppendToInteraction(_ context.Context, uuid, prompt string) error {
	f.appended = append(f.appended, uuid+"|"+prompt)
	return f.err
}

func (f *fakeDeliverer) DispatchAgent(_ context.Context, _ nativecoder.CIFailureEvent, leadUUID, prompt string) error {
	f.dispatched = append(f.dispatched, leadUUID+"|"+prompt)
	return f.err
}

func event(pr int, branch string) nativecoder.CIFailureEvent {
	return nativecoder.CIFailureEvent{
		SchemaVersion: nativecoder.CIFailureSchemaVersion,
		Kind:          nativecoder.CIFailureEventKind,
		Provider:      "github",
		Repo:          "https://github.com/msgmate-io/open-chat-go.git",
		PRNumber:      pr,
		Branch:        branch,
		RunURL:        "https://example.invalid/run/1",
		FailingJobs:   []string{"test"},
	}
}

func TestResolveRequiresRepo(t *testing.T) {
	src := &fakeSource{}
	relay := Relay{Interactions: src}
	if _, err := relay.Resolve(context.Background(), nativecoder.CIFailureEvent{}); err == nil {
		t.Fatal("expected validation error for missing repo")
	}
	if len(src.repos) != 0 {
		t.Fatalf("interaction store must not be queried for an invalid event: %v", src.repos)
	}
}

func TestResolveScopesCandidateInteractions(t *testing.T) {
	src := &fakeSource{interactions: []nativecoder.LeadInteraction{
		{
			UUID:           "lead-1",
			Kind:           "devlead",
			Repo:           "msgmate-io/open-chat-go",
			Status:         nativecoder.LeadInteractionActive,
			LastActivityAt: time.Now(),
		},
		{
			// Same repo but too old to matter.
			UUID:           "stale-1",
			Kind:           "devlead",
			Repo:           "msgmate-io/open-chat-go",
			Status:         nativecoder.LeadInteractionCompleted,
			LastActivityAt: time.Now().Add(-30 * 24 * time.Hour),
		},
		{
			// A different repo must never be a relay target.
			UUID:           "other-1",
			Kind:           "devlead",
			Repo:           "msgmate-io/open-chat-go-ci",
			Status:         nativecoder.LeadInteractionActive,
			LastActivityAt: time.Now(),
		},
	}}
	relay := Relay{Interactions: src}
	res, err := relay.Resolve(context.Background(), event(7, ""))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(res.CandidateInteractions) != 1 || res.CandidateInteractions[0] != "lead-1" {
		t.Fatalf("candidates must be scoped to the event repo: %v", res.CandidateInteractions)
	}
	if len(src.repos) != 1 || src.repos[0] != "msgmate-io/open-chat-go" {
		t.Fatalf("interaction store queried for the wrong repos: %v", src.repos)
	}
}

func TestResolveRequiresInteractionSource(t *testing.T) {
	relay := Relay{}
	if _, err := relay.Resolve(context.Background(), event(7, "")); err == nil {
		t.Fatal("expected error when no interaction source is configured")
	}
}

func TestResolveAppendsToActiveLead(t *testing.T) {
	src := &fakeSource{interactions: []nativecoder.LeadInteraction{{
		UUID:           "lead-1",
		Kind:           "devlead",
		Repo:           "msgmate-io/open-chat-go",
		PRNumber:       7,
		Status:         nativecoder.LeadInteractionActive,
		LastActivityAt: time.Now(),
	}}}
	relay := Relay{Interactions: src}
	res, err := relay.Resolve(context.Background(), event(7, ""))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !res.LeadFound || res.LeadInteractionID != "lead-1" || !res.LeadRunning {
		t.Fatalf("lead not resolved: %+v", res)
	}
	if res.Repo != "msgmate-io/open-chat-go" {
		t.Fatalf("repo not normalized: %q", res.Repo)
	}
	if res.FixSessionKey != "msgmate-io/open-chat-go#pr-7" {
		t.Fatalf("unexpected fix session key: %q", res.FixSessionKey)
	}
	if res.Dispatch {
		t.Fatalf("active matching lead must be appended to, not dispatched: %+v", res)
	}
	if res.Route != string(nativecoder.RouteAppendOngoing) {
		t.Fatalf("route = %q, want %q", res.Route, nativecoder.RouteAppendOngoing)
	}
	if res.TargetInteractionID != "lead-1" || res.TargetInteractionKind != "devlead" {
		t.Fatalf("target interaction not surfaced: %+v", res)
	}
	if res.Attempt != 1 || res.MaxAttempts != nativecoder.DefaultFixCycleMaxAttempts {
		t.Fatalf("attempt bookkeeping = %d/%d", res.Attempt, res.MaxAttempts)
	}
	if !strings.Contains(res.Prompt, nativecoder.NeverPollCINote) {
		t.Fatalf("rendered prompt must carry the never-poll-CI note")
	}
}

func TestHandleDispatchesFreshAgent(t *testing.T) {
	src := &fakeSource{}
	del := &fakeDeliverer{}
	relay := Relay{Router: nativecoder.DevLeadRouter{}, Interactions: src, Deliverer: del}
	res, err := relay.Handle(context.Background(), event(11, "feature/x"))
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if !res.Dispatch {
		t.Fatalf("no lead exists so a fresh agent must be dispatched: %+v", res)
	}
	if res.Route != string(nativecoder.RouteSpawnNew) {
		t.Fatalf("route = %q, want %q", res.Route, nativecoder.RouteSpawnNew)
	}
	if res.TargetInteractionID != "" || res.TargetInteractionKind != "" {
		t.Fatalf("a fresh dispatch has no target interaction: %+v", res)
	}
	if len(del.dispatched) != 1 {
		t.Fatalf("expected one dispatch, got %v", del.dispatched)
	}
	if len(del.appended) != 0 {
		t.Fatalf("nothing to append to: %v", del.appended)
	}
}

func TestHandleAppendsWithoutDeliverer(t *testing.T) {
	src := &fakeSource{interactions: []nativecoder.LeadInteraction{{
		UUID:           "lead-2",
		Kind:           "devlead",
		Repo:           "msgmate-io/open-chat-go",
		PRNumber:       7,
		Status:         nativecoder.LeadInteractionActive,
		LastActivityAt: time.Now(),
	}}}
	relay := Relay{Interactions: src}
	res, err := relay.Handle(context.Background(), event(7, ""))
	if err != nil {
		t.Fatalf("handle without deliverer must still resolve: %v", err)
	}
	if !res.LeadFound || !res.LeadRunning || res.Dispatch {
		t.Fatalf("resolution changed by a missing deliverer: %+v", res)
	}
}

func TestHandlePropagatesDelivererError(t *testing.T) {
	src := &fakeSource{}
	del := &fakeDeliverer{err: errors.New("boom")}
	relay := Relay{Interactions: src, Deliverer: del}
	if _, err := relay.Handle(context.Background(), event(11, "")); err == nil {
		t.Fatal("expected deliverer error to propagate")
	}
}

func TestHandleReactivatesPastInteraction(t *testing.T) {
	past := nativecoder.LeadInteraction{
		UUID:           "past-1",
		Kind:           "devlead",
		Repo:           "msgmate-io/open-chat-go",
		PRNumber:       9,
		Status:         nativecoder.LeadInteractionCompleted,
		LastActivityAt: time.Now().Add(-time.Minute),
	}
	src := &fakeSource{interactions: []nativecoder.LeadInteraction{past}}
	del := &fakeDeliverer{}
	relay := Relay{Interactions: src, Deliverer: del}
	res, err := relay.Handle(context.Background(), event(9, ""))
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if res.Route != string(nativecoder.RouteReactivatePast) {
		t.Fatalf("route = %q, want %q", res.Route, nativecoder.RouteReactivatePast)
	}
	if !res.Dispatch || res.TargetInteractionID != "past-1" {
		t.Fatalf("reactivation must dispatch against the past interaction: %+v", res)
	}
	if len(del.dispatched) != 1 || !strings.HasPrefix(del.dispatched[0], "past-1|") {
		t.Fatalf("expected dispatch against past-1, got %v", del.dispatched)
	}
	if len(del.appended) != 0 {
		t.Fatalf("a reactivation never appends: %v", del.appended)
	}
}

func TestHandleDedupesWithoutDelivering(t *testing.T) {
	fixSessionKey := "msgmate-io/open-chat-go#pr-7"
	owner := nativecoder.LeadInteraction{
		UUID:           "lead-1",
		Kind:           "devlead",
		Repo:           "msgmate-io/open-chat-go",
		PRNumber:       7,
		Status:         nativecoder.LeadInteractionActive,
		LastActivityAt: time.Now(),
		FixAttempts:    map[string]int{fixSessionKey: 1},
	}
	src := &fakeSource{interactions: []nativecoder.LeadInteraction{owner}}
	del := &fakeDeliverer{}
	relay := Relay{Interactions: src, Deliverer: del}
	replayed := event(7, "")
	replayed.Attempt = 1
	res, err := relay.Handle(context.Background(), replayed)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if res.Route != string(nativecoder.RouteDedupeExisting) {
		t.Fatalf("route = %q, want dedupe", res.Route)
	}
	if res.Dispatch || len(del.dispatched) != 0 || len(del.appended) != 0 {
		t.Fatalf("dedupe must deliver nothing: res=%+v delivered=%v/%v", res, del.dispatched, del.appended)
	}
	if res.TargetInteractionID != "lead-1" {
		t.Fatalf("dedupe should still name the owner: %+v", res)
	}
}

func TestHandleEscalatesWithoutDelivering(t *testing.T) {
	fixSessionKey := "msgmate-io/open-chat-go#pr-5"
	owner := nativecoder.LeadInteraction{
		UUID:           "lead-3",
		Kind:           "devlead",
		Repo:           "msgmate-io/open-chat-go",
		PRNumber:       5,
		Status:         nativecoder.LeadInteractionActive,
		LastActivityAt: time.Now(),
		FixAttempts:    map[string]int{fixSessionKey: 99},
	}
	src := &fakeSource{interactions: []nativecoder.LeadInteraction{owner}}
	del := &fakeDeliverer{}
	relay := Relay{Router: nativecoder.DevLeadRouter{MaxAttempts: 3}, Interactions: src, Deliverer: del}
	res, err := relay.Handle(context.Background(), event(5, ""))
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if res.Route != string(nativecoder.RouteEscalateMaintainer) {
		t.Fatalf("route = %q, want escalation", res.Route)
	}
	if len(del.dispatched) != 0 || len(del.appended) != 0 {
		t.Fatalf("escalation must deliver nothing: %v/%v", del.dispatched, del.appended)
	}
}

func TestParseEventRejectsFutureSchema(t *testing.T) {
	raw := []byte(`{"kind":"ci_failure","schema_version":99,"repo":"msgmate-io/open-chat-go"}`)
	if _, err := ParseEvent(raw); err == nil {
		t.Fatal("expected unsupported schema version to be rejected")
	}
}

func TestRelayHandlerAuthAndSuccess(t *testing.T) {
	src := &fakeSource{}
	relay := Relay{Interactions: src}
	handler := RelayHandler(relay, "secret-token")
	body := `{"kind":"ci_failure","schema_version":1,"repo":"msgmate-io/open-chat-go","branch":"main","sha":"abc123"}`

	req := httptest.NewRequest(http.MethodPost, RelayPath, strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without a token, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, RelayPath, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret-token")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var res Resolution
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if res.Repo != "msgmate-io/open-chat-go" {
		t.Fatalf("unexpected repo in response: %q", res.Repo)
	}
}

func TestRelayHandlerRejectsGetAndBadPayload(t *testing.T) {
	handler := RelayHandler(Relay{Interactions: &fakeSource{}}, "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, RelayPath, nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, RelayPath, strings.NewReader("not-json")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a bad payload, got %d", rec.Code)
	}
}
