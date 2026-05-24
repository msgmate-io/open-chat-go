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

func envelope(t *testing.T, kind string, payload interface{}) []byte {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	env, err := json.Marshal(Event{SchemaVersion: EventSchemaVersion, Kind: kind, Payload: raw})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return env
}

func TestParseEnvelopeValidatesVersionKindAndPayload(t *testing.T) {
	if _, err := ParseEnvelope([]byte(`{"kind":"ci_failure"}`)); err == nil {
		t.Fatal("expected a missing payload to be rejected")
	}
	if _, err := ParseEnvelope([]byte(`{"schema_version":9,"kind":"ci_failure","payload":{}}`)); err == nil {
		t.Fatal("expected a future schema version to be rejected")
	}
	if _, err := ParseEnvelope([]byte(`{"payload":{}}`)); err == nil {
		t.Fatal("expected a missing kind to be rejected")
	}
	parsed, err := ParseEnvelope([]byte(`{"kind":" CI_Failure ","payload":{"repo":"msgmate-io/open-chat-go"}}`))
	if err != nil {
		t.Fatalf("parse envelope: %v", err)
	}
	if parsed.CanonicalKind() != nativecoder.CIFailureEventKind || !parsed.IsCIFailure() {
		t.Fatalf("unexpected canonical kind %q (ci failure: %v)", parsed.CanonicalKind(), parsed.IsCIFailure())
	}
}

func TestEnvelopeIsCIFailureAcceptsAliasOnly(t *testing.T) {
	if !(Event{Kind: "ci_error"}).IsCIFailure() {
		t.Fatal("expected the ci_error alias to be treated as a CI failure")
	}
	if (Event{Kind: "ci_success"}).IsCIFailure() {
		t.Fatal("unexpected CI failure for a non-CI-failure kind")
	}
}

func TestIngestRejectsUnsupportedKind(t *testing.T) {
	src := &fakeSource{}
	relay := Relay{Interactions: src}
	_, err := relay.Ingest(context.Background(), Event{Kind: "ci_success", Payload: json.RawMessage(`{}`)})
	if !errors.Is(err, ErrUnsupportedEventKind) {
		t.Fatalf("expected ErrUnsupportedEventKind, got %v", err)
	}
	if len(src.repos) != 0 {
		t.Fatalf("an unsupported kind must not reach the interaction store: %v", src.repos)
	}
}

func TestIngestRoutesCIFailureToRepoLead(t *testing.T) {
	src := &fakeSource{interactions: []nativecoder.LeadInteraction{
		{
			UUID:           "lead-1",
			Kind:           nativecoder.DevLeadInteractionKind,
			Repo:           "msgmate-io/open-chat-go",
			PRNumber:       7,
			Status:         nativecoder.LeadInteractionActive,
			LastActivityAt: time.Now(),
		},
		{
			UUID:           "past-1",
			Kind:           "msgmate-coder",
			Repo:           "msgmate-io/open-chat-go",
			PRNumber:       3,
			Status:         nativecoder.LeadInteractionCompleted,
			LastActivityAt: time.Now().Add(-time.Hour),
		},
		{
			// Different repo: never a relay target.
			UUID:           "other-1",
			Kind:           nativecoder.DevLeadInteractionKind,
			Repo:           "msgmate-io/open-chat-go-ci",
			Status:         nativecoder.LeadInteractionActive,
			LastActivityAt: time.Now(),
		},
	}}
	del := &fakeDeliverer{}
	relay := Relay{Interactions: src, Deliverer: del}

	res, err := relay.Ingest(context.Background(), Event{
		SchemaVersion: EventSchemaVersion,
		Kind:          nativecoder.CIFailureEventKind,
		Payload: json.RawMessage(`{"provider":"github","repo":"https://github.com/msgmate-io/open-chat-go.git",` +
			`"pr_number":7,"branch":"fix/ci","severity":"high"}`),
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if res.Repo != "msgmate-io/open-chat-go" {
		t.Fatalf("repo not normalized through the envelope: %q", res.Repo)
	}
	if !res.LeadFound || res.LeadInteractionID != "lead-1" || !res.LeadRunning {
		t.Fatalf("lead not resolved: %+v", res)
	}
	if res.TargetInteractionID != "lead-1" || res.Route != string(nativecoder.RouteAppendOngoing) {
		t.Fatalf("expected an append to the running lead, got route=%q target=%q", res.Route, res.TargetInteractionID)
	}
	if len(res.CandidateInteractions) != 2 || res.CandidateInteractions[0] != "lead-1" || res.CandidateInteractions[1] != "past-1" {
		t.Fatalf("candidate interactions must be the repo's live + recent ones: %v", res.CandidateInteractions)
	}
	if len(del.appended) != 1 || !strings.HasPrefix(del.appended[0], "lead-1|") {
		t.Fatalf("expected the prompt appended to lead-1, got %v", del.appended)
	}
}

func TestIngestRejectsInvalidCIFailurePayload(t *testing.T) {
	src := &fakeSource{}
	relay := Relay{Interactions: src}
	_, err := relay.Ingest(context.Background(), Event{
		Kind:    nativecoder.CIFailureEventKind,
		Payload: json.RawMessage(`{"pr_number":1}`),
	})
	if !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("expected ErrInvalidEvent for a payload without a repo, got %v", err)
	}
}

func TestEventsHandlerAuthDispatchAndStatuses(t *testing.T) {
	relay := Relay{Interactions: &fakeSource{}}
	handler := EventsHandler(relay, "secret-token")

	post := func(token string, body []byte) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, EventsPath, strings.NewReader(string(body)))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	ciPayload := map[string]interface{}{"repo": "msgmate-io/open-chat-go"}

	if rec := post("wrong", envelope(t, nativecoder.CIFailureEventKind, ciPayload)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for a wrong token, got %d", rec.Code)
	}
	if rec := post("", envelope(t, nativecoder.CIFailureEventKind, ciPayload)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without a token, got %d", rec.Code)
	}
	if rec := post("secret-token", envelope(t, "ci_success", ciPayload)); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for an unsupported kind, got %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := post("secret-token", envelope(t, nativecoder.CIFailureEventKind, map[string]interface{}{"pr_number": 3})); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for an invalid ci_failure payload, got %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := post("secret-token", envelope(t, nativecoder.CIFailureEventAliasKind, ciPayload)); rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for the ci_error alias, got %d (%s)", rec.Code, rec.Body.String())
	}

	rec := post("secret-token", envelope(t, nativecoder.CIFailureEventKind, ciPayload))
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
	if rec := post("secret-token", []byte(`{"kind":"ci_failure","payload":`)); rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a malformed envelope, got %d", rec.Code)
	}

	get := httptest.NewRequest(http.MethodGet, EventsPath, nil)
	get.Header.Set("Authorization", "Bearer secret-token")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, get)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for GET, got %d", rr.Code)
	}
}
