package nativecoder

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func shellClock(step int) time.Time {
	return time.Date(2025, 2, 3, 4, 5, 6, 0, time.UTC).Add(time.Duration(step) * time.Second)
}

func TestNormalizeCoderShellMode(t *testing.T) {
	for _, input := range []string{"", "FOREGROUND", " foreground "} {
		if mode, err := NormalizeCoderShellMode(input); err != nil || mode != CoderShellModeForeground {
			t.Fatalf("expected %q to mean foreground, got %q (%v)", input, mode, err)
		}
	}
	for _, mode := range []string{CoderShellModeBackground, CoderShellModeStream, CoderShellModeInteractive} {
		if got, err := NormalizeCoderShellMode(" " + mode + " "); err != nil || got != mode {
			t.Fatalf("expected %q to normalize to itself, got %q (%v)", mode, got, err)
		}
		if !CoderShellModeIsLive(mode) {
			t.Fatalf("%s must be a live session", mode)
		}
	}
	if _, err := NormalizeCoderShellMode("daemon"); !errors.Is(err, ErrCoderShellUnknownMode) {
		t.Fatalf("an unknown mode must be rejected, got %v", err)
	}
	if CoderShellModeIsLive(CoderShellModeForeground) {
		t.Fatal("foreground is not a live session")
	}
	if !CoderShellModeAcceptsStdin(CoderShellModeInteractive) {
		t.Fatal("interactive must accept stdin")
	}
	for _, mode := range []string{CoderShellModeForeground, CoderShellModeBackground, CoderShellModeStream, ""} {
		if CoderShellModeAcceptsStdin(mode) {
			t.Fatalf("%q must not accept stdin", mode)
		}
	}
}

func TestCoderShellStoreStartValidation(t *testing.T) {
	store := NewCoderShellStore()
	if _, err := store.Start("", "make test", CoderShellModeBackground, shellClock(0)); !errors.Is(err, ErrCoderShellEmptyChat) {
		t.Fatalf("a chat id is required, got %v", err)
	}
	if _, err := store.Start("chat-1", "   ", CoderShellModeBackground, shellClock(0)); !errors.Is(err, ErrCoderShellEmptyCommand) {
		t.Fatalf("a command is required, got %v", err)
	}
	if _, err := store.Start("chat-1", "make test", "foreground-ish", shellClock(0)); !errors.Is(err, ErrCoderShellUnknownMode) {
		t.Fatalf("an unknown mode must be rejected, got %v", err)
	}
	if sessions := store.List("chat-1"); len(sessions) != 0 {
		t.Fatalf("a rejected start must not record a session: %+v", sessions)
	}

	long := strings.Repeat("x", MaxCoderShellCommandRunes+25)
	session, err := store.Start("chat-1", long, CoderShellModeStream, shellClock(0))
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if len([]rune(session.Command)) > MaxCoderShellCommandRunes+len(" … (truncated)") {
		t.Fatalf("the recorded command must be bounded, got %d runes", len([]rune(session.Command)))
	}
	if !strings.HasSuffix(session.Command, "(truncated)") {
		t.Fatalf("a truncated command must say so: %q", session.Command[len(session.Command)-20:])
	}
	if session.Status != CoderShellStatusRunning || session.Mode != CoderShellModeStream {
		t.Fatalf("unexpected session: %+v", session)
	}
	if want := shellClock(0).UTC().Format(time.RFC3339); session.StartedAt != want {
		t.Fatalf("expected started_at %q, got %q", want, session.StartedAt)
	}
}

func TestCoderShellStoreOutputAndTail(t *testing.T) {
	store := NewCoderShellStore()
	session, err := store.Start("chat-1", "npm run dev", CoderShellModeBackground, shellClock(0))
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if _, err := store.Append("chat-1", session.ID, "listening on :3000\n"); err != nil {
		t.Fatalf("append failed: %v", err)
	}
	if _, err := store.Append("chat-1", session.ID, "compiled\n"); err != nil {
		t.Fatalf("append failed: %v", err)
	}

	first, err := store.Tail("chat-1", session.ID, 0)
	if err != nil {
		t.Fatalf("tail failed: %v", err)
	}
	if first.Data != "listening on :3000\ncompiled\n" || first.Offset != 0 {
		t.Fatalf("unexpected first tail: %+v", first)
	}
	if !first.Pending || first.Dropped != 0 || first.Status != CoderShellStatusRunning {
		t.Fatalf("a running session must stay pending: %+v", first)
	}

	// Resuming from the returned offset must only yield what arrived since.
	if _, err := store.Append("chat-1", session.ID, "hot reload\n"); err != nil {
		t.Fatalf("append failed: %v", err)
	}
	second, err := store.Tail("chat-1", session.ID, first.NextOffset)
	if err != nil {
		t.Fatalf("tail failed: %v", err)
	}
	if second.Data != "hot reload\n" {
		t.Fatalf("expected only the new output, got %q", second.Data)
	}
	if second.Offset != first.NextOffset {
		t.Fatalf("expected offset %d, got %d", first.NextOffset, second.Offset)
	}

	// An offset past the end clamps instead of panicking, and reports the end.
	beyond, err := store.Tail("chat-1", session.ID, 1<<20)
	if err != nil {
		t.Fatalf("tail failed: %v", err)
	}
	if beyond.Data != "" || beyond.NextOffset != second.NextOffset {
		t.Fatalf("an offset past the end must clamp: %+v", beyond)
	}

	finished, err := store.Finish("chat-1", session.ID, CoderShellStatusCompleted, 0, shellClock(4))
	if err != nil {
		t.Fatalf("finish failed: %v", err)
	}
	if finished.Status != CoderShellStatusCompleted || finished.ExitCode == nil || *finished.ExitCode != 0 {
		t.Fatalf("unexpected finished session: %+v", finished)
	}
	if finished.EndedAt == "" {
		t.Fatalf("a finished session must record ended_at: %+v", finished)
	}
	if _, err := store.Append("chat-1", session.ID, "late output"); !errors.Is(err, ErrCoderShellFinished) {
		t.Fatalf("appending after the exit must fail, got %v", err)
	}
	done, err := store.Tail("chat-1", session.ID, second.NextOffset)
	if err != nil {
		t.Fatalf("tail failed: %v", err)
	}
	if done.Pending {
		t.Fatal("a finished session must not report pending")
	}
	if _, err := store.Finish("chat-1", session.ID, "exploded", 1, shellClock(5)); !errors.Is(err, ErrCoderShellUnknownStatus) {
		t.Fatalf("an unknown status must be rejected, got %v", err)
	}
}

func TestCoderShellStoreBoundsOutputTail(t *testing.T) {
	store := NewCoderShellStore()
	session, err := store.Start("chat-1", "tail -f app.log", CoderShellModeStream, shellClock(0))
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	chunk := strings.Repeat("a", 4096)
	total := 0
	for i := 0; i < MaxCoderShellTailBytes/len(chunk)+3; i++ {
		if _, err := store.Append("chat-1", session.ID, chunk); err != nil {
			t.Fatalf("append failed: %v", err)
		}
		total += len(chunk)
	}
	tail, err := store.Tail("chat-1", session.ID, 0)
	if err != nil {
		t.Fatalf("tail failed: %v", err)
	}
	if int64(len(tail.Data)) > MaxCoderShellTailBytes {
		t.Fatalf("the retained tail must stay bounded, got %d bytes", len(tail.Data))
	}
	if tail.Dropped <= 0 {
		t.Fatal("a reader that fell behind must learn it lost output")
	}
	if int64(len(tail.Data)) != tail.NextOffset-tail.Offset {
		t.Fatalf("data/offset accounting is inconsistent: len=%d offset=%d next=%d", len(tail.Data), tail.Offset, tail.NextOffset)
	}
	// The newest bytes survive: the retained tail is the end of the stream.
	if !strings.HasSuffix(tail.Data, chunk) {
		t.Fatal("the tail must end with the newest output")
	}
	session, ok := store.Get("chat-1", session.ID)
	if !ok {
		t.Fatal("the session must exist")
	}
	if session.OutputBytes != int64(total) || session.DroppedBytes != tail.Dropped {
		t.Fatalf("unexpected byte accounting: %+v (dropped=%d)", session, tail.Dropped)
	}
	if session.RetainedBytes != int64(len(tail.Data)) {
		t.Fatalf("expected retained %d, got %d", len(tail.Data), session.RetainedBytes)
	}
}

func TestCoderShellStoreInteractiveStdin(t *testing.T) {
	store := NewCoderShellStore()
	interactive, err := store.Start("chat-1", "python3 -i", CoderShellModeInteractive, shellClock(0))
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	updated, err := store.Write("chat-1", interactive.ID, "print('hi')\n")
	if err != nil {
		t.Fatalf("stdin write failed: %v", err)
	}
	if updated.StdinBytes != int64(len("print('hi')\n")) {
		t.Fatalf("expected the stdin byte count, got %+v", updated)
	}
	// A write that is too large is rejected before it is accounted.
	if _, err := store.Write("chat-1", interactive.ID, strings.Repeat("x", MaxCoderShellStdinBytes+1)); !errors.Is(err, ErrCoderShellStdinTooBig) {
		t.Fatalf("an oversized stdin payload must be rejected, got %v", err)
	}
	background, err := store.Start("chat-1", "npm run dev", CoderShellModeBackground, shellClock(1))
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if _, err := store.Write("chat-1", background.ID, "n\n"); !errors.Is(err, ErrCoderShellStdinRejected) {
		t.Fatalf("a non-interactive session must reject stdin, got %v", err)
	}
	if _, err := store.Kill("chat-1", interactive.ID, shellClock(2)); err != nil {
		t.Fatalf("kill failed: %v", err)
	}
	if _, err := store.Write("chat-1", interactive.ID, "x"); !errors.Is(err, ErrCoderShellFinished) {
		t.Fatalf("stdin after the exit must fail, got %v", err)
	}
	killed, ok := store.Get("chat-1", interactive.ID)
	if !ok || killed.Status != CoderShellStatusKilled || killed.ExitCode == nil || *killed.ExitCode != -1 {
		t.Fatalf("unexpected killed session: %+v", killed)
	}
}

func TestCoderShellStoreIsolationAndCaps(t *testing.T) {
	store := NewCoderShellStore()
	mine, err := store.Start("chat-1", "make watch", CoderShellModeStream, shellClock(0))
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if _, ok := store.Get("chat-2", mine.ID); ok {
		t.Fatal("a session id must not address another chat's session")
	}
	if _, err := store.Append("chat-2", mine.ID, "leak"); !errors.Is(err, ErrCoderShellNoSession) {
		t.Fatalf("cross-chat access must be rejected, got %v", err)
	}
	if _, err := store.Tail("chat-2", mine.ID, 0); !errors.Is(err, ErrCoderShellNoSession) {
		t.Fatalf("cross-chat tail must be rejected, got %v", err)
	}
	if _, err := store.Tail("chat-1", "sh-9999", 0); !errors.Is(err, ErrCoderShellNoSession) {
		t.Fatalf("an unknown session must be rejected, got %v", err)
	}
	if _, err := store.Tail("   ", mine.ID, 0); !errors.Is(err, ErrCoderShellEmptyChat) {
		t.Fatalf("a chat id is required, got %v", err)
	}

	// Foreground calls are never live, so a burst of them cannot exhaust the cap.
	for i := 0; i < MaxCoderShellSessionsPerChat*2; i++ {
		session, err := store.Start("chat-3", "go test ./...", "", shellClock(i))
		if err != nil {
			t.Fatalf("a foreground call must not consume the live budget: %v", err)
		}
		if CoderShellModeIsLive(session.Mode) {
			t.Fatalf("expected a foreground session, got %+v", session)
		}
		if _, err := store.Finish("chat-3", session.ID, CoderShellStatusCompleted, 0, shellClock(i)); err != nil {
			t.Fatalf("finish failed: %v", err)
		}
	}

	// The live cap is per chat and only counts running live sessions.
	for i := 0; i < MaxCoderShellSessionsPerChat; i++ {
		if _, err := store.Start("chat-4", "node server.js", CoderShellModeBackground, shellClock(i)); err != nil {
			t.Fatalf("start %d failed: %v", i, err)
		}
	}
	if _, err := store.Start("chat-4", "node server2.js", CoderShellModeBackground, shellClock(99)); !errors.Is(err, ErrCoderShellTooManySession) {
		t.Fatalf("the live cap must apply per chat, got %v", err)
	}
	if _, err := store.Start("chat-5", "node server.js", CoderShellModeBackground, shellClock(99)); err != nil {
		t.Fatalf("the cap must not leak into another chat: %v", err)
	}

	sessions := store.List("chat-4")
	if len(sessions) != MaxCoderShellSessionsPerChat {
		t.Fatalf("expected %d sessions, got %d", MaxCoderShellSessionsPerChat, len(sessions))
	}
	for i := 1; i < len(sessions); i++ {
		if sessions[i-1].ID >= sessions[i].ID {
			t.Fatalf("sessions must be listed oldest first: %+v", sessions)
		}
	}

	store.Clear("chat-4")
	if sessions := store.List("chat-4"); len(sessions) != 0 {
		t.Fatalf("Clear must drop the chat's sessions: %+v", sessions)
	}
	if _, err := store.Start("chat-4", "node server.js", CoderShellModeBackground, shellClock(100)); err != nil {
		t.Fatalf("Clear must free the live budget: %v", err)
	}
	if sessions := store.List("chat-3"); len(sessions) == 0 {
		t.Fatal("Clear must only affect the named chat")
	}
}

func TestCoderShellStoreConcurrentUse(t *testing.T) {
	store := NewCoderShellStore()
	session, err := store.Start("chat-1", "npm run dev", CoderShellModeStream, shellClock(0))
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 16; j++ {
				if _, err := store.Append("chat-1", session.ID, "tick\n"); err != nil {
					t.Errorf("append failed: %v", err)
					return
				}
				if _, err := store.Tail("chat-1", session.ID, 0); err != nil {
					t.Errorf("tail failed: %v", err)
					return
				}
				store.List("chat-1")
				store.Get("chat-1", session.ID)
			}
		}()
	}
	for i := 0; i < 8; i++ {
		<-done
	}
	if _, err := store.Finish("chat-1", session.ID, CoderShellStatusCompleted, 0, shellClock(9)); err != nil {
		t.Fatalf("finish failed: %v", err)
	}
}

func TestCoderShellSessionsRendering(t *testing.T) {
	if got := RenderCoderShellSessions(nil); !strings.Contains(got, "none") {
		t.Fatalf("an empty store must render as none, got %q", got)
	}
	code := 130
	session := CoderShellSession{
		ID:          "sh-0001",
		Mode:        CoderShellModeInteractive,
		Command:     "python3 -i",
		Status:      CoderShellStatusKilled,
		OutputBytes: 42,
		ExitCode:    &code,
	}
	line := RenderCoderShellSessions([]CoderShellSession{session})
	for _, want := range []string{"sh-0001", CoderShellModeInteractive, CoderShellStatusKilled, "python3 -i", "42 bytes", "exit 130"} {
		if !strings.Contains(line, want) {
			t.Fatalf("rendered sessions must contain %q: %q", want, line)
		}
	}
	if strings.Contains(line, "stdin") {
		t.Fatalf("the render must not expose stdin details: %q", line)
	}

	encoded, err := CoderShellSessionsJSON([]CoderShellSession{session})
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if !strings.Contains(encoded, `"session_id"`) && !strings.Contains(encoded, `"id":"sh-0001"`) {
		t.Fatalf("unexpected json: %s", encoded)
	}
	if !strings.Contains(encoded, `"version":1`) {
		t.Fatalf("the document must carry its shape version: %s", encoded)
	}
	empty, err := CoderShellSessionsJSON(nil)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if !strings.Contains(empty, `"sessions":[]`) {
		t.Fatalf("an empty store must encode as an empty list, got %s", empty)
	}
	for _, leak := range []string{"token", "secret", "password"} {
		if strings.Contains(strings.ToLower(encoded), leak) {
			t.Fatalf("the session state must not carry %q: %s", leak, encoded)
		}
	}
}
