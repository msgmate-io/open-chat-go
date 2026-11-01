package nativecoder

// This file is the core-side contract for the coder surface of the opencode
// parity gaps that are sequenced "now" (ci#394 gaps 1, 5 and 8). Each gap
// already ships its per-chat state (todoplan.go, shellmodes.go, sessiontrust.go)
// behind plain store methods; this file gives the tool-dispatch layer a single,
// stable contract to call so the coding integration can expose the tools
// (coder_todo_write, coder_todo_read, coder_bash background/stream/interactive,
// coder_trust) without reaching into the raw stores or re-deriving limits.
//
// A request is the validated, *normalized* form of a tool call: the integration
// maps the tool's arguments onto one of these payloads, and the coder core
// answers with the data the tool result and the chat transcript are built from.
// Every method is synchronous and free of side effects beyond the store it is
// named after, so it is unit-testable without an LLM runtime.
//
// Errors are the exported sentinel errors of the backing stores (wrapped with
// %w), so a caller can classify a rejection or a missing plan/session instead
// of parsing message text.
//
// Versioned shapes: each response carries the shape version of its backing
// document, so a consumer that renders a structured tool result can detect an
// older view instead of silently misreading it.

import (
	"sync"
	"time"
)

// --------------------------- todo list (gap 1) ---------------------------

// CoderTodoPlanRequest is the tool-dispatch form of `coder_todo_write`: it
// replaces the whole plan of one chat. An empty items list clears the plan.
type CoderTodoPlanRequest struct {
	// ChatID identifies the chat whose plan is replaced.
	ChatID string
	// Items are the new steps, in display order, exactly as the tool call
	// passed them (Content trims whitespace; Status and Priority normalize).
	Items []CoderTodoItem
	// At is the write instant. A zero value keeps the previous timestamp.
	At time.Time
}

// CoderTodoPlanResult is the tool-dispatch result of a write or read.
type CoderTodoPlanResult struct {
	// Plan is the stored plan (the write returns the updated one, the read the
	// chat's current one; a chat without a plan reads an empty plan at rev 0).
	Plan CoderTodoPlan
	// Rendered is the deterministic markdown a chat panel / tool result shows.
	Rendered string
	// JSON is the stable JSON form of the plan (revision, status counts) for
	// structured tool metadata; it never includes command/output text.
	JSON string
}

// coderTodoSurface is the state + render entry point behind coder_todo_write
// and coder_todo_read. Writing an empty list clears the plan (opencode's
// todowrite with zero steps), sharing the store's semantics.
type coderTodoSurface struct {
	store *CoderTodoStore
}

// Write replaces the plan of one chat. It is a no-side-effect reflection of
// CoderTodoStore.Write on top of a fully-populated request.
func (s *coderTodoSurface) Write(req CoderTodoPlanRequest) (CoderTodoPlanResult, error) {
	plan, err := s.store.Write(req.ChatID, req.Items, req.At)
	if err != nil {
		return CoderTodoPlanResult{}, err
	}
	return renderCoderTodoResult(plan)
}

// Read returns the current plan of one chat (an empty plan at revision 0 when
// the chat has none), like opencode's todoread.
func (s *coderTodoSurface) Read(chatID string) (CoderTodoPlanResult, error) {
	return renderCoderTodoResult(s.store.Read(chatID))
}

func renderCoderTodoResult(plan CoderTodoPlan) (CoderTodoPlanResult, error) {
	encoded, err := CoderTodoPlanJSON(plan)
	if err != nil {
		return CoderTodoPlanResult{}, err
	}
	return CoderTodoPlanResult{
		Plan:     plan,
		Rendered: RenderCoderTodoPlan(plan),
		JSON:     encoded,
	}, nil
}

// ------------------------- shell modes (gap 5) ---------------------------

// CoderShellRunRequest is the tool-dispatch form of a `coder_bash` call with a
// live mode (background / stream / interactive) or the existing foreground
// mode. The integration performs the actual exec; the contract below owns the
// per-chat session bookkeeping it then feeds.
//
// No raw command text or output ever leaves the store: a session carries the
// command and byte counts, and output is only ever read back by offset through
// Tail.
type CoderShellRunRequest struct {
	// ChatID identifies the chat the session belongs to.
	ChatID string
	// Command is the shell command, as the tool call passed it.
	Command string
	// Mode is one of the CoderShellMode* constants; empty means foreground.
	Mode string
	// At is the start instant.
	At time.Time
}

// CoderShellStartResult is what a live-mode `coder_bash` call returns: one
// session plus the ids/text the tool result and chat transcript are rendered
// from. Foreground mode is handled by the integration itself (start + finish in
// the same call), so only live sessions appear here.
type CoderShellStartResult struct {
	// Session is the new live session.
	Session CoderShellSession
	// Rendered is the deterministic text shown for the started session.
	Rendered string
	// JSON is the stable JSON form of the started session.
	JSON string
}

// CoderShellReadRequest addresses one session's retained output for a tail,
// kill, stdin write or status read.
type CoderShellReadRequest struct {
	// ChatID and SessionID together identify a session; the id alone is never
	// enough to address another chat's session.
	ChatID    string
	SessionID string
	// FromOffset is the absolute byte offset for Tail (ignored by status/kill).
	FromOffset int64
	// Input is the stdin payload for an interactive write; only its byte count
	// is kept, the bytes themselves are never retained.
	Input string
	// At is the instant for Kill.
	At time.Time
}

// coderShellSurface is the state + render entry point behind live coder_bash
// sessions. All methods are projections of the CoderShellStore methods; they
// exist so a dispatcher never has to build result text itself.
type coderShellSurface struct {
	store *CoderShellStore
}

// Start begins a live session (background / stream / interactive). The
// integration starts the process and then feeds it via Append / Finish.
func (s *coderShellSurface) Start(req CoderShellRunRequest) (CoderShellStartResult, error) {
	session, err := s.store.Start(req.ChatID, req.Command, req.Mode, req.At)
	if err != nil {
		return CoderShellStartResult{}, err
	}
	encoded, err := CoderShellSessionJSON(session)
	if err != nil {
		return CoderShellStartResult{}, err
	}
	return CoderShellStartResult{
		Session:  session,
		Rendered: RenderCoderShellSession(session),
		JSON:     encoded,
	}, nil
}

// Tail reads a session's retained output from an absolute offset, exactly like
// a `coder_bash` tail call.
func (s *coderShellSurface) Tail(req CoderShellReadRequest) (CoderShellOutputChunk, error) {
	return s.store.Tail(req.ChatID, req.SessionID, req.FromOffset)
}

// Append records output a running session produced.
func (s *coderShellSurface) Append(req CoderShellReadRequest, output string) (CoderShellSession, error) {
	return s.store.Append(req.ChatID, req.SessionID, output)
}

// Write sends stdin to an interactive session (one bounded write).
func (s *coderShellSurface) Write(req CoderShellReadRequest) (CoderShellSession, error) {
	return s.store.Write(req.ChatID, req.SessionID, req.Input)
}

// Kill marks a running session as killed.
func (s *coderShellSurface) Kill(req CoderShellReadRequest) (CoderShellSession, error) {
	session, err := s.store.Kill(req.ChatID, req.SessionID, req.At)
	if err != nil {
		return CoderShellSession{}, err
	}
	return session, nil
}

// List returns every session of a chat (oldest first) for a status read.
func (s *coderShellSurface) List(chatID string) ([]CoderShellSession, error) {
	return s.store.List(chatID), nil
}

// ------------------------- session trust (gap 8) -------------------------

// CoderTrustRequest is the tool-dispatch form of `coder_trust`. Action is one
// of the CoderTrustAction* constants; Classes are the concrete class names or
// the `all` alias, normalized exactly like the store does.
type CoderTrustRequest struct {
	// ChatID identifies the chat session the trust is scoped to.
	ChatID string
	// Action is grant, revoke or status.
	Action string
	// Classes are the requested action classes (or `all`). Status ignores them.
	Classes []string
	// At is the instant of the accepted change.
	At time.Time
}

// CoderTrustResult is what a `coder_trust` call returns.
type CoderTrustResult struct {
	// State is the resulting trust state.
	State CoderSessionTrustState
	// Rendered is the deterministic text shown in the transcript.
	Rendered string
	// JSON is the stable JSON form of the state.
	JSON string
}

// coderTrustSurface is the state + render entry point behind coder_trust.
type coderTrustSurface struct {
	store *CoderSessionTrustStore
}

// Apply runs one trust mutation and returns the resulting state. The `status`
// action performs no mutation and can never fail on the store.
func (s *coderTrustSurface) Apply(req CoderTrustRequest) (CoderTrustResult, error) {
	var (
		state CoderSessionTrustState
		err   error
	)
	switch req.Action {
	case CoderTrustActionGrant:
		state, err = s.store.Grant(req.ChatID, req.Classes, req.At)
	case CoderTrustActionRevoke:
		state, err = s.store.Revoke(req.ChatID, req.Classes, req.At)
	case CoderTrustActionStatus:
		state = s.store.State(req.ChatID)
	default:
		return CoderTrustResult{}, ErrCoderTrustUnknownAction
	}
	if err != nil {
		return CoderTrustResult{}, err
	}
	encoded, err := CoderSessionTrustJSON(state)
	if err != nil {
		return CoderTrustResult{}, err
	}
	return CoderTrustResult{
		State:    state,
		Rendered: RenderCoderSessionTrust(state),
		JSON:     encoded,
	}, nil
}

// ------------------------- shared surface state -------------------------

// CoderParitySurface bundles the three "now" gap surfaces so the coding
// integration can wire them in one place and the tool loop can hold one
// reference. The mutex protects against a nil-map constructor race only; each
// store is itself safe for concurrent use.
type CoderParitySurface struct {
	mu     sync.Mutex
	Todo   *coderTodoSurface
	Shell  *coderShellSurface
	Trust  *coderTrustSurface
	ChatID string
}

// NewCoderParitySurface returns a surface bound to a chat with fresh backing
// stores, ready for the tool-dispatch layer.
func NewCoderParitySurface(chatID string) *CoderParitySurface {
	return &CoderParitySurface{
		Todo:   &coderTodoSurface{store: NewCoderTodoStore()},
		Shell:  &coderShellSurface{store: NewCoderShellStore()},
		Trust:  &coderTrustSurface{store: NewCoderSessionTrustStore()},
		ChatID: chatID,
	}
}
