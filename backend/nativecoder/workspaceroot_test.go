package nativecoder

import (
	"strings"
	"testing"

	"backend/sandboxsession"
)

// ci#819 part A: the workspace-root default is one abstraction shared by the
// shell/session layer and the model-facing guidance, so the root a coder is
// told about and the root its shell actually starts in cannot drift apart.
func TestWorkspaceRootDefaultMatchesSessionLayer(t *testing.T) {
	if WorkspaceRootDefault != sandboxsession.DefaultWorkDir {
		t.Fatalf("workspace root guidance default %q != session default %q", WorkspaceRootDefault, sandboxsession.DefaultWorkDir)
	}
	if got := WorkspaceRootFromConfig(nil); got != WorkspaceRootDefault {
		t.Fatalf("empty config: got %q, want %q", got, WorkspaceRootDefault)
	}
	if got := WorkspaceRootFromConfig(map[string]interface{}{WorkspaceRootConfigKey: "  "}); got != WorkspaceRootDefault {
		t.Fatalf("blank config value: got %q, want %q", got, WorkspaceRootDefault)
	}
	if got := WorkspaceRootFromConfig(map[string]interface{}{WorkspaceRootConfigKey: " /srv/checkout/ "}); got != "/srv/checkout" {
		t.Fatalf("declared root: got %q, want %q", got, "/srv/checkout")
	}
}

// The guidance must state the root up front and explicitly retire the redundant
// `cd` prefix the ticket is about.
func TestWorkspaceRootGuidanceStatesRootAndRetiresCD(t *testing.T) {
	guidance := WorkspaceRootGuidance()
	if !strings.Contains(guidance, WorkspaceRootDefault) {
		t.Fatalf("guidance does not name the root %q: %s", WorkspaceRootDefault, guidance)
	}
	for _, fragment := range []string{"cd $(git rev-parse --show-toplevel)", "no-op", "workspace-relative"} {
		if !strings.Contains(guidance, fragment) {
			t.Errorf("guidance lacks %q: %s", fragment, guidance)
		}
	}
	if len(WorkspaceRootGuidanceLines()) < 2 {
		t.Error("guidance should render as more than one line for the prompt block")
	}
}

func TestWithWorkspaceRootDoesNotMutateCallerMap(t *testing.T) {
	original := map[string]interface{}{"coder_mode": "trust"}
	merged := WithWorkspaceRoot(original, "/srv/checkout")
	if _, ok := original[WorkspaceRootConfigKey]; ok {
		t.Fatal("WithWorkspaceRoot mutated the caller's config")
	}
	if got := merged[WorkspaceRootConfigKey]; got != "/srv/checkout" {
		t.Fatalf("merged root: got %v", got)
	}
	// A blank root leaves the default standing and the map untouched.
	if got := WithWorkspaceRoot(nil, "  "); got != nil {
		t.Fatalf("blank root should be a no-op, got %v", got)
	}
}

func TestStripRedundantWorkspaceRootCD(t *testing.T) {
	cases := []struct {
		name      string
		command   string
		root      string
		want      string
		wantStrip bool
	}{
		{
			name:      "plain root prefix",
			command:   "cd /workspace && go test ./backend/nativecoder/...",
			root:      WorkspaceRootDefault,
			want:      "go test ./backend/nativecoder/...",
			wantStrip: true,
		},
		{
			name:      "quoted and semicolon separated",
			command:   `cd "/workspace/"; git status`,
			root:      WorkspaceRootDefault,
			want:      "git status",
			wantStrip: true,
		},
		{
			name:      "rev-parse spelling",
			command:   "cd $(git rev-parse --show-toplevel) && gofmt -l .",
			root:      WorkspaceRootDefault,
			want:      "gofmt -l .",
			wantStrip: true,
		},
		{
			name:      "navigation only is dropped",
			command:   "cd /workspace",
			root:      WorkspaceRootDefault,
			want:      "",
			wantStrip: true,
		},
		{
			name:    "real subdirectory descent is untouched",
			command: "cd backend && go test ./nativecoder/...",
			root:    WorkspaceRootDefault,
			want:    "cd backend && go test ./nativecoder/...",
		},
		{
			name:    "descend into the root when the sandbox root differs",
			command: "cd /workspace && go build ./...",
			root:    "/srv/checkout",
			want:    "cd /workspace && go build ./...",
		},
		{
			name:      "declared non-default root is stripped",
			command:   "cd /srv/checkout && go build ./...",
			root:      "/srv/checkout",
			want:      "go build ./...",
			wantStrip: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, changed := StripRedundantWorkspaceRootCD(tc.command, tc.root)
			if got != tc.want || changed != tc.wantStrip {
				t.Fatalf("got (%q, %v), want (%q, %v)", got, changed, tc.want, tc.wantStrip)
			}
		})
	}
}

// The abstraction has to hold for a sandbox that declares a non-default root.
func TestResolveWorkDirIsTheSingleRootResolution(t *testing.T) {
	if got := sandboxsession.ResolveWorkDir(""); got != WorkspaceRootDefault {
		t.Fatalf("unset workdir: got %q, want %q", got, WorkspaceRootDefault)
	}
	if got := sandboxsession.ResolveWorkDir("/srv/checkout/"); got != "/srv/checkout" {
		t.Fatalf("trailing separator: got %q, want %q", got, "/srv/checkout")
	}
	if got := sandboxsession.ResolveWorkDir("/"); got != "/" {
		t.Fatalf("root workdir: got %q, want %q", got, "/")
	}
}
