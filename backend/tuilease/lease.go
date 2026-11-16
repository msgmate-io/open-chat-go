// Package tuilease tracks whether the coder TUI that drives an interaction over
// a local ssh link is still attached (ci#1101).
//
// A coder interaction is frequently driven by a TUI on the operator's machine
// that reaches the sandbox over a local ssh link. When that TUI goes away - the
// terminal is killed, the ssh link drops, the laptop sleeps - the interaction
// itself used to keep running upstream for a long time: work continued although
// nobody could see it, confirm it or steer it any more.
//
// That is now a first-class failure state:
//
//   - the TUI renews a lease per (chat, session) while it drives the
//     interaction (Heartbeat);
//   - a clean quit calls Detach, a killed terminal simply stops renewing and the
//     lease expires;
//   - either way the lease turns into a Report, which renders as a visible
//     detached-failure turn carrying the upstream sandboxes the interaction can
//     be resumed in (ContinueRequest).
//
// The package deliberately holds no credentials: a lease records only
// chat/session/sandbox identifiers plus the transport name, and a Report never
// carries an attach token, kubeconfig, owner identity or any other user data.
// Resuming is a fresh attach decision, not a replay of a secret.
package tuilease

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultLeaseTTL is how long a lease survives without a heartbeat. It is
	// generous enough for a slow turn to be rendered, short enough that a killed
	// TUI is noticed long before the upstream run finishes on its own.
	DefaultLeaseTTL = 90 * time.Second

	// TransportSSH names the local ssh link a TUI uses to reach the sandbox.
	TransportSSH = "ssh"

	// ReasonTUIdetached is the recorded failure reason of a detached TUI.
	ReasonTUIdetached = "tui_detached"

	// MetadataKey is the message metadata key carrying a Report. The chat UI
	// resolves the detached-failure widget from it.
	MetadataKey = "tui_detached"
)

// ErrInvalidLease is returned for a lease that cannot identify who is driving
// which interaction, and for a continue request that does not name a sandbox
// this interaction could actually be resumed in.
var ErrInvalidLease = errors.New("tuilease: invalid lease")

// Lease identifies one TUI client driving one interaction.
type Lease struct {
	ChatUUID        string
	InteractionUUID string
	SessionID       string
	SandboxUUID     string
	// Transport names how the client reaches the sandbox; TransportSSH for a
	// local ssh link.
	Transport string
	// LastSeen is the last heartbeat.
	LastSeen time.Time
}

// Key returns the registry key of the lease: one lease per chat and session, so
// two TUIs driving the same chat never shadow each other.
func (l Lease) Key() string {
	return strings.TrimSpace(l.ChatUUID) + "|" + strings.TrimSpace(l.SessionID)
}

// validate checks the fields a lease needs to be actionable.
func (l Lease) validate() error {
	if strings.TrimSpace(l.ChatUUID) == "" {
		return fmt.Errorf("%w: chat uuid is required", ErrInvalidLease)
	}
	if strings.TrimSpace(l.SessionID) == "" {
		return fmt.Errorf("%w: session id is required", ErrInvalidLease)
	}
	return nil
}

// TransportOr returns the lease transport, defaulting to the local ssh link.
func (l Lease) TransportOr() string {
	if t := strings.TrimSpace(l.Transport); t != "" {
		return t
	}
	return TransportSSH
}

// Registry holds the live TUI leases. It keeps no durable state: a restart of
// the server simply forgets the leases and every driving TUI re-registers with
// its next heartbeat.
type Registry struct {
	mu     sync.Mutex
	ttl    time.Duration
	leases map[string]Lease
}

// NewRegistry returns a registry with the given lease TTL. A non-positive TTL
// falls back to DefaultLeaseTTL.
func NewRegistry(ttl time.Duration) *Registry {
	if ttl <= 0 {
		ttl = DefaultLeaseTTL
	}
	return &Registry{ttl: ttl, leases: map[string]Lease{}}
}

// TTL returns the effective lease TTL.
func (r *Registry) TTL() time.Duration {
	if r == nil || r.ttl <= 0 {
		return DefaultLeaseTTL
	}
	return r.ttl
}

// Heartbeat registers or renews the lease at now.
func (r *Registry) Heartbeat(l Lease, now time.Time) error {
	if r == nil {
		return fmt.Errorf("%w: no registry", ErrInvalidLease)
	}
	if err := l.validate(); err != nil {
		return err
	}
	l.Transport = l.TransportOr()
	l.ChatUUID = strings.TrimSpace(l.ChatUUID)
	l.SessionID = strings.TrimSpace(l.SessionID)
	l.SandboxUUID = strings.TrimSpace(l.SandboxUUID)
	l.LastSeen = now.UTC()

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.leases == nil {
		r.leases = map[string]Lease{}
	}
	r.leases[l.Key()] = l
	return nil
}

// Detach removes the lease of a TUI that reported a clean detach. It returns the
// removed lease and whether one was present; detaching an unknown lease is not an
// error (a killed TUI never gets to report).
func (r *Registry) Detach(chatUUID, sessionID string) (Lease, bool) {
	if r == nil {
		return Lease{}, false
	}
	key := Lease{ChatUUID: chatUUID, SessionID: sessionID}.Key()
	r.mu.Lock()
	defer r.mu.Unlock()
	lease, ok := r.leases[key]
	if !ok {
		return Lease{}, false
	}
	delete(r.leases, key)
	return lease, true
}

// Expired returns the leases that stopped heartbeating by now and forgets them:
// a TUI that died without a clean detach. The result is sorted by chat and
// session so a caller reporting them is deterministic.
func (r *Registry) Expired(now time.Time) []Lease {
	if r == nil {
		return nil
	}
	deadline := now.Add(-r.TTL())
	r.mu.Lock()
	defer r.mu.Unlock()
	var expired []Lease
	for key, lease := range r.leases {
		if lease.LastSeen.Before(deadline) {
			expired = append(expired, lease)
			delete(r.leases, key)
		}
	}
	sort.Slice(expired, func(i, j int) bool { return expired[i].Key() < expired[j].Key() })
	return expired
}

// ActiveLeases returns a copy of the leases of one chat.
func (r *Registry) ActiveLeases(chatUUID string) []Lease {
	if r == nil {
		return nil
	}
	want := strings.TrimSpace(chatUUID)
	r.mu.Lock()
	defer r.mu.Unlock()
	var active []Lease
	for _, lease := range r.leases {
		if lease.ChatUUID == want {
			active = append(active, lease)
		}
	}
	sort.Slice(active, func(i, j int) bool { return active[i].SessionID < active[j].SessionID })
	return active
}

// Candidate is one upstream sandbox the detached interaction can be resumed in.
// It carries identifiers and a human label only - never a token, a kubeconfig or
// any other credential.
type Candidate struct {
	SandboxUUID string
	Provider    string
	Label       string
	ExpiresAt   time.Time
	// Running reports whether the sandbox still has work in flight, which is the
	// most useful default choice when resuming.
	Running bool
}

// Report is the detached-failure state of one interaction: what was observed,
// which sandboxes can take over, and how to resume.
type Report struct {
	Reason          string
	ChatUUID        string
	InteractionUUID string
	SessionID       string
	SandboxUUID     string
	Transport       string
	DetachedAt      time.Time
	Candidates      []Candidate
}

// NewReport builds the detached-failure report of a lease at now.
func NewReport(l Lease, now time.Time, candidates []Candidate) Report {
	report := Report{
		Reason:          ReasonTUIdetached,
		ChatUUID:        strings.TrimSpace(l.ChatUUID),
		InteractionUUID: strings.TrimSpace(l.InteractionUUID),
		SessionID:       strings.TrimSpace(l.SessionID),
		SandboxUUID:     strings.TrimSpace(l.SandboxUUID),
		Transport:       l.TransportOr(),
		DetachedAt:      now.UTC(),
	}
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.SandboxUUID) == "" {
			continue
		}
		candidate.SandboxUUID = strings.TrimSpace(candidate.SandboxUUID)
		report.Candidates = append(report.Candidates, candidate)
	}
	return report
}

// DefaultSandboxUUID is the sandbox a resume should propose first: the sandbox
// the interaction ran in when it is still a candidate, then a candidate with work
// in flight, then the first candidate.
func (r Report) DefaultSandboxUUID() string {
	for _, candidate := range r.Candidates {
		if candidate.SandboxUUID == r.SandboxUUID {
			return candidate.SandboxUUID
		}
	}
	for _, candidate := range r.Candidates {
		if candidate.Running {
			return candidate.SandboxUUID
		}
	}
	if len(r.Candidates) > 0 {
		return r.Candidates[0].SandboxUUID
	}
	return ""
}

// Metadata renders the report for a message's metadata: it marks the turn as a
// finished failure (so the interaction resolves as failed instead of resting
// silently) and carries the candidate sandboxes for the resume widget.
func (r Report) Metadata() map[string]interface{} {
	candidates := make([]map[string]interface{}, 0, len(r.Candidates))
	for _, candidate := range r.Candidates {
		entry := map[string]interface{}{
			"sandbox_uuid": candidate.SandboxUUID,
			"provider":     candidate.Provider,
			"label":        candidate.Label,
			"running":      candidate.Running,
		}
		if !candidate.ExpiresAt.IsZero() {
			entry["expires_at"] = candidate.ExpiresAt.UTC().Format(time.RFC3339)
		}
		candidates = append(candidates, entry)
	}
	return map[string]interface{}{
		"finished":       true,
		"error":          true,
		"failure_reason": ReasonTUIdetached,
		MetadataKey: map[string]interface{}{
			"reason":           r.Reason,
			"chat_uuid":        r.ChatUUID,
			"interaction_uuid": r.InteractionUUID,
			"session_id":       r.SessionID,
			"sandbox_uuid":     r.SandboxUUID,
			"transport":        r.Transport,
			"detached_at":      r.DetachedAt.UTC().Format(time.RFC3339),
			"default_sandbox":  r.DefaultSandboxUUID(),
			"candidates":       candidates,
		},
	}
}

// DetachedReason returns the resolved reason of the report, which is always the
// detached-TUI failure.
func (r Report) DetachedReason() string {
	if reason := strings.TrimSpace(r.Reason); reason != "" {
		return reason
	}
	return ReasonTUIdetached
}

// Text renders the visible, human-facing detached-failure message.
func (r Report) Text() string {
	var b strings.Builder
	b.WriteString("This interaction stopped: the coder TUI driving it over a local ssh link detached ")
	if r.Transport != TransportSSH {
		b.WriteString("(" + r.Transport + ") ")
	}
	b.WriteString(fmt.Sprintf("at %s. The interaction is no longer watched, so it was not continued upstream.", r.DetachedAt.UTC().Format(time.RFC3339)))
	if len(r.Candidates) == 0 {
		b.WriteString(" No upstream sandbox is available to continue it in right now; resume once a sandbox is listed again.")
		return b.String()
	}
	b.WriteString(" Continue it in one of the upstream sandboxes below to keep the work:")
	for _, candidate := range r.Candidates {
		label := strings.TrimSpace(candidate.Label)
		if label == "" {
			label = "sandbox"
		}
		b.WriteString(fmt.Sprintf(" %s (%s)", candidate.SandboxUUID, label))
	}
	b.WriteString(".")
	return b.String()
}

// ContinueRequest is the resolved "continue the interaction in this upstream
// sandbox" instruction handed to the attach path.
type ContinueRequest struct {
	ChatUUID        string
	InteractionUUID string
	SessionID       string
	SandboxUUID     string
	Provider        string
	Transport       string
	// Resume indicates the interaction should be resumed (not restarted) in the
	// selected sandbox.
	Resume bool
}

// ContinueRequest resolves the resume decision for one candidate. An empty
// sandboxUUID selects Report.DefaultSandboxUUID; an unknown one is rejected so a
// client cannot point the interaction at a sandbox the operator was never offered.
func (r Report) ContinueRequest(sandboxUUID string) (ContinueRequest, error) {
	selected := strings.TrimSpace(sandboxUUID)
	if selected == "" {
		selected = r.DefaultSandboxUUID()
	}
	if selected == "" {
		return ContinueRequest{}, fmt.Errorf("%w: no upstream sandbox is available to continue the interaction in", ErrInvalidLease)
	}
	for _, candidate := range r.Candidates {
		if candidate.SandboxUUID != selected {
			continue
		}
		return ContinueRequest{
			ChatUUID:        r.ChatUUID,
			InteractionUUID: r.InteractionUUID,
			SessionID:       r.SessionID,
			SandboxUUID:     candidate.SandboxUUID,
			Provider:        candidate.Provider,
			Transport:       r.Transport,
			Resume:          true,
		}, nil
	}
	return ContinueRequest{}, fmt.Errorf("%w: sandbox %q is not a resume candidate of this interaction", ErrInvalidLease, selected)
}
