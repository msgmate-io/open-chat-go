package bots

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Regression pins for ci#1028: once a plan has been posted for a non-`[WIP]`
// issue, the development lead continues straight into implementation without
// waiting for a human approval message. A `[WIP]`-prefixed issue title still
// gates the implementation dispatch behind explicit human approval, and an
// explicit `require_confirmation` on the dispatch action always wins.

// TestCI1028PlanImplementationRequiresConfirmation pins the gate: no marker →
// autonomous continuation, `[WIP]` anywhere in the title (case-insensitive) →
// human approval, explicit require_confirmation overrides both, and an
// unresolved title falls back to requiring approval.
func TestCI1028PlanImplementationRequiresConfirmation(t *testing.T) {
	yes := true
	no := false
	cases := []struct {
		name   string
		action postActionPlanAutoImplement
		title  string
		want   bool
	}{
		{
			name:   "plain issue auto-continues",
			action: postActionPlanAutoImplement{Repo: "msgmate-io/open-chat-go-ci", Number: 1028},
			title:  "Allow autonomous development continuation after plan approval",
			want:   false,
		},
		{
			name:   "wip prefix gates the dispatch",
			action: postActionPlanAutoImplement{},
			title:  "[WIP] Allow autonomous development continuation",
			want:   true,
		},
		{
			name:   "wip marker anywhere in the title gates the dispatch",
			action: postActionPlanAutoImplement{},
			title:  "Allow autonomous continuation [WIP]",
			want:   true,
		},
		{
			name:   "wip marker match is case-insensitive",
			action: postActionPlanAutoImplement{},
			title:  "[wip] allow autonomous continuation",
			want:   true,
		},
		{
			name:   "explicit confirmation still gates a plain issue",
			action: postActionPlanAutoImplement{RequireConfirmation: &yes},
			title:  "Allow autonomous development continuation",
			want:   true,
		},
		{
			name:   "explicit auto-approval wins over the wip marker",
			action: postActionPlanAutoImplement{RequireConfirmation: &no},
			title:  "[WIP] allow autonomous continuation",
			want:   false,
		},
		{
			name:   "custom wip marker is honoured",
			action: postActionPlanAutoImplement{WipTitleMarker: "[BLOCKED]"},
			title:  "[BLOCKED] allow autonomous continuation",
			want:   true,
		},
		{
			name:   "custom wip marker replaces the default marker",
			action: postActionPlanAutoImplement{WipTitleMarker: "[BLOCKED]"},
			title:  "[WIP] allow autonomous continuation",
			want:   false,
		},
		{
			name:   "unresolved title falls back to requiring approval",
			action: postActionPlanAutoImplement{},
			title:  "",
			want:   true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := planImplementationRequiresConfirmation(tc.action, tc.title)
			if got != tc.want {
				t.Fatalf("planImplementationRequiresConfirmation(%q) = %v (%s), want %v", tc.title, got, reason, tc.want)
			}
			if strings.TrimSpace(reason) == "" {
				t.Fatalf("the gate must report why it decided %v", got)
			}
		})
	}
}

// TestCI1028TitleCarriesWipMarker pins the marker match helper (trimmed,
// case-insensitive, substring) and its degenerate inputs.
func TestCI1028TitleCarriesWipMarker(t *testing.T) {
	cases := []struct {
		title  string
		marker string
		want   bool
	}{
		{"[WIP] something", "[WIP]", true},
		{"something [wip]", "[WIP]", true},
		{"  [WIP]  padded  ", " [WIP] ", true},
		{"something", "[WIP]", false},
		{"", "[WIP]", false},
		{"[WIP] something", "", false},
	}
	for _, tc := range cases {
		if got := titleCarriesWipMarker(tc.title, tc.marker); got != tc.want {
			t.Fatalf("titleCarriesWipMarker(%q, %q) = %v, want %v", tc.title, tc.marker, got, tc.want)
		}
	}
}

// TestCI1028ResolvePlanImplementationConfirmationUsesTheActionTitle pins that a
// title supplied by the dispatching integration decides the gate without any
// GitHub API call (no token is configured in this test).
func TestCI1028ResolvePlanImplementationConfirmationUsesTheActionTitle(t *testing.T) {
	t.Setenv(postGithubTokenEnv, "")
	t.Setenv(planImplementationGitHubAPIBaseEnv, "")

	plain := postActionPlanAutoImplement{Repo: "msgmate-io/open-chat-go-ci", Number: 1028, Title: "Allow autonomous continuation"}
	if got := resolvePlanImplementationConfirmation(plain); got {
		t.Fatalf("a plain issue title must auto-continue, got require_confirmation=%v", got)
	}

	wip := postActionPlanAutoImplement{Repo: "msgmate-io/open-chat-go-ci", Number: 1028, Title: "[WIP] Allow autonomous continuation"}
	if got := resolvePlanImplementationConfirmation(wip); !got {
		t.Fatalf("a [WIP] issue title must stay gated, got require_confirmation=%v", got)
	}
}

// TestCI1028ResolvePlanImplementationConfirmationFetchesTheTitle pins the title
// resolution path: with no title on the action the dispatch fetches it from the
// GitHub API with the existing post token.
func TestCI1028ResolvePlanImplementationConfirmationFetchesTheTitle(t *testing.T) {
	title := "[WIP] gated fixture"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("title lookup method = %s, want GET", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/repos/msgmate-io/ci-1028-fixture/issues/1028") {
			t.Errorf("title lookup path = %s, want the fixture issue endpoint", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-token" {
			t.Errorf("title lookup authorization header = %q, want the post token", auth)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"title": title})
	}))
	defer server.Close()

	t.Setenv(postGithubTokenEnv, "test-token")
	t.Setenv(planImplementationGitHubAPIBaseEnv, server.URL)

	action := postActionPlanAutoImplement{Repo: "msgmate-io/ci-1028-fixture", Number: 1028}
	if got := resolvePlanImplementationConfirmation(action); !got {
		t.Fatalf("a fetched [WIP] title must stay gated, got require_confirmation=%v", got)
	}

	// A failed lookup keeps the safe behaviour: approval stays required.
	t.Setenv(planImplementationGitHubAPIBaseEnv, "http://127.0.0.1:1")
	if got := resolvePlanImplementationConfirmation(action); !got {
		t.Fatalf("an unresolved title must require confirmation, got require_confirmation=%v", got)
	}
}

// TestCI1028FetchIssueTitleToRejectsErrors pins the failure paths of the title
// lookup (bad status, empty title, unusable action).
func TestCI1028FetchIssueTitleToRejectsErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	if _, err := fetchIssueTitleTo(server.URL, "test-token", "msgmate-io/open-chat-go-ci", 1028); err == nil {
		t.Fatal("a non-2xx title lookup must fail")
	}
	if _, err := fetchIssueTitleTo("", "test-token", "", 0); err == nil {
		t.Fatal("a title lookup without repo/number must fail")
	}
	// Without a configured token the title cannot be resolved; the dispatch then
	// falls back to requiring confirmation (safe behaviour).
	t.Setenv(postGithubTokenEnv, "")
	if _, err := fetchPlanImplementationIssueTitle(postActionPlanAutoImplement{Repo: "o/n", Number: 1}); err == nil {
		t.Fatal("a title lookup without the post token must fail")
	}
	if strings.TrimSpace(planImplementationGitHubAPIBase()) == "" {
		t.Fatal("the API base must always resolve")
	}
}

// TestCI1028PromptStatesTheApprovalState pins the prompt delta: the dispatch
// prompt tells the implementation agent whether the plan is already approved
// (autonomous continuation) or still needs an explicit human approval ([WIP]).
func TestCI1028PromptStatesTheApprovalState(t *testing.T) {
	action := postActionPlanAutoImplement{Repo: "msgmate-io/open-chat-go-ci", Number: 1028, Bot: "msgmate-coder-repl-bot"}

	autonomous := buildPlanImplementationPrompt(action, "the plan", false)
	if !strings.Contains(autonomous, "continue straight into implementation") {
		t.Fatalf("an auto-approved prompt must say the plan may continue into implementation: %q", autonomous)
	}
	if strings.Contains(autonomous, "wait for that approval") {
		t.Fatalf("an auto-approved prompt must not ask for an approval message: %q", autonomous)
	}

	gated := buildPlanImplementationPrompt(action, "the plan", true)
	if !strings.Contains(gated, "explicit human approval") {
		t.Fatalf("a gated prompt must ask for explicit human approval: %q", gated)
	}
	if !strings.Contains(gated, "the plan") {
		t.Fatalf("the gated prompt must still carry the plan verbatim: %q", gated)
	}
}
