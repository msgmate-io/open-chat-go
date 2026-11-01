package nativecoder

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCoderTodoSurfaceWriteRead(t *testing.T) {
	todo := &coderTodoSurface{store: NewCoderTodoStore()}
	at := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

	// Writing an empty plan for a chat that never wrote reads back at rev 0.
	empty, err := todo.Read("chat-a")
	if err != nil {
		t.Fatalf("read empty plan: %v", err)
	}
	if empty.Plan.Revision != 0 {
		t.Fatalf("empty plan revision = %d, want 0", empty.Plan.Revision)
	}
	if len(empty.Plan.Items) != 0 {
		t.Fatalf("empty plan has %d items, want 0", len(empty.Plan.Items))
	}
	if !strings.Contains(empty.Rendered, "none") && !strings.Contains(empty.Rendered, "No") && !strings.Contains(empty.Rendered, "no") {
		t.Logf("note: empty-plan rendering = %q", empty.Rendered)
	}

	written, err := todo.Write(CoderTodoPlanRequest{
		ChatID: "chat-a",
		Items: []CoderTodoItem{
			{ID: "1", Content: "Step one", Status: CoderTodoStatusInProgress},
			{ID: "2", Content: "Step two", Status: CoderTodoStatusCompleted},
			{ID: "3", Content: "Step three", Status: CoderTodoStatusPending},
		},
		At: at,
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if written.Plan.Revision != 1 {
		t.Fatalf("revision = %d, want 1", written.Plan.Revision)
	}
	if len(written.Plan.Items) != 3 {
		t.Fatalf("item count = %d, want 3", len(written.Plan.Items))
	}
	// Rendered markdown shows every item's content, no extra text invented.
	for _, content := range []string{"Step one", "Step two", "Step three"} {
		if !strings.Contains(written.Rendered, content) {
			t.Errorf("rendered plan missing %q (rendered=%q)", content, written.Rendered)
		}
	}
	// JSON metadata carries revision/version/counts only.
	var meta map[string]interface{}
	if err := json.Unmarshal([]byte(written.JSON), &meta); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if meta["revision"] != float64(1) {
		t.Errorf("json revision = %v, want 1", meta["revision"])
	}
	if meta["version"] != float64(CoderTodoPlanVersion) {
		t.Errorf("json version = %v, want %d", meta["version"], CoderTodoPlanVersion)
	}
	if meta["item_count"] != float64(3) {
		t.Errorf("json item_count = %v, want 3", meta["item_count"])
	}
	if meta["in_progress"] != float64(1) || meta["completed"] != float64(1) || meta["pending"] != float64(1) {
		t.Errorf("json status counts wrong: %v", meta)
	}

	// Read reflects the same plan with a fresh render.
	reread, err := todo.Read("chat-a")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if reread.Plan.Revision != 1 || len(reread.Plan.Items) != 3 {
		t.Fatalf("reread plan = %+v", reread.Plan)
	}

	// An empty items list clears the plan but bumps the revision.
	cleared, err := todo.Write(CoderTodoPlanRequest{ChatID: "chat-a", Items: []CoderTodoItem{}})
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if cleared.Plan.Revision != 2 {
		t.Fatalf("cleared revision = %d, want 2", cleared.Plan.Revision)
	}
	if len(cleared.Plan.Items) != 0 {
		t.Fatalf("cleared plan has %d items, want 0", len(cleared.Plan.Items))
	}

	// Chats are isolated from one another.
	other, err := todo.Read("chat-b")
	if err != nil {
		t.Fatalf("read chat-b: %v", err)
	}
	if other.Plan.Revision != 0 || len(other.Plan.Items) != 0 {
		t.Fatalf("chat-b plan not isolated: %+v", other.Plan)
	}

	// A rejected write surfaces the store's classified error.
	if _, err := todo.Write(CoderTodoPlanRequest{ChatID: "chat-a", Items: []CoderTodoItem{{Status: CoderTodoStatusPending}}}); !errors.Is(err, ErrCoderTodoEmptyContent) {
		t.Fatalf("empty-content write error = %v, want ErrCoderTodoEmptyContent", err)
	}
	if _, err := todo.Write(CoderTodoPlanRequest{ChatID: " ", Items: []CoderTodoItem{{Content: "x"}}}); !errors.Is(err, ErrCoderTodoEmptyChat) {
		t.Fatalf("empty-chat write error = %v, want ErrCoderTodoEmptyChat", err)
	}
}

func TestCoderShellSurfaceLifecycle(t *testing.T) {
	shell := &coderShellSurface{store: NewCoderShellStore()}
	at := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

	started, err := shell.Start(CoderShellRunRequest{ChatID: "chat-a", Command: "watch -n1 date", Mode: "background", At: at})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !strings.HasPrefix(started.Session.ID, "sh-") {
		t.Fatalf("session id = %q, want sh- prefix", started.Session.ID)
	}
	if started.Session.Mode != "background" {
		t.Fatalf("session mode = %q, want background", started.Session.Mode)
	}
	if !strings.Contains(started.Rendered, started.Session.ID) {
		t.Errorf("start render missing session id: %q", started.Rendered)
	}
	var startDoc map[string]interface{}
	if err := json.Unmarshal([]byte(started.JSON), &startDoc); err != nil {
		t.Fatalf("start json decode: %v", err)
	}
	if startDoc["version"] != float64(CoderShellParityVersion) {
		t.Errorf("start json version = %v, want %d", startDoc["version"], CoderShellParityVersion)
	}
	if _, ok := startDoc["session"]; !ok {
		t.Errorf("start json missing session key: %s", started.JSON)
	}

	// Tail of a fresh session: pending, offset 0, no data.
	chunk, err := shell.Tail(CoderShellReadRequest{ChatID: "chat-a", SessionID: started.Session.ID, FromOffset: 0})
	if err != nil {
		t.Fatalf("tail: %v", err)
	}
	if !chunk.Pending || chunk.Offset != 0 || chunk.NextOffset != 0 || chunk.Data != "" {
		t.Fatalf("fresh tail chunk = %+v", chunk)
	}

	// Append output; tail resumes at the new end.
	appended, err := shell.Append(CoderShellReadRequest{ChatID: "chat-a", SessionID: started.Session.ID}, "line one\n")
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if appended.OutputBytes != int64(len("line one\n")) {
		t.Fatalf("output bytes = %d, want %d", appended.OutputBytes, len("line one\n"))
	}
	chunk2, err := shell.Tail(CoderShellReadRequest{ChatID: "chat-a", SessionID: started.Session.ID, FromOffset: 0})
	if err != nil {
		t.Fatalf("tail 2: %v", err)
	}
	if chunk2.Data != "line one\n" || chunk2.NextOffset != int64(len("line one\n")) {
		t.Fatalf("tail after append = %+v", chunk2)
	}
	chunk3, err := shell.Tail(CoderShellReadRequest{ChatID: "chat-a", SessionID: started.Session.ID, FromOffset: chunk2.NextOffset})
	if err != nil {
		t.Fatalf("tail 3: %v", err)
	}
	if chunk3.Data != "" || chunk3.Pending {
		t.Fatalf("tail past end = %+v", chunk3)
	}

	// Interactive session: stdin carries a byte count only (never the text).
	interactive, err := shell.Start(CoderShellRunRequest{ChatID: "chat-a", Command: "python3 -i", Mode: "interactive", At: at})
	if err != nil {
		t.Fatalf("start interactive: %v", err)
	}
	wrote, err := shell.Write(CoderShellReadRequest{ChatID: "chat-a", SessionID: interactive.Session.ID, Input: "print(1)\n"})
	if err != nil {
		t.Fatalf("stdin write: %v", err)
	}
	if wrote.StdinBytes != int64(len("print(1)\n")) {
		t.Fatalf("stdin bytes = %d, want %d", wrote.StdinBytes, len("print(1)\n"))
	}
	if !strings.Contains(wrote.Command, "python3") {
		t.Fatalf("interactive command lost: %+v", wrote)
	}

	// Kill freezes the session as killed.
	killed, err := shell.Kill(CoderShellReadRequest{ChatID: "chat-a", SessionID: started.Session.ID, At: at})
	if err != nil {
		t.Fatalf("kill: %v", err)
	}
	if killed.Status != "killed" {
		t.Fatalf("killed status = %q, want killed", killed.Status)
	}
	if killed.ExitCode == nil || *killed.ExitCode != -1 {
		t.Fatalf("killed exit = %+v, want -1", killed.ExitCode)
	}

	// List is oldest-first and spans every session of the chat.
	list, err := shell.List("chat-a")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("list length = %d, want 2", len(list))
	}
	if list[0].ID != started.Session.ID || list[1].ID != interactive.Session.ID {
		t.Fatalf("list order = [%s, %s]", list[0].ID, list[1].ID)
	}

	// Chats are isolated: another chat lists no sessions.
	other, err := shell.List("chat-b")
	if err != nil {
		t.Fatalf("list chat-b: %v", err)
	}
	if len(other) != 0 {
		t.Fatalf("chat-b sessions = %d, want 0", len(other))
	}
	if _, err := shell.Tail(CoderShellReadRequest{ChatID: "chat-b", SessionID: started.Session.ID}); !errors.Is(err, ErrCoderShellNoSession) {
		t.Fatalf("cross-chat tail error = %v, want ErrCoderShellNoSession", err)
	}
	// Foreground sessions never count as live, so a fresh chat can start freely.
	if _, err := shell.Start(CoderShellRunRequest{ChatID: "chat-a", Command: "echo hi", Mode: ""}); err != nil {
		t.Fatalf("start foreground: %v", err)
	}
	if _, err := shell.Start(CoderShellRunRequest{ChatID: "chat-a", Command: "", Mode: "background"}); !errors.Is(err, ErrCoderShellEmptyCommand) {
		t.Fatalf("empty-command error = %v, want ErrCoderShellEmptyCommand", err)
	}
	if _, err := shell.Start(CoderShellRunRequest{ChatID: "chat-a", Command: "x", Mode: "hologram"}); err == nil {
		t.Fatalf("unknown mode accepted")
	}
}

func TestCoderTrustSurfaceDispatch(t *testing.T) {
	trust := &coderTrustSurface{store: NewCoderSessionTrustStore()}
	at := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

	// Status on a fresh session: nothing trusted.
	status, err := trust.Apply(CoderTrustRequest{ChatID: "chat-a", Action: CoderTrustActionStatus})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !status.State.Empty() || status.State.Revision != 0 {
		t.Fatalf("fresh trust state = %+v", status.State)
	}

	// Grant one class; the resulting state reflects it and the JSON carries it.
	granted, err := trust.Apply(CoderTrustRequest{ChatID: "chat-a", Action: CoderTrustActionGrant, Classes: []string{"shell"}, At: at})
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	if !granted.State.Allows(CoderTrustClassShell) {
		t.Fatalf("granted state does not allow shell: %+v", granted.State)
	}
	if granted.State.Revision != 1 {
		t.Fatalf("granted revision = %d, want 1", granted.State.Revision)
	}
	if !strings.Contains(granted.Rendered, "shell") {
		t.Errorf("grant render missing shell class: %q", granted.Rendered)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal([]byte(granted.JSON), &doc); err != nil {
		t.Fatalf("grant json decode: %v", err)
	}
	if doc["version"] != float64(CoderSessionTrustVersion) || doc["revision"] != float64(1) {
		t.Errorf("grant json doc = %v", doc)
	}

	// Revoke the class; nothing is trusted again but the revision moved.
	revoked, err := trust.Apply(CoderTrustRequest{ChatID: "chat-a", Action: CoderTrustActionRevoke, Classes: []string{"shell"}, At: at})
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if !revoked.State.Empty() || revoked.State.Revision != 2 {
		t.Fatalf("revoked state = %+v", revoked.State)
	}

	// An unknown action is a classified sentinel, not a store call.
	if _, err := trust.Apply(CoderTrustRequest{ChatID: "chat-a", Action: "purge"}); !errors.Is(err, ErrCoderTrustUnknownAction) {
		t.Fatalf("unknown-action error = %v, want ErrCoderTrustUnknownAction", err)
	}

	// Chats are isolated.
	other, err := trust.Apply(CoderTrustRequest{ChatID: "chat-b", Action: CoderTrustActionStatus})
	if err != nil {
		t.Fatalf("status chat-b: %v", err)
	}
	if !other.State.Empty() {
		t.Fatalf("chat-b trust not isolated: %+v", other.State)
	}
}

func TestCoderParitySurfaceBundle(t *testing.T) {
	surface := NewCoderParitySurface("chat-a")
	if surface == nil || surface.Todo == nil || surface.Shell == nil || surface.Trust == nil {
		t.Fatalf("surface bundle incomplete: %+v", surface)
	}
	if surface.ChatID != "chat-a" {
		t.Fatalf("surface chat id = %q, want chat-a", surface.ChatID)
	}
	// Each sub-surface is wired to a fresh store of its own.
	if _, err := surface.Shell.List("chat-a"); err != nil {
		t.Fatalf("surface shell list: %v", err)
	}
	if _, err := surface.Todo.Write(CoderTodoPlanRequest{ChatID: "chat-a", Items: []CoderTodoItem{{Content: "x"}}}); err != nil {
		t.Fatalf("surface todo write: %v", err)
	}
}
