package tools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// stubGitHubPR is a tiny stateful GitHub stub: it serves GET on the pull
// resource and accepts the PATCH/label writes, mutating the served state so the
// tool's read-back sees the effect.
type stubGitHubPR struct {
	mu        sync.Mutex
	pull      map[string]interface{}
	patchSeen map[string]interface{}
	addLabels []string
	removed   []string
	// ignoreWrites makes the stub pretend the write succeeded while never
	// applying it - the "verified:false" read-back path.
	ignoreWrites bool
}

func newStubGitHubPR() *stubGitHubPR {
	return &stubGitHubPR{
		pull: map[string]interface{}{
			"title":    "old title",
			"body":     "old body",
			"state":    "open",
			"html_url": "https://example.test/pull/42",
			"base":     map[string]interface{}{"ref": "dev"},
			"labels":   []interface{}{map[string]interface{}{"name": "keep"}},
		},
	}
}

func (s *stubGitHubPR) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"bad credentials"}`))
			return
		}
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(s.pull)
		case http.MethodPatch:
			decoded := map[string]interface{}{}
			_ = json.NewDecoder(r.Body).Decode(&decoded)
			s.patchSeen = decoded
			if !s.ignoreWrites {
				for key, value := range decoded {
					if key == "base" {
						s.pull["base"] = map[string]interface{}{"ref": value}
						continue
					}
					s.pull[key] = value
				}
			}
			_ = json.NewEncoder(w).Encode(s.pull)
		case http.MethodPost:
			decoded := map[string]interface{}{}
			_ = json.NewDecoder(r.Body).Decode(&decoded)
			labels, _ := decoded["labels"].([]interface{})
			for _, label := range labels {
				name, _ := label.(string)
				s.addLabels = append(s.addLabels, name)
				if !s.ignoreWrites {
					s.addLabel(name)
				}
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
		case http.MethodDelete:
			name := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			s.removed = append(s.removed, name)
			if !s.ignoreWrites {
				s.removeLabel(name)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
}

func (s *stubGitHubPR) addLabel(name string) {
	labels, _ := s.pull["labels"].([]interface{})
	for _, label := range labels {
		if existing, ok := label.(map[string]interface{}); ok && existing["name"] == name {
			return
		}
	}
	s.pull["labels"] = append(labels, map[string]interface{}{"name": name})
}

func (s *stubGitHubPR) removeLabel(name string) {
	labels, _ := s.pull["labels"].([]interface{})
	kept := make([]interface{}, 0, len(labels))
	for _, label := range labels {
		if existing, ok := label.(map[string]interface{}); ok && existing["name"] == name {
			continue
		}
		kept = append(kept, label)
	}
	s.pull["labels"] = kept
}

func newStubPREditServer(t *testing.T, stub *stubGitHubPR) string {
	t.Helper()
	server := httptest.NewServer(stub.handler())
	t.Cleanup(server.Close)
	return server.URL
}

// TestGitHubPREditVerifiesTitleAndLabels is the happy path: the edit is applied
// and the read-back confirms it, so the report is verified:true.
func TestGitHubPREditVerifiesTitleAndLabels(t *testing.T) {
	stub := newStubGitHubPR()
	base := newStubPREditServer(t, stub)

	result, err := runGitHubPREdit(nil, base, "test-token", GitHubPREditInput{
		Repo:         "msgmate-io/open-chat-go",
		PullNumber:   42,
		Title:        "new title",
		AddLabels:    []string{"test", "test"},
		RemoveLabels: []string{"keep"},
	})
	if err != nil {
		t.Fatalf("runGitHubPREdit returned an error: %v", err)
	}
	if result["verified"] != true {
		t.Fatalf("expected verified:true, got %v (problems: %v)", result["verified"], result["verification_problems"])
	}
	if stub.patchSeen["title"] != "new title" {
		t.Fatalf("expected the PATCH to carry the new title, got %v", stub.patchSeen)
	}
	if len(stub.addLabels) != 1 || stub.addLabels[0] != "test" {
		t.Fatalf("expected the de-duplicated label add, got %v", stub.addLabels)
	}
	if result["weblink"] != "https://example.test/pull/42" {
		t.Fatalf("expected the pull weblink in the report, got %v", result["weblink"])
	}
}

// TestGitHubPREditReportsUnverifiedReadBack covers the ci#514 criterion 4
// failure path: the write "succeeds" but the read-back does not show it, so the
// tool must report verified:false with the concrete mismatch instead of
// claiming the edit landed.
func TestGitHubPREditReportsUnverifiedReadBack(t *testing.T) {
	stub := newStubGitHubPR()
	stub.ignoreWrites = true
	base := newStubPREditServer(t, stub)

	result, err := runGitHubPREdit(nil, base, "test-token", GitHubPREditInput{
		Repo:       "msgmate-io/open-chat-go",
		PullNumber: 42,
		Title:      "new title",
	})
	if err != nil {
		t.Fatalf("a drifted read-back must not fail the call: %v", err)
	}
	if result["verified"] != false {
		t.Fatalf("expected verified:false, got %v", result["verified"])
	}
	problems, _ := result["verification_problems"].([]string)
	if len(problems) == 0 || !strings.Contains(problems[0], "new title") {
		t.Fatalf("expected the title mismatch in the problems, got %v", result["verification_problems"])
	}
	if note, _ := result["note"].(string); !strings.Contains(note, "do not report the change as done") {
		t.Fatalf("expected the verified:false note, got %q", note)
	}
}

// TestGitHubPREditRefusesRetargetOffTrunk is the trunk guard: a base away from
// `dev` needs an explicit opt-in.
func TestGitHubPREditRefusesRetargetOffTrunk(t *testing.T) {
	stub := newStubGitHubPR()
	base := newStubPREditServer(t, stub)

	_, err := runGitHubPREdit(nil, base, "test-token", GitHubPREditInput{
		Repo:       "msgmate-io/open-chat-go",
		PullNumber: 42,
		Base:       "main",
	})
	if err == nil || !strings.Contains(err.Error(), "allow_base_retarget") {
		t.Fatalf("expected the trunk guard to refuse a retarget off dev, got %v", err)
	}

	// The explicit opt-in goes through.
	result, err := runGitHubPREdit(nil, base, "test-token", GitHubPREditInput{
		Repo:              "msgmate-io/open-chat-go",
		PullNumber:        42,
		Base:              "main",
		AllowBaseRetarget: true,
	})
	if err != nil {
		t.Fatalf("allow_base_retarget must permit the retarget: %v", err)
	}
	if result["verified"] != true {
		t.Fatalf("expected verified:true for the opted-in retarget, got %v", result["verified"])
	}
}

// TestGitHubPREditSkipsRedundantBase pins that a retarget onto the branch the
// PR already targets is not re-applied (no pointless write).
func TestGitHubPREditSkipsRedundantBase(t *testing.T) {
	stub := newStubGitHubPR()
	base := newStubPREditServer(t, stub)

	_, err := runGitHubPREdit(nil, base, "test-token", GitHubPREditInput{
		Repo:              "msgmate-io/open-chat-go",
		PullNumber:        42,
		Base:              "dev",
		AllowBaseRetarget: false,
	})
	if err == nil || !strings.Contains(err.Error(), "nothing to change") {
		t.Fatalf("expected the redundant base to leave nothing to change, got %v", err)
	}
}

// TestGitHubPREditRejectsEmptyEdit pins the input validation.
func TestGitHubPREditRejectsEmptyEdit(t *testing.T) {
	cases := []struct {
		name string
		in   GitHubPREditInput
		want string
	}{
		{name: "bad repo", in: GitHubPREditInput{Repo: "no-slash", PullNumber: 1, Title: "x"}, want: "owner/name"},
		{name: "bad number", in: GitHubPREditInput{Repo: "a/b", PullNumber: 0, Title: "x"}, want: "pull_number"},
		{name: "bad state", in: GitHubPREditInput{Repo: "a/b", PullNumber: 1, State: "merged"}, want: "state must be"},
		{name: "no change", in: GitHubPREditInput{Repo: "a/b", PullNumber: 1}, want: "nothing to change"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := runGitHubPREdit(nil, "http://127.0.0.1:1", "test-token", tc.in)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q in the error, got %v", tc.want, err)
			}
		})
	}
}

// TestGitHubPREditRunFunctionIsRegistered checks the definition wiring end to
// end: a JSON payload parses into the input type and the token comes from init.
func TestGitHubPREditRunFunctionIsRegistered(t *testing.T) {
	stub := newStubGitHubPR()
	base := newStubPREditServer(t, stub)

	if GitHubPREditToolDef.Name != "github_pr_edit" {
		t.Fatalf("unexpected tool name %q", GitHubPREditToolDef.Name)
	}
	if !GitHubPREditToolDef.RequiresInit {
		t.Fatal("github_pr_edit must require init data to reach the token")
	}

	payload := `{"repo":"msgmate-io/open-chat-go","pull_number":42,"body":"new body"}`
	var in GitHubPREditInput
	if err := json.Unmarshal([]byte(payload), &in); err != nil {
		t.Fatalf("the tool input type does not accept the documented payload: %v", err)
	}
	out, err := GitHubPREditToolDef.RunFunction(in, map[string]interface{}{
		"github_token":    "test-token",
		"github_api_base": base,
	})
	if err != nil {
		t.Fatalf("RunFunction returned an error: %v", err)
	}
	var report map[string]interface{}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("the tool output is not JSON: %v", err)
	}
	if report["verified"] != true {
		t.Fatalf("expected a verified report, got %v", out)
	}
}

// TestGitHubPREditNeedsToken pins the missing-token error and that the token is
// never echoed back.
func TestGitHubPREditNeedsToken(t *testing.T) {
	for _, name := range githubTokenEnvNames {
		t.Setenv(name, "")
	}
	_, err := GitHubPREditToolDef.RunFunction(GitHubPREditInput{
		Repo:       "msgmate-io/open-chat-go",
		PullNumber: 42,
		Title:      "x",
	}, map[string]interface{}{})
	if err == nil || !strings.Contains(err.Error(), "needs a GitHub token") {
		t.Fatalf("expected the missing-token error, got %v", err)
	}
}

// TestNormalizeGitHubLabels pins the trim/dedupe/sort behaviour.
func TestNormalizeGitHubLabels(t *testing.T) {
	got := normalizeGitHubLabels([]string{" test ", "", "Test", "ci"})
	want := []string{"ci", "test"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("expected %v, got %v", want, got)
	}
}
