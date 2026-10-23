package nativecoder

import "testing"

// TestNormalizeRepo is the single canonical test for the shared repo
// canonicaliser (repo.go). It used to exist twice — once in tags_test.go
// (ci#650) and once in devlead_test.go (ci#608) — because each consumer had
// brought its own copy of NormalizeRepo. Both copies are merged here.
func TestNormalizeRepo(t *testing.T) {
	cases := map[string]string{
		// already canonical
		"msgmate-io/open-chat-go": "msgmate-io/open-chat-go",
		// https clone URL, with and without the .git suffix
		"https://github.com/msgmate-io/open-chat-go.git": "msgmate-io/open-chat-go",
		"https://gitlab.com/group/sub/repo":              "sub/repo",
		// scp-style ssh remote
		"git@github.com:msgmate-io/open-chat-go.git": "msgmate-io/open-chat-go",
		// surrounding whitespace and mixed case
		"  Msgmate-IO/Open-Chat-Go.git ": "msgmate-io/open-chat-go",
		// unusable values resolve to "" so callers can validate too
		"   ":                       "",
		"open-chat-go":              "",
		"":                          "",
		"git@github.com:only-owner": "",
		"https://github.com/":       "",
	}
	for input, want := range cases {
		if got := NormalizeRepo(input); got != want {
			t.Errorf("NormalizeRepo(%q) = %q, want %q", input, got, want)
		}
	}
}

// TestNormalizeBranch pins the branch canonicalisation the lead router and the
// fix-session key depend on.
func TestNormalizeBranch(t *testing.T) {
	cases := map[string]string{
		"main":               "main",
		"  refs/heads/Main ": "main",
		"refs/heads/feat/x":  "feat/x",
		"":                   "",
	}
	for input, want := range cases {
		if got := NormalizeBranch(input); got != want {
			t.Errorf("NormalizeBranch(%q) = %q, want %q", input, got, want)
		}
	}
}
