package webtools

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"syscall"
	"testing"
)

func TestHTMLToTextStripsMarkupAndScripts(t *testing.T) {
	doc := `<!doctype html><html><head><title>Docs &amp; Guide</title>
	<style>.x{color:red}</style><script>alert("hi")</script></head>
	<body><h1>Hello</h1><p>World &amp; friends</p><ul><li>one</li><li>two</li></ul></body></html>`
	title, text := htmlToText(doc)
	if title != "Docs & Guide" {
		t.Fatalf("expected decoded title, got %q", title)
	}
	if strings.Contains(text, "alert") || strings.Contains(text, "color:red") {
		t.Fatalf("script/style content must be stripped, got: %q", text)
	}
	if !strings.Contains(text, "Hello") || !strings.Contains(text, "World & friends") {
		t.Fatalf("expected body text, got: %q", text)
	}
	if !strings.Contains(text, "one") || !strings.Contains(text, "two") {
		t.Fatalf("expected list items, got: %q", text)
	}
}

func TestIsHTMLContentType(t *testing.T) {
	if !isHTMLContentType("text/html; charset=utf-8", []byte("<html></html>")) {
		t.Fatal("text/html should be treated as HTML")
	}
	if isHTMLContentType("application/json", []byte(`{"a":1}`)) {
		t.Fatal("application/json must not be treated as HTML")
	}
	if !isHTMLContentType("", []byte("<!doctype html><html>")) {
		t.Fatal("sniffed HTML should be treated as HTML")
	}
	if isHTMLContentType("text/plain", []byte("plain body")) {
		t.Fatal("plain text must not be treated as HTML")
	}
}

func TestStripTagsCollapsesWhitespace(t *testing.T) {
	got := stripTags("<span>  hello \n  <b>world</b> </span>")
	if got != "hello world" {
		t.Fatalf("expected collapsed text, got %q", got)
	}
}

// flakyTransport fails the first `failures` requests with `err` and then
// succeeds. It lets the retry loop be exercised without touching the network.
type flakyTransport struct {
	failures int
	calls    int
	err      error
}

func (f *flakyTransport) RoundTrip(*http.Request) (*http.Response, error) {
	f.calls++
	if f.calls <= f.failures {
		return nil, f.err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("ok")),
	}, nil
}

func TestIsTransientNetworkError(t *testing.T) {
	transient := []error{
		&net.OpError{Op: "read", Err: syscall.ECONNRESET},
		io.EOF,
		io.ErrUnexpectedEOF,
		errors.New("read tcp 10.0.0.1:1234->40.114.177.156:443: read: connection reset by peer"),
		errors.New("dial tcp: i/o timeout"),
	}
	for _, err := range transient {
		if !isTransientNetworkError(err) {
			t.Fatalf("expected %v to be transient", err)
		}
	}
	permanent := []error{
		nil,
		context.Canceled,
		context.DeadlineExceeded,
		&net.DNSError{Err: "no such host", Name: "nope.invalid", IsNotFound: true},
		errors.New("invalid search response"),
	}
	for _, err := range permanent {
		if isTransientNetworkError(err) {
			t.Fatalf("expected %v not to be transient", err)
		}
	}
}

func TestDoRequestRetriesTransientFailures(t *testing.T) {
	oldClient := httpClient
	defer func() { httpClient = oldClient }()
	transport := &flakyTransport{failures: 2, err: &net.OpError{Op: "read", Err: syscall.ECONNRESET}}
	httpClient = &http.Client{Transport: transport}

	resp, err := doRequest(context.Background(), func() (*http.Request, error) {
		return http.NewRequest(http.MethodGet, "http://example.invalid/", nil)
	})
	if err != nil {
		t.Fatalf("expected retry to succeed, got %v", err)
	}
	defer resp.Body.Close()
	if transport.calls != networkAttempts {
		t.Fatalf("expected %d attempts, got %d", networkAttempts, transport.calls)
	}
}

func TestDoRequestDoesNotRetryPermanentErrors(t *testing.T) {
	oldClient := httpClient
	defer func() { httpClient = oldClient }()
	transport := &flakyTransport{failures: 5, err: errors.New("invalid search response")}
	httpClient = &http.Client{Transport: transport}

	_, err := doRequest(context.Background(), func() (*http.Request, error) {
		return http.NewRequest(http.MethodGet, "http://example.invalid/", nil)
	})
	if err == nil {
		t.Fatal("expected permanent error to be returned")
	}
	if transport.calls != 1 {
		t.Fatalf("expected a single attempt, got %d", transport.calls)
	}
}
