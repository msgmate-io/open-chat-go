// Package cirelay is the core-side base-integration extension point for the
// CI-error relay (ci#607): it ingests a structured, versioned, secret-free
// ci_failure event handed over by an external CI system and resolves it to the
// target repo's development lead plus the candidate agent interactions.
//
// It is deliberately small and transport-thin: the CI side only emits the
// payload (payload + auth), the core validates it, resolves the routing targets
// through the pure nativecoder.DevLeadRouter and hands the rendered prompt to an
// integration-supplied Deliverer. Integrations that do not implement the
// capability are unaffected: with no Deliverer wired in the relay still
// resolves and reports, it just does not deliver (backwards compatible).
//
// Usage (the open-chat-go workflow is only a thin caller that forwards the
// raw payload):
//
//	relay := cirelay.Relay{Interactions: store, Deliverer: chooser}
//	http.Handle(cirelay.RelayPath, cirelay.RelayHandler(relay, os.Getenv(cirelay.RelayTokenEnv)))
package cirelay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"backend/nativecoder"
)

const (
	// RelayPath is the ingestion endpoint an external CI system posts a
	// ci_failure event to. The open-chat-go workflow is only a thin caller.
	RelayPath = "/api/integrations/ci-failure"
	// RelayTokenEnv names the environment variable holding the least-privilege
	// relay token. No token is ever logged or echoed back.
	RelayTokenEnv = "CI_RELAY_TOKEN"
	// RelayPayloadLimit caps the accepted body so a relay cannot be used to
	// push unbounded data (logs are digests/excerpts only).
	RelayPayloadLimit = 64 << 10
)

// InteractionSource enumerates a repo's development-agent interactions (live +
// recent) for the chooser. It must be read-only and side-effect free.
type InteractionSource interface {
	RepoInteractions(ctx context.Context, repo string) ([]nativecoder.LeadInteraction, error)
}

// Deliverer is the routing capability an integration (agent chooser /
// development lead) implements. All three operations are idempotent-friendly:
// the core may retry a relay after a transient failure.
type Deliverer interface {
	// AppendToInteraction appends a rendered fix prompt to a live interaction.
	AppendToInteraction(ctx context.Context, interactionUUID, prompt string) error
	// DispatchAgent starts a fresh agent session for the failing branch/PR.
	DispatchAgent(ctx context.Context, event nativecoder.CIFailureEvent, leadUUID, prompt string) error
}

// Resolution is the structured outcome the chooser returns to the relay caller
// (and asserts in tests): the resolved lead, the candidate interactions and
// whether a fix session was dispatched.
type Resolution struct {
	Repo                  string   `json:"repo"`
	FixSessionKey         string   `json:"fix_session_key"`
	Route                 string   `json:"route"`
	Attempt               int      `json:"attempt,omitempty"`
	MaxAttempts           int      `json:"max_attempts,omitempty"`
	LeadInteractionID     string   `json:"lead_interaction_id,omitempty"`
	LeadFound             bool     `json:"lead_found"`
	LeadRunning           bool     `json:"lead_running"`
	TargetInteractionID   string   `json:"target_interaction_id,omitempty"`
	TargetInteractionKind string   `json:"target_interaction_kind,omitempty"`
	CandidateInteractions []string `json:"candidate_interactions,omitempty"`
	// Dispatch reports whether delivering this decision starts a new agent turn
	// (spawn_new / reactivate_past) rather than appending to a live interaction
	// (append_ongoing). Dedupe and escalation deliver nothing at all.
	Dispatch bool   `json:"dispatch"`
	Prompt   string `json:"prompt"`
}

// Relay ties the pure routing decision to the integration-supplied store and
// deliverer. A zero Deliverer is valid: the relay then resolves without
// delivering.
type Relay struct {
	Router       nativecoder.DevLeadRouter
	Interactions InteractionSource
	Deliverer    Deliverer
}

// Resolve validates the event, enumerates the repo's interactions, resolves the
// lead and computes the candidate-interaction list + routing decision. It has no
// side effects, so it is safe to call before delivering.
func (r Relay) Resolve(ctx context.Context, event nativecoder.CIFailureEvent) (Resolution, error) {
	if err := event.Validate(); err != nil {
		return Resolution{}, err
	}
	if r.Interactions == nil {
		return Resolution{}, errors.New("cirelay: no interaction source configured")
	}
	repo := nativecoder.NormalizeRepo(event.Repo)
	interactions, err := r.Interactions.RepoInteractions(ctx, repo)
	if err != nil {
		return Resolution{}, fmt.Errorf("cirelay: enumerate interactions for %s: %w", repo, err)
	}
	decision, err := r.Router.Route(event, interactions)
	if err != nil {
		return Resolution{}, err
	}
	res := Resolution{
		Repo:                  repo,
		FixSessionKey:         event.FixSessionKey(),
		Route:                 string(decision.Route),
		Attempt:               decision.Attempt,
		MaxAttempts:           decision.MaxAttempts,
		TargetInteractionID:   decision.TargetInteractionUUID,
		TargetInteractionKind: decision.TargetInteractionKind,
		CandidateInteractions: interactionUUIDs(r.Router.CandidateInteractions(repo, interactions)),
		// A fresh session and a reactivated past interaction both need a new
		// agent turn; only an append to a live interaction (or a dedupe /
		// escalation, which deliver nothing) avoids one.
		Dispatch: dispatchRequired(decision),
		Prompt:   decision.Prompt(event),
	}
	if lead, ok := r.Router.ResolveLeadInteraction(repo, interactions); ok {
		res.LeadFound = true
		res.LeadInteractionID = lead.UUID
		res.LeadRunning = lead.IsActive()
	}
	return res, nil
}

// dispatchRequired reports whether the decision needs a *new* agent turn rather
// than an append to a live interaction: a fresh plan->implement session
// (spawn_new) or a reactivation of a past interaction (reactivate_past).
// append_ongoing is delivered by appending, while dedupe and escalation deliver
// nothing at all.
func dispatchRequired(decision nativecoder.LeadRoutingDecision) bool {
	return decision.Route == nativecoder.RouteSpawnNew || decision.Route == nativecoder.RouteReactivatePast
}

// Handle resolves a relayed event and, when a Deliverer is wired in, delivers the
// fix prompt by route:
//
//   - append_ongoing appends the prompt to the chosen live interaction;
//   - spawn_new / reactivate_past start a new agent turn, targeted at the
//     re-activated interaction when the router picked one;
//   - dedupe_existing and escalate_maintainer deliver nothing (the failure is
//     already being handled, or a human must intervene).
func (r Relay) Handle(ctx context.Context, event nativecoder.CIFailureEvent) (Resolution, error) {
	res, err := r.Resolve(ctx, event)
	if err != nil {
		return Resolution{}, err
	}
	if r.Deliverer == nil {
		return res, nil
	}
	switch res.Route {
	case string(nativecoder.RouteSpawnNew), string(nativecoder.RouteReactivatePast):
		return res, r.Deliverer.DispatchAgent(ctx, event, res.TargetInteractionID, res.Prompt)
	case string(nativecoder.RouteAppendOngoing):
		if res.TargetInteractionID == "" {
			return res, errors.New("cirelay: append_ongoing without a target interaction")
		}
		return res, r.Deliverer.AppendToInteraction(ctx, res.TargetInteractionID, res.Prompt)
	default:
		return res, nil
	}
}

// ParseEvent decodes a relay payload using the shared contract parser.
func ParseEvent(raw []byte) (nativecoder.CIFailureEvent, error) {
	return nativecoder.ParseCIFailureEvent(raw)
}

// RelayHandler is the HTTP ingestion hook: POST RelayPath with the relay bearer
// token and a ci_failure JSON body. It performs no routing logic of its own.
func RelayHandler(relay Relay, token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if token != "" && !validToken(req.Header.Get("Authorization"), token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		raw, err := readLimited(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		event, err := ParseEvent(raw)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		res, err := relay.Handle(req.Context(), event)
		if err != nil {
			http.Error(w, "relay failed", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
	})
}

func readLimited(req *http.Request) ([]byte, error) {
	raw := make([]byte, 0, 1024)
	buf := make([]byte, 4096)
	for {
		n, err := req.Body.Read(buf)
		if n > 0 {
			raw = append(raw, buf[:n]...)
			if len(raw) > RelayPayloadLimit {
				return nil, errors.New("ci_failure payload too large")
			}
		}
		if err != nil {
			break
		}
	}
	if len(raw) == 0 {
		return nil, errors.New("empty ci_failure payload")
	}
	return raw, nil
}

func validToken(header, want string) bool {
	got := strings.TrimSpace(header)
	if lower := strings.ToLower(got); strings.HasPrefix(lower, "bearer ") {
		got = strings.TrimSpace(got[len("bearer "):])
	}
	return got != "" && got == want
}

func interactionUUIDs(in []nativecoder.LeadInteraction) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, item := range in {
		if item.UUID != "" {
			out = append(out, item.UUID)
		}
	}
	return out
}
