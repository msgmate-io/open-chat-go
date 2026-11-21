package msgmate

import (
	"context"
	"errors"
	"testing"
)

// TestNoteSandboxWorkspaceMissingRootIsBestEffort guards ci#1168 item 1: the
// producer that stamps a missing sandbox workspace root onto the chat's stored
// workspace selector must never panic and never surface an error into the tool
// loop, because it runs on the failure path of the tool call it describes.
func TestNoteSandboxWorkspaceMissingRootIsBestEffort(t *testing.T) {
	const missingPath = "/workspace/coder-00000000-0000-0000-0000-000000000000"

	tests := []struct {
		name string
		ctx  func() context.Context
	}{
		{
			name: "no turn spec on the context",
			ctx:  func() context.Context { return context.Background() },
		},
		{
			name: "turn spec without a note hook",
			ctx: func() context.Context {
				return withAgentTurnSpec(context.Background(), AgentTurnSpec{})
			},
		},
		{
			name: "note hook that fails",
			ctx: func() context.Context {
				return withAgentTurnSpec(context.Background(), AgentTurnSpec{
					NoteWorkspaceMissingRoot: func(string) error {
						return errors.New("persist failed")
					},
				})
			},
		},
		{
			name: "note hook that succeeds",
			ctx: func() context.Context {
				return withAgentTurnSpec(context.Background(), AgentTurnSpec{
					NoteWorkspaceMissingRoot: func(string) error { return nil },
				})
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := noteSandboxWorkspaceMissingRoot(tc.ctx(), missingPath); err != nil {
				t.Fatalf("noteSandboxWorkspaceMissingRoot() = %v, want nil", err)
			}
		})
	}
}

// TestNoteSandboxWorkspaceMissingRootHookNeedsChat guards that the hook is only
// built for a bot-reply turn that names a chat: a turn without a chat uuid
// cannot be attributed to a stored selector.
func TestNoteSandboxWorkspaceMissingRootHookNeedsChat(t *testing.T) {
	if hook := NoteSandboxWorkspaceMissingRootHook(context.Background()); hook != nil {
		t.Fatalf("hook = %v, want nil without a turn context", hook)
	}

	// A chat that does not exist (or no database at all) must not produce an
	// error: the observation is dropped rather than masking the tool failure.
	if err := persistSandboxWorkspaceMissingRoot("no-such-chat-uuid", "/workspace/coder-dead"); err != nil {
		t.Fatalf("persistSandboxWorkspaceMissingRoot() = %v, want nil", err)
	}
}
