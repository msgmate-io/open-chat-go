package nativecoder

// Long-running / streaming shell commands — opencode `bash` parity (ci#394 gap 5).
//
// `coder_bash` runs a command to completion under a timeout, so a dev server, a
// watcher or `tail -f` cannot be used at all: the call either never returns or
// returns nothing useful. opencode's shell tool separates *starting* a command
// from *observing* it.
//
// This file is the core-side contract for the same modes:
//
//   - `foreground` — the existing behaviour, kept as the default so nothing
//     changes for a short command;
//   - `background` — the command is started and the call returns a session id;
//   - `stream`     — the command is started and output is read incrementally;
//   - `interactive` — a live session that also accepts stdin (a REPL or a
//     prompt-driven command); only the byte count of stdin is kept, never the
//     bytes themselves.
//
// The integration still performs the actual `exec` (the coding integration owns
// the tool); this store owns the per-chat session state: which sessions exist,
// their status, and an *offset-addressed* output buffer so a caller can re-read
// from where it stopped (`Tail`) instead of re-running the command or dumping
// the whole log into the context window. Offsets are absolute byte positions
// from the start of the output, so they stay meaningful after the bounded tail
// buffer drops old bytes — a reader that fell behind learns it lost `Dropped`
// bytes instead of silently rendering a truncated log.

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// CoderShellToolName is the coder tool the modes extend.
	CoderShellToolName = "coder_bash"
	// CoderShellParityGapID names the ci#394 parity gap this state closes.
	CoderShellParityGapID = "shell_modes"

	// MaxCoderShellSessionsPerChat caps the live sessions of one chat, so a
	// runaway loop cannot pin unbounded state.
	MaxCoderShellSessionsPerChat = 16
	// MaxCoderShellTailBytes bounds the retained output of one session.
	MaxCoderShellTailBytes = 64 * 1024
	// MaxCoderShellCommandRunes bounds the recorded command text.
	MaxCoderShellCommandRunes = 4096
	// MaxCoderShellStdinBytes bounds one interactive write, so a stray paste
	// cannot try to push a whole file into a process.
	MaxCoderShellStdinBytes = 64 * 1024
)

// Shell modes.
const (
	CoderShellModeForeground = "foreground"
	CoderShellModeBackground = "background"
	CoderShellModeStream     = "stream"
	// CoderShellModeInteractive starts a session that also accepts stdin, so a
	// REPL or a prompt-driven command can be driven without a separate mode.
	CoderShellModeInteractive = "interactive"
)

// Session statuses.
const (
	CoderShellStatusRunning   = "running"
	CoderShellStatusCompleted = "completed"
	CoderShellStatusFailed    = "failed"
	CoderShellStatusKilled    = "killed"
)

// Validation errors, exported so a caller can classify a rejection.
var (
	ErrCoderShellEmptyChat      = errors.New("coder shell: chat id is required")
	ErrCoderShellEmptyCommand   = errors.New("coder shell: command is required")
	ErrCoderShellUnknownMode    = errors.New("coder shell: unknown mode")
	ErrCoderShellUnknownStatus  = errors.New("coder shell: unknown status")
	ErrCoderShellTooManySession = errors.New("coder shell: too many live sessions")
	ErrCoderShellNoSession      = errors.New("coder shell: unknown session")
	ErrCoderShellFinished       = errors.New("coder shell: session already finished")
	ErrCoderShellStdinTooBig    = errors.New("coder shell: stdin payload too large")
	ErrCoderShellStdinRejected  = errors.New("coder shell: mode does not accept stdin")
)

// NormalizeCoderShellMode validates a requested mode. The empty string means
// `foreground`: an existing single-shot call keeps its behaviour.
func NormalizeCoderShellMode(mode string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", CoderShellModeForeground:
		return CoderShellModeForeground, nil
	case CoderShellModeBackground:
		return CoderShellModeBackground, nil
	case CoderShellModeStream:
		return CoderShellModeStream, nil
	case CoderShellModeInteractive:
		return CoderShellModeInteractive, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrCoderShellUnknownMode, mode)
	}
}

// CoderShellModeIsLive reports whether a mode hands back a session the caller
// has to observe, as opposed to a foreground call that returns its output. An
// interactive session can accept stdin, so it is live as well.
func CoderShellModeIsLive(mode string) bool {
	return mode == CoderShellModeBackground || mode == CoderShellModeStream || mode == CoderShellModeInteractive
}

// CoderShellModeAcceptsStdin reports whether a mode accepts stdin writes. Only
// an interactive session does; a background or streaming command has no prompt
// to answer, so a stray write is rejected instead of being buffered forever.
func CoderShellModeAcceptsStdin(mode string) bool {
	return mode == CoderShellModeInteractive
}

// CoderShellSession is one shell invocation tracked for a chat.
type CoderShellSession struct {
	ID        string `json:"id"`
	Mode      string `json:"mode"`
	Command   string `json:"command"`
	Status    string `json:"status"`
	StartedAt string `json:"started_at,omitempty"`
	EndedAt   string `json:"ended_at,omitempty"`
	// OutputBytes counts every byte the session produced, including bytes the
	// tail buffer already dropped.
	OutputBytes int64 `json:"output_bytes"`
	// RetainedBytes is the size of the readable tail.
	RetainedBytes int64 `json:"retained_bytes"`
	// DroppedBytes is how much output is no longer readable.
	DroppedBytes int64 `json:"dropped_bytes"`
	// ExitCode is set once the session finished.
	ExitCode *int `json:"exit_code,omitempty"`
	// StdinBytes counts the bytes written to an interactive session. The bytes
	// themselves are deliberately not kept: stdin can contain user data.
	StdinBytes int64 `json:"stdin_bytes,omitempty"`
}

// CoderShellOutputChunk is an incremental read of a session's output.
type CoderShellOutputChunk struct {
	SessionID string `json:"session_id"`
	// Offset is the absolute position the returned data starts at.
	Offset int64 `json:"offset"`
	// NextOffset is the offset to pass to the next read.
	NextOffset int64  `json:"next_offset"`
	Data       string `json:"data"`
	Status     string `json:"status"`
	// Dropped counts the bytes that were produced after the reader's offset but
	// are no longer retained, so the reader knows the log has a gap.
	Dropped int64 `json:"dropped"`
	// Pending reports whether more output may still arrive.
	Pending bool `json:"pending"`
}

type coderShellBuffer struct {
	session CoderShellSession
	output  []byte
}

// CoderShellStore holds the shell sessions of every chat, isolated per chat.
type CoderShellStore struct {
	mu       sync.Mutex
	sequence int64
	sessions map[string]map[string]*coderShellBuffer
}

// NewCoderShellStore returns an empty store.
func NewCoderShellStore() *CoderShellStore {
	return &CoderShellStore{sessions: map[string]map[string]*coderShellBuffer{}}
}

// Start records a new shell session for chatID and returns it. A foreground
// session is not "live": it is created and finished by the same call, so it
// never counts against the live-session cap.
func (s *CoderShellStore) Start(chatID, command, mode string, at time.Time) (CoderShellSession, error) {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return CoderShellSession{}, ErrCoderShellEmptyChat
	}
	command = strings.TrimSpace(command)
	if command == "" {
		return CoderShellSession{}, ErrCoderShellEmptyCommand
	}
	normalizedMode, err := NormalizeCoderShellMode(mode)
	if err != nil {
		return CoderShellSession{}, err
	}
	if runes := []rune(command); len(runes) > MaxCoderShellCommandRunes {
		command = string(runes[:MaxCoderShellCommandRunes]) + " … (truncated)"
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions == nil {
		s.sessions = map[string]map[string]*coderShellBuffer{}
	}
	if s.sessions[chatID] == nil {
		s.sessions[chatID] = map[string]*coderShellBuffer{}
	}
	if live := countCoderShellLiveLocked(s.sessions[chatID]); live >= MaxCoderShellSessionsPerChat {
		return CoderShellSession{}, fmt.Errorf("%w: %d live sessions for this chat", ErrCoderShellTooManySession, live)
	}
	s.sequence++
	session := CoderShellSession{
		// Zero-padded so the id sorts chronologically as a plain string, which
		// keeps listing deterministic without a second ordering key.
		ID:      fmt.Sprintf("sh-%04d", s.sequence),
		Mode:    normalizedMode,
		Command: command,
		Status:  CoderShellStatusRunning,
	}
	if !at.IsZero() {
		session.StartedAt = at.UTC().Format(time.RFC3339)
	}
	s.sessions[chatID][session.ID] = &coderShellBuffer{session: session}
	return session, nil
}

// Get returns one session of one chat. The session id alone is never enough:
// a caller cannot address another chat's session.
func (s *CoderShellStore) Get(chatID, sessionID string) (CoderShellSession, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	buffer, ok := s.sessions[strings.TrimSpace(chatID)][strings.TrimSpace(sessionID)]
	if !ok {
		return CoderShellSession{}, false
	}
	return buffer.session, true
}

// List returns every session of a chat, oldest first.
func (s *CoderShellStore) List(chatID string) []CoderShellSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	buffers := s.sessions[strings.TrimSpace(chatID)]
	out := make([]CoderShellSession, 0, len(buffers))
	for _, buffer := range buffers {
		out = append(out, buffer.session)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Append records output produced by a running session and returns the updated
// session. Appending to a finished session is an error: output after the exit
// is a bug in the caller, not something to silently swallow.
func (s *CoderShellStore) Append(chatID, sessionID, output string) (CoderShellSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	buffer, err := s.lookupLocked(chatID, sessionID)
	if err != nil {
		return CoderShellSession{}, err
	}
	if buffer.session.Status != CoderShellStatusRunning {
		return CoderShellSession{}, fmt.Errorf("%w: %s", ErrCoderShellFinished, buffer.session.Status)
	}
	buffer.output = append(buffer.output, output...)
	buffer.session.OutputBytes += int64(len(output))
	if excess := len(buffer.output) - MaxCoderShellTailBytes; excess > 0 {
		buffer.output = append([]byte(nil), buffer.output[excess:]...)
		buffer.session.DroppedBytes += int64(excess)
	}
	buffer.session.RetainedBytes = int64(len(buffer.output))
	return buffer.session, nil
}

// Write records stdin sent to an interactive session and returns the updated
// session. Only an interactive session accepts stdin, the payload is bounded,
// and only the byte count is kept: stdin can carry user data or a secret, so the
// bytes are never retained or echoed back.
func (s *CoderShellStore) Write(chatID, sessionID, input string) (CoderShellSession, error) {
	if len(input) > MaxCoderShellStdinBytes {
		return CoderShellSession{}, fmt.Errorf("%w: %d > %d bytes", ErrCoderShellStdinTooBig, len(input), MaxCoderShellStdinBytes)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	buffer, err := s.lookupLocked(chatID, sessionID)
	if err != nil {
		return CoderShellSession{}, err
	}
	if buffer.session.Status != CoderShellStatusRunning {
		return CoderShellSession{}, fmt.Errorf("%w: %s", ErrCoderShellFinished, buffer.session.Status)
	}
	if !CoderShellModeAcceptsStdin(buffer.session.Mode) {
		return CoderShellSession{}, fmt.Errorf("%w: mode %s", ErrCoderShellStdinRejected, buffer.session.Mode)
	}
	buffer.session.StdinBytes += int64(len(input))
	return buffer.session, nil
}

// Tail reads a session's retained output from an absolute offset. A reader that
// fell behind the retained tail gets `Dropped > 0` and the newest bytes rather
// than a silent gap.
func (s *CoderShellStore) Tail(chatID, sessionID string, fromOffset int64) (CoderShellOutputChunk, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	buffer, err := s.lookupLocked(chatID, sessionID)
	if err != nil {
		return CoderShellOutputChunk{}, err
	}
	if fromOffset < 0 {
		fromOffset = 0
	}
	start := buffer.session.DroppedBytes
	chunk := CoderShellOutputChunk{
		SessionID: buffer.session.ID,
		Offset:    start,
		Status:    buffer.session.Status,
		Pending:   buffer.session.Status == CoderShellStatusRunning,
	}
	if fromOffset < start {
		chunk.Dropped = start - fromOffset
	} else if fromOffset > start {
		start = fromOffset
		chunk.Offset = start
	}
	end := buffer.session.OutputBytes
	// A reader may pass an offset past the end of the output (for example a
	// resumed session); clamp instead of slicing out of range, and report the
	// real end so the next read resumes correctly.
	if start > end {
		start = end
		chunk.Offset = end
	}
	chunk.Data = string(buffer.output[start-buffer.session.DroppedBytes:])
	chunk.NextOffset = end
	return chunk, nil
}

// Finish closes a session with a terminal status and an exit code.
func (s *CoderShellStore) Finish(chatID, sessionID, status string, exitCode int, at time.Time) (CoderShellSession, error) {
	switch status {
	case CoderShellStatusCompleted, CoderShellStatusFailed, CoderShellStatusKilled:
	default:
		return CoderShellSession{}, fmt.Errorf("%w: %q", ErrCoderShellUnknownStatus, status)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	buffer, err := s.lookupLocked(chatID, sessionID)
	if err != nil {
		return CoderShellSession{}, err
	}
	buffer.session.Status = status
	if !at.IsZero() {
		buffer.session.EndedAt = at.UTC().Format(time.RFC3339)
	}
	code := exitCode
	buffer.session.ExitCode = &code
	return buffer.session, nil
}

// Kill marks a running session as killed, as a `coder_bash` kill would.
func (s *CoderShellStore) Kill(chatID, sessionID string, at time.Time) (CoderShellSession, error) {
	return s.Finish(chatID, sessionID, CoderShellStatusKilled, -1, at)
}

// Clear drops every session of a chat, so a finished interaction leaves no
// scratch state (and no live-session budget) behind.
func (s *CoderShellStore) Clear(chatID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, strings.TrimSpace(chatID))
}

func (s *CoderShellStore) lookupLocked(chatID, sessionID string) (*coderShellBuffer, error) {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return nil, ErrCoderShellEmptyChat
	}
	buffer, ok := s.sessions[chatID][strings.TrimSpace(sessionID)]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrCoderShellNoSession, sessionID)
	}
	return buffer, nil
}

func countCoderShellLiveLocked(sessions map[string]*coderShellBuffer) int {
	live := 0
	for _, buffer := range sessions {
		// A foreground session is started and finished by the same synchronous
		// call, so it can never be a runaway: only live (background/streaming/
		// interactive) sessions count against the cap.
		if buffer.session.Mode == CoderShellModeForeground {
			continue
		}
		if buffer.session.Status == CoderShellStatusRunning {
			live++
		}
	}
	return live
}

// RenderCoderShellSessions renders the sessions of one chat for the coder UI:
// newest state first, no command output, so rendering never re-fills the context
// window it exists to protect.
func RenderCoderShellSessions(sessions []CoderShellSession) string {
	if len(sessions) == 0 {
		return "Shell sessions: none."
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "Shell sessions (%d):\n", len(sessions))
	for _, session := range sessions {
		code := ""
		if session.ExitCode != nil {
			code = fmt.Sprintf(", exit %d", *session.ExitCode)
		}
		fmt.Fprintf(&builder, "- %s [%s/%s] %s (%d bytes%s)\n",
			session.ID, session.Mode, session.Status, session.Command, session.OutputBytes, code)
	}
	return strings.TrimRight(builder.String(), "\n")
}

// RenderCoderShellSession renders one session as the deterministic line the
// chat view shows right after a live `coder_bash` call started it (same shape
// as the per-session lines of RenderCoderShellSessions, so a status read and a
// start follow the same format).
func RenderCoderShellSession(session CoderShellSession) string {
	code := ""
	if session.ExitCode != nil {
		code = fmt.Sprintf(", exit %d", *session.ExitCode)
	}
	return fmt.Sprintf("%s [%s/%s] %s (%d bytes%s)",
		session.ID, session.Mode, session.Status, session.Command, session.OutputBytes, code)
}

// CoderShellSessionJSON marshals one session as stable JSON for a live
// `coder_bash` start result (versioned like CoderShellSessionsJSON, never
// including retained output bytes).
func CoderShellSessionJSON(session CoderShellSession) (string, error) {
	encoded, err := json.Marshal(struct {
		Version int               `json:"version"`
		Session CoderShellSession `json:"session"`
	}{Version: CoderShellParityVersion, Session: session})
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// CoderShellSessionsJSON renders sessions as stable JSON for run evidence.
func CoderShellSessionsJSON(sessions []CoderShellSession) (string, error) {
	if sessions == nil {
		sessions = []CoderShellSession{}
	}
	encoded, err := json.Marshal(struct {
		Version  int                 `json:"version"`
		Sessions []CoderShellSession `json:"sessions"`
	}{Version: CoderShellParityVersion, Sessions: sessions})
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// CoderShellParityVersion is the shape version of the session state.
const CoderShellParityVersion = 1
