package gittooling

import (
	"strings"
	"testing"
)

// TestValidatePushTargetDeniesProtectedBranch pins the core ci#1138 rule: a push
// whose destination is main, master or dev is refused, and the error names the
// destination so the model can pick another target.
func TestValidatePushTargetDeniesProtectedBranch(t *testing.T) {
	cases := []struct {
		name    string
		refspec string
		wantSub string
	}{
		{name: "main short", refspec: "feature:main", wantSub: "protected branch \"main\""},
		{name: "main full ref", refspec: "feature:refs/heads/main", wantSub: "protected branch \"main\""},
		{name: "dev short", refspec: "feature:dev", wantSub: "protected branch \"dev\""},
		{name: "master full ref", refspec: "feature:refs/heads/master", wantSub: "protected branch \"master\""},
		{name: "HEAD to main", refspec: "HEAD:main", wantSub: "protected branch \"main\""},
		{name: "bare branch to main", refspec: "main", wantSub: "protected branch \"main\""},
		{name: "force plus refspec", refspec: "+refs/heads/main", wantSub: "force-push"},
		{name: "force flag", refspec: "--force feature:feature", wantSub: "force-push"},
		{name: "force short flag", refspec: "-f feature:feature", wantSub: "force-push"},
		{name: "force with lease", refspec: "--force-with-lease feature:feature", wantSub: "force-push"},
		{name: "empty refspec", refspec: "   ", wantSub: "explicit refspec"},
		{name: "no destination", refspec: "feature:", wantSub: "no destination branch"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := ValidatePushTarget(testCase.refspec, nil)
			if err == nil {
				t.Fatalf("ValidatePushTarget(%q) must be refused", testCase.refspec)
			}
			if !strings.Contains(err.Error(), testCase.wantSub) {
				t.Fatalf("ValidatePushTarget(%q) error %q must contain %q", testCase.refspec, err.Error(), testCase.wantSub)
			}
		})
	}
}

// TestValidatePushTargetAllowsFeatureBranch pins the positive case, and that a
// narrowed protected list (an integration bot may only narrow) is honoured.
func TestValidatePushTargetAllowsFeatureBranch(t *testing.T) {
	allowed := []string{
		"feature:feature",
		"feat/ci-1138:feat/ci-1138",
		"fix/x:chore/fix-x",
		"feature:refs/heads/feature",
	}
	for _, refspec := range allowed {
		if err := ValidatePushTarget(refspec, nil); err != nil {
			t.Fatalf("ValidatePushTarget(%q) must be allowed: %v", refspec, err)
		}
	}
	// Narrowed policy: only main stays protected, so dev becomes pushable.
	if err := ValidatePushTarget("feature:dev", []string{"main"}); err != nil {
		t.Fatalf("narrowed policy must allow dev: %v", err)
	}
	if err := ValidatePushTarget("feature:main", []string{"main"}); err == nil {
		t.Fatal("narrowed policy must still deny main")
	}
}

// TestValidatePushHeadRefusesDetachedAndProtected pins the HEAD half of the
// guard: a detached HEAD or a protected HEAD is refused even when the refspec
// destination itself is fine.
func TestValidatePushHeadRefusesDetachedAndProtected(t *testing.T) {
	for _, head := range []string{"", "HEAD", "HEAD~1", "HEAD^2"} {
		if err := ValidatePushHead(head, nil); err == nil {
			t.Fatalf("ValidatePushHead(%q) must be refused (detached)", head)
		}
	}
	for _, head := range []string{"main", "master", "dev", "refs/heads/main"} {
		if err := ValidatePushHead(head, nil); err == nil {
			t.Fatalf("ValidatePushHead(%q) must be refused (protected)", head)
		}
	}
	if err := ValidatePushHead("feat/ci-1138", nil); err != nil {
		t.Fatalf("feature HEAD must be allowed: %v", err)
	}
}

// TestValidatePushNamesFirstViolation pins the ordering of ValidatePush: the
// destination is checked before the HEAD, so pushing from main to main reports
// the destination.
func TestValidatePushNamesFirstViolation(t *testing.T) {
	err := ValidatePush("main:main", "main", nil)
	if err == nil {
		t.Fatal("ValidatePush(main:main) must be refused")
	}
	if !strings.Contains(err.Error(), "protected branch \"main\"") {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := ValidatePush("feature:feature", "main", nil); err == nil {
		t.Fatal("ValidatePush from a protected HEAD must be refused")
	}
	if err := ValidatePush("feature:feature", "feat/ci-1138", nil); err != nil {
		t.Fatalf("feature -> feature from a feature HEAD must be allowed: %v", err)
	}
}

// TestProtectedBranchesDefaults pins the published default list, which is the
// same list the bot bootstrap policy mirrors into default_shared_config.
func TestProtectedBranchesDefaults(t *testing.T) {
	want := []string{"main", "master", "dev"}
	got := ProtectedBranches()
	if len(got) != len(want) {
		t.Fatalf("ProtectedBranches() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ProtectedBranches() = %v, want %v", got, want)
		}
	}
	if !IsProtectedBranch("refs/heads/main", nil) || IsProtectedBranch("feature", nil) {
		t.Fatal("IsProtectedBranch must match refs/heads/main and reject a feature branch")
	}
	if len(DefaultPushDenylist()) == 0 {
		t.Fatal("DefaultPushDenylist must not be empty")
	}
}

// TestGitToolDescriptionCoversPushAction extends the description pin to the
// mutating push action: the refusal rules must be documented for the model, not
// only enforced in code.
func TestGitToolDescriptionCoversPushAction(t *testing.T) {
	doc := GitActionDoc(GitActionPush)
	if doc == "" {
		t.Fatalf("action %q has no description bullet", GitActionPush)
	}
	for _, pin := range []string{
		"protected branch",
		"main, master, dev",
		"force push",
		"detached",
	} {
		if !strings.Contains(doc, pin) {
			t.Fatalf("push description is missing %q: %q", pin, doc)
		}
	}
}
