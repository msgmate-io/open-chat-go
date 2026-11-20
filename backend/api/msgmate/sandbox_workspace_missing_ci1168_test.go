package msgmate

// ci#1168: regression guard for the live canary shape of the missing
// sandbox-workspace failure.
//
// The classifier fixtures carried only the older `sh: cd: can't cd to ...`
// form. The live canary (and the msgmate_coder tool API) emits a *wrapped*
// form with an extra `line 0:` segment:
//
//	coder tool api failed (400): failed to list .: sh: cd: line 0: can't cd to
//	/workspace/coder-<uuid>: No such file or directory
//
// Every branch of the workspace-missing handling (non-transient
// classification, path extraction, model-facing placeholder) hung off a
// marker match that this shape must satisfy. If a refactor narrows the
// matcher again, this test fails instead of the branch silently dropping out
// of the live path the way the ci#1168 producer did.

import (
	"errors"
	"strings"
	"testing"
)

const ci1168WorkspaceUUID = "9cc22aa8-d14e-4ac8-acfb"

func ci1168WorkspaceMissingError() error {
	return errors.New("coder tool api failed (400): failed to list .: sh: cd: line 0: can't cd to /workspace/coder-" +
		ci1168WorkspaceUUID + ": No such file or directory")
}

// The live `line 0:` form is classified as a missing sandbox workspace root,
// with the per-chat path extracted.
func TestSandboxWorkspaceMissingClassifiesLiveToolAPIForm(t *testing.T) {
	err := ci1168WorkspaceMissingError()

	path, missing := isSandboxWorkspaceMissing(err)
	if !missing {
		t.Fatalf("expected the live tool-api form to classify as a missing workspace root: %v", err)
	}
	wantPath := "/workspace/coder-" + ci1168WorkspaceUUID
	if path != wantPath {
		t.Fatalf("expected extracted path %q, got %q", wantPath, path)
	}

	// Deterministic: the path is chat-derived and stable, so a retry re-enters
	// the same absent directory. It must never be retried as transient.
	if isTransientToolFailure(err) {
		t.Fatalf("a missing sandbox workspace root must not be transient: %v", err)
	}

	// The path must come from the error, not from the placeholder fallback.
	if fallback := sandboxWorkspacePathFromError("no path here"); fallback != "" {
		t.Fatalf("expected no path for an unrelated error, got %q", fallback)
	}
	if got := sandboxWorkspacePathFromError(err.Error()); got != wantPath {
		t.Fatalf("expected sandboxWorkspacePathFromError to extract %q, got %q", wantPath, got)
	}
}

// The model-facing placeholder is built for the live form and names the
// extracted path instead of the generic `<chat_uuid>` placeholder.
func TestSandboxWorkspaceMissingPlaceholderLiveToolAPIForm(t *testing.T) {
	err := ci1168WorkspaceMissingError()
	path, missing := isSandboxWorkspaceMissing(err)
	if !missing {
		t.Fatalf("expected the live tool-api form to classify as a missing workspace root: %v", err)
	}

	got := buildSandboxWorkspaceMissingToolErrorPlaceholder("coder_list", path, err)
	wantPath := "/workspace/coder-" + ci1168WorkspaceUUID
	if !strings.Contains(got, wantPath) {
		t.Fatalf("expected the placeholder to name the extracted path %q:\n%s", wantPath, got)
	}
	if strings.Contains(got, "<chat_uuid>") {
		t.Fatalf("expected the placeholder to use the extracted path, not its fallback:\n%s", got)
	}
	if !strings.Contains(got, "coder_list") {
		t.Fatalf("expected the placeholder to name the failing tool:\n%s", got)
	}
	// The remedies that can work, and the budget statement, are the point of
	// the placeholder: a bare error invites the one retry that cannot work.
	for _, want := range []string{"does not exist", "not retried", "re-select the workspace"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected the placeholder to contain %q:\n%s", want, got)
		}
	}
}
