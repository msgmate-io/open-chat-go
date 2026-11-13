package githubpr

// Tests for the typed github_pr_update_branch route (ci#1037). The HTTP layer
// is replaced with an httptest stub so the suite needs no network and pins the
// two legitimate 422 answers (already up to date, merge conflict) as structured
// states instead of raw failures, plus the verification failure path where the
// head SHA did not actually move.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubGitHub wires a Client onto a test server. The handler receives the stub
// and may answer however the case needs.
type stubGitHub struct {
	t       *testing.T
	client  *Client
	server  *httptest.Server
	calls   []stubCall
	handler func(w http.ResponseWriter, r *http.Request, call stubCall)
}

type stubCall struct {
	Method string
	Path   string
	Body   string
}

func newStubGitHub(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, call stubCall)) *stubGitHub {
	t.Helper()
	stub := &stubGitHub{t: t, handler: handler}
	stub.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		call := stubCall{Method: r.Method, Path: r.URL.Path, Body: string(raw)}
		stub.calls = append(stub.calls, call)
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("request to %s %s carried Authorization %q, want the bearer token", call.Method, call.Path, got)
		}
		stub.handler(w, r, call)
	}))
	t.Cleanup(stub.server.Close)
	stub.client = &Client{
		HTTPClient:  stub.server.Client(),
		BaseURL:     stub.server.URL,
		Token:       "test-token",
		DefaultRepo: "msgmate-io/open-chat-go",
	}
	return stub
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, payload string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write([]byte(payload)); err != nil {
		t.Fatalf("write stub body: %v", err)
	}
}

func pullPayload(headSHA, mergeableState string) string {
	return `{"number":1037,"mergeable":true,"mergeable_state":"` + mergeableState + `","head":{"sha":"` + headSHA + `"}}`
}

// TestUpdateBranchSuccessMovesHead is the happy path: GitHub accepts the update
// (202), the re-read shows a new head SHA and a CLEAN state, so the answer is
// verified and reports the re-read mergeable fields.
func TestUpdateBranchSuccessMovesHead(t *testing.T) {
	stub := newStubGitHub(t, func(w http.ResponseWriter, r *http.Request, call stubCall) {
		switch {
		case call.Method == http.MethodPut && call.Path == "/repos/msgmate-io/open-chat-go/pulls/1037/update-branch":
			writeJSON(t, w, http.StatusAccepted, `{"message":"Updating pull request branch."}`)
		case call.Method == http.MethodGet && call.Path == "/repos/msgmate-io/open-chat-go/pulls/1037":
			writeJSON(t, w, http.StatusOK, pullPayload("newsha999", "clean"))
		default:
			t.Errorf("unexpected request %s %s", call.Method, call.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})

	result, err := stub.client.UpdateBranch(context.Background(), UpdateBranchRequest{
		Number:          1037,
		ExpectedHeadSHA: "oldsha111",
	})
	if err != nil {
		t.Fatalf("UpdateBranch returned error: %v", err)
	}
	if result.State != StateUpdated {
		t.Fatalf("state = %q, want %q", result.State, StateUpdated)
	}
	if !result.Verified {
		t.Fatalf("verified = false (%s), want true", result.VerificationError)
	}
	if result.HeadSHA != "newsha999" {
		t.Errorf("head sha = %q, want newsha999", result.HeadSHA)
	}
	if result.PreviousHeadSHA != "oldsha111" {
		t.Errorf("previous head sha = %q, want oldsha111", result.PreviousHeadSHA)
	}
	if result.MergeStateStatus != "CLEAN" {
		t.Errorf("merge_state_status = %q, want CLEAN", result.MergeStateStatus)
	}
	if result.Mergeable == nil || !*result.Mergeable {
		t.Errorf("mergeable = %v, want true", result.Mergeable)
	}
	if result.Repo != "msgmate-io/open-chat-go" || result.Number != 1037 {
		t.Errorf("result identity = %s#%d, want msgmate-io/open-chat-go#1037", result.Repo, result.Number)
	}
	if result.Tool != UpdateBranchToolName {
		t.Errorf("tool = %q, want %q", result.Tool, UpdateBranchToolName)
	}
	if len(stub.calls) != 2 {
		t.Fatalf("made %d calls, want 2 (PUT then GET)", len(stub.calls))
	}
	var body map[string]string
	if err := json.Unmarshal([]byte(stub.calls[0].Body), &body); err != nil {
		t.Fatalf("PUT body is not JSON: %v (%s)", err, stub.calls[0].Body)
	}
	if body["expected_head_sha"] != "oldsha111" {
		t.Errorf("PUT body expected_head_sha = %q, want oldsha111", body["expected_head_sha"])
	}
}

// TestUpdateBranchAlreadyUpToDate covers the first legitimate 422: the branch is
// already fresh, which is a normal answer, not a failure.
func TestUpdateBranchAlreadyUpToDate(t *testing.T) {
	stub := newStubGitHub(t, func(w http.ResponseWriter, r *http.Request, call stubCall) {
		switch call.Method {
		case http.MethodPut:
			writeJSON(t, w, http.StatusUnprocessableEntity, `{"message":"Branch is already up to date."}`)
		case http.MethodGet:
			writeJSON(t, w, http.StatusOK, pullPayload("samehead222", "clean"))
		default:
			t.Errorf("unexpected request %s %s", call.Method, call.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})

	result, err := stub.client.UpdateBranch(context.Background(), UpdateBranchRequest{
		Repo:            "msgmate-io/open-chat-go",
		Number:          1037,
		ExpectedHeadSHA: "samehead222",
	})
	if err != nil {
		t.Fatalf("already-up-to-date must not be an error, got: %v", err)
	}
	if result.State != StateAlreadyUpToDate {
		t.Fatalf("state = %q, want %q", result.State, StateAlreadyUpToDate)
	}
	if !result.Verified {
		t.Fatalf("verified = false (%s), want true", result.VerificationError)
	}
	if result.Message != "Branch is already up to date." {
		t.Errorf("message = %q, want GitHub's own text", result.Message)
	}
	if result.HeadSHA != "samehead222" {
		t.Errorf("head sha = %q, want samehead222", result.HeadSHA)
	}
}

// TestUpdateBranchMergeConflict covers the second legitimate 422: base and head
// conflict, so the server cannot update the branch. Still structured, still not
// an error, but explicitly unverified.
func TestUpdateBranchMergeConflict(t *testing.T) {
	stub := newStubGitHub(t, func(w http.ResponseWriter, r *http.Request, call stubCall) {
		switch call.Method {
		case http.MethodPut:
			writeJSON(t, w, http.StatusUnprocessableEntity, `{"message":"merge conflict between base and head"}`)
		case http.MethodGet:
			writeJSON(t, w, http.StatusOK, pullPayload("stalehead333", "dirty"))
		default:
			t.Errorf("unexpected request %s %s", call.Method, call.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})

	result, err := stub.client.UpdateBranch(context.Background(), UpdateBranchRequest{Number: 1037})
	if err != nil {
		t.Fatalf("merge conflict must be a structured state, not an error: %v", err)
	}
	if result.State != StateMergeConflict {
		t.Fatalf("state = %q, want %q", result.State, StateMergeConflict)
	}
	if result.Verified {
		t.Errorf("verified = true for a merge conflict, want false")
	}
	if !strings.Contains(result.VerificationError, "rebase") {
		t.Errorf("verification_error = %q, want it to mention the needed rebase", result.VerificationError)
	}
	if result.MergeStateStatus != "DIRTY" {
		t.Errorf("merge_state_status = %q, want DIRTY", result.MergeStateStatus)
	}
}

// TestUpdateBranchVerificationFailure is the case the other write tools also
// guard: GitHub said 202, but the re-read shows the head did not move, so the
// answer must say so instead of claiming success.
func TestUpdateBranchVerificationFailure(t *testing.T) {
	stub := newStubGitHub(t, func(w http.ResponseWriter, r *http.Request, call stubCall) {
		switch call.Method {
		case http.MethodPut:
			writeJSON(t, w, http.StatusAccepted, `{"message":"Updating pull request branch."}`)
		case http.MethodGet:
			// Head still the old commit and still behind the base.
			writeJSON(t, w, http.StatusOK, pullPayload("oldsha444", "behind"))
		default:
			t.Errorf("unexpected request %s %s", call.Method, call.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})

	result, err := stub.client.UpdateBranch(context.Background(), UpdateBranchRequest{
		Number:          1037,
		ExpectedHeadSHA: "oldsha444",
	})
	if err != nil {
		t.Fatalf("UpdateBranch returned error: %v", err)
	}
	if result.State != StateUpdated {
		t.Fatalf("state = %q, want %q", result.State, StateUpdated)
	}
	if result.Verified {
		t.Fatalf("verified = true although the head did not move")
	}
	if !strings.Contains(result.VerificationError, "did not move") {
		t.Errorf("verification_error = %q, want it to state the head did not move", result.VerificationError)
	}
	if result.MergeStateStatus != "BEHIND" {
		t.Errorf("merge_state_status = %q, want BEHIND", result.MergeStateStatus)
	}
}

// TestUpdateBranchUnreadableAfterUpdate: the update was accepted but the re-read
// failed, so verification must fail loudly with the read error.
func TestUpdateBranchUnreadableAfterUpdate(t *testing.T) {
	stub := newStubGitHub(t, func(w http.ResponseWriter, r *http.Request, call stubCall) {
		switch call.Method {
		case http.MethodPut:
			writeJSON(t, w, http.StatusAccepted, `{"message":"Updating pull request branch."}`)
		case http.MethodGet:
			writeJSON(t, w, http.StatusInternalServerError, `{"message":"boom"}`)
		default:
			t.Errorf("unexpected request %s %s", call.Method, call.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})

	result, err := stub.client.UpdateBranch(context.Background(), UpdateBranchRequest{Number: 1037})
	if err != nil {
		t.Fatalf("UpdateBranch returned error: %v", err)
	}
	if result.Verified {
		t.Fatalf("verified = true although the pull request could not be re-read")
	}
	if !strings.Contains(result.VerificationError, "could not be re-read") {
		t.Errorf("verification_error = %q, want it to report the failed re-read", result.VerificationError)
	}
}

// TestUpdateBranchServerErrorIsAnError keeps real failures as failures: a 5xx or
// an unexpected 422 (for example a stale expected_head_sha) must surface an
// error the caller can act on.
func TestUpdateBranchServerErrorIsAnError(t *testing.T) {
	stub := newStubGitHub(t, func(w http.ResponseWriter, r *http.Request, call stubCall) {
		if call.Method == http.MethodPut {
			writeJSON(t, w, http.StatusUnprocessableEntity, `{"message":"Head branch was modified. Review and try the merge again."}`)
			return
		}
		writeJSON(t, w, http.StatusOK, pullPayload("otherhead555", "behind"))
	})

	result, err := stub.client.UpdateBranch(context.Background(), UpdateBranchRequest{
		Number:          1037,
		ExpectedHeadSHA: "stalehead",
	})
	if err == nil {
		t.Fatalf("expected an error for an unexpected 422, got state %q", result.State)
	}
	if result.State != StateFailed {
		t.Errorf("state = %q, want %q", result.State, StateFailed)
	}
	if !strings.Contains(err.Error(), "422") {
		t.Errorf("error = %v, want it to carry the status", err)
	}
}

// TestRunUpdateBranchToolParsesTypedArguments pins the typed entry point: the
// JSON arguments carry repo/number/expected_head_sha and a missing number is
// rejected before any HTTP call.
func TestRunUpdateBranchToolParsesTypedArguments(t *testing.T) {
	stub := newStubGitHub(t, func(w http.ResponseWriter, r *http.Request, call stubCall) {
		switch call.Method {
		case http.MethodPut:
			if !strings.Contains(call.Path, "/repos/other/repo/pulls/42/update-branch") {
				t.Errorf("PUT path = %q, want the repo from the arguments", call.Path)
			}
			writeJSON(t, w, http.StatusAccepted, `{"message":"Updating pull request branch."}`)
		case http.MethodGet:
			writeJSON(t, w, http.StatusOK, pullPayload("fresh666", "clean"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	result, err := stub.client.RunUpdateBranchTool(context.Background(), []byte(`{"repo":"other/repo","number":42,"expected_head_sha":"oldsix"}`))
	if err != nil {
		t.Fatalf("RunUpdateBranchTool returned error: %v", err)
	}
	if !result.Verified || result.State != StateUpdated {
		t.Fatalf("result = %+v, want a verified update", result)
	}

	callsBefore := len(stub.calls)
	if _, err := stub.client.RunUpdateBranchTool(context.Background(), []byte(`{"repo":"other/repo"}`)); err == nil {
		t.Fatalf("expected an error for missing number")
	}
	if len(stub.calls) != callsBefore {
		t.Errorf("a request was sent for invalid arguments (%d -> %d)", callsBefore, len(stub.calls))
	}
}

// TestUpdateBranchRequiresRepo: without a repo argument and without a default
// there is nothing to call, so it fails fast without HTTP.
func TestUpdateBranchRequiresRepo(t *testing.T) {
	client := &Client{HTTPClient: http.DefaultClient, BaseURL: "https://example.invalid"}
	t.Setenv("OPEN_CHAT_GITHUB_REPO", "")
	if _, err := client.UpdateBranch(context.Background(), UpdateBranchRequest{Number: 1}); err == nil {
		t.Fatalf("expected an error when no repo is available")
	}
}

// TestClassifyUnprocessable pins the 422 mapping, including that an
// already-up-to-date message wins over the looser conflict match.
func TestClassifyUnprocessable(t *testing.T) {
	cases := []struct {
		message string
		want    UpdateBranchState
	}{
		{"Branch is already up to date.", StateAlreadyUpToDate},
		{"The branch is already up-to-date with the base branch", StateAlreadyUpToDate},
		{"merge conflict between base and head", StateMergeConflict},
		{"Pull request is not mergeable: conflicts detected", StateMergeConflict},
		{"Head branch was modified. Review and try the merge again.", StateFailed},
		{"", StateFailed},
	}
	for _, tc := range cases {
		if got := classifyUnprocessable(tc.message); got != tc.want {
			t.Errorf("classifyUnprocessable(%q) = %q, want %q", tc.message, got, tc.want)
		}
	}
}

// TestUpdateBranchToolSpec pins the typed surface: the tool name, the required
// number and the optional repo/expected_head_sha.
func TestUpdateBranchToolSpec(t *testing.T) {
	if UpdateBranchToolSpec.Name != "github_pr_update_branch" {
		t.Fatalf("tool name = %q, want github_pr_update_branch", UpdateBranchToolSpec.Name)
	}
	if UpdateBranchToolSpec.Name != UpdateBranchToolName {
		t.Errorf("spec name %q != %q", UpdateBranchToolSpec.Name, UpdateBranchToolName)
	}
	required := map[string]bool{}
	seen := map[string]bool{}
	for _, param := range UpdateBranchToolSpec.Parameters {
		seen[param.Name] = true
		if param.Required {
			required[param.Name] = true
		}
	}
	if !seen["repo"] || !seen["number"] || !seen["expected_head_sha"] {
		t.Fatalf("parameters = %+v, want repo, number, expected_head_sha", UpdateBranchToolSpec.Parameters)
	}
	if !required["number"] {
		t.Errorf("number must be required")
	}
	if required["repo"] || required["expected_head_sha"] {
		t.Errorf("repo and expected_head_sha must stay optional")
	}
	if !strings.Contains(UpdateBranchToolSpec.Description, "update-branch") {
		t.Errorf("description should name the REST endpoint it routes to")
	}
}

// TestNormalizeMergeStateStatus pins the REST -> mergeStateStatus normalization
// the answer carries.
func TestNormalizeMergeStateStatus(t *testing.T) {
	yes := true
	no := false
	cases := []struct {
		state     string
		mergeable *bool
		want      string
	}{
		{"behind", &yes, "BEHIND"},
		{"clean", &yes, "CLEAN"},
		{"dirty", &no, "DIRTY"},
		{"", &yes, "CLEAN"},
		{"", &no, "DIRTY"},
		{"", nil, "UNKNOWN"},
	}
	for _, tc := range cases {
		if got := normalizeMergeStateStatus(tc.state, tc.mergeable); got != tc.want {
			t.Errorf("normalizeMergeStateStatus(%q, %v) = %q, want %q", tc.state, tc.mergeable, got, tc.want)
		}
	}
}

// TestAlreadyUpToDateBehindIsNotVerified: GitHub can answer "already up to date"
// while the PR still reads BEHIND (an eventually-consistent read). That must not
// be reported as verified.
func TestAlreadyUpToDateBehindIsNotVerified(t *testing.T) {
	stub := newStubGitHub(t, func(w http.ResponseWriter, r *http.Request, call stubCall) {
		switch call.Method {
		case http.MethodPut:
			writeJSON(t, w, http.StatusUnprocessableEntity, `{"message":"Branch is already up to date."}`)
		case http.MethodGet:
			writeJSON(t, w, http.StatusOK, pullPayload("behindhead777", "behind"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	result, err := stub.client.UpdateBranch(context.Background(), UpdateBranchRequest{Number: 1037})
	if err != nil {
		t.Fatalf("UpdateBranch returned error: %v", err)
	}
	if result.Verified {
		t.Fatalf("verified = true although the PR still reads BEHIND")
	}
	if !strings.Contains(result.VerificationError, "BEHIND") {
		t.Errorf("verification_error = %q, want it to mention BEHIND", result.VerificationError)
	}
}
