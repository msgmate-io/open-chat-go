package nativecoder

import (
	"testing"
)

// TestWorkspaceSelectionResolverSkipSeedsNothing asserts the pre-call init
// hook that seeds the coder workspace is a no-op when the chat carries no repo
// or branch tag. The private open-chat-go-ci canary workflow relies on this:
// its device chats are tagged only with `repo:msgmate-io/open-chat-go` and
// `issue:898`, so the resolver must NOT pre-seed a workspace they never asked
// for. If it ever did, the first coder_bash/coder_read would silently use a
// sandbox repo other than the one the agent actually ran
// coder_select_workspace against, masking dispatch acceptance probes.
func TestWorkspaceSelectionResolverSkipSeedsNothing(t *testing.T) {
	resolver := WorkspaceSelectionResolver()

	// No repo, no branch, no explicit git_repo config => nothing to seed.
	payload, err := resolver.Resolve(PreCallInitContext{Tags: []string{"issue:898"}})
	if err != nil {
		t.Fatalf("Resolve returned unexpected error: %v", err)
	}
	if len(payload) != 0 {
		t.Fatalf("Resolve seeded %v for a repo-less chat; want no payload", payload)
	}

	// issue-only tags (the canary chat shape) must also seed nothing.
	payload, err = resolver.Resolve(PreCallInitContext{Tags: []string{"issue:898", "repo:msgmate-io/open-chat-go"}})
	if err != nil {
		t.Fatalf("Resolve with repo tag returned unexpected error: %v", err)
	}
	if len(payload) == 0 {
		// The repo tag alone would seed auto-sandbox, which is fine for a
		// normal devagent chat; the canary device chats, however, never have a
		// branch and MUST stay unbound so the agent's own
		// coder_select_workspace call wins.
		if _, ok := payload[AutoSandboxFlagInput]; ok {
			t.Fatalf("Resolve seeded AutoSandbox for a branch-less chat: %v", payload)
		}
	}
}
