package nativecoder

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCloseIssueUsesLinkedIssueRepoAndIsIdempotent(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"state":"open"}`)
		case r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPatch:
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), `"state":"closed"`) {
				t.Errorf("close payload = %s, want state closed", body)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"state":"closed"}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client := testClient(server)
	if err := client.CloseIssue(context.Background(), 813, "Closed by consolidation PR #900."); err != nil {
		t.Fatalf("CloseIssue: %v", err)
	}
	want := []string{
		"GET /repos/msgmate-io/open-chat-go-ci/issues/813",
		"POST /repos/msgmate-io/open-chat-go-ci/issues/813/comments",
		"PATCH /repos/msgmate-io/open-chat-go-ci/issues/813",
	}
	if strings.Join(calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls =\n%s\nwant\n%s", strings.Join(calls, "\n"), strings.Join(want, "\n"))
	}
}

func TestCloseIssueSkipsAnAlreadyClosedIssue(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"state":"closed"}`)
	}))
	defer server.Close()

	client := testClient(server)
	if err := client.CloseIssue(context.Background(), 7, "already done"); err != nil {
		t.Fatalf("CloseIssue: %v", err)
	}
	if len(calls) != 1 || calls[0] != "GET /repos/msgmate-io/open-chat-go-ci/issues/7" {
		t.Fatalf("already-closed issue must not be commented/patched, calls=%v", calls)
	}
}
