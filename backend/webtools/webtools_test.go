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

// cannedTransport returns a fixed response for every request, so the search
// parsers can be exercised against challenge pages and payloads offline.
type cannedTransport struct {
	status int
	body   string
}

func (c *cannedTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: c.status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(c.body)),
	}, nil
}

func withCannedResponse(t *testing.T, status int, body string) {
	t.Helper()
	old := httpClient
	httpClient = &http.Client{Transport: &cannedTransport{status: status, body: body}}
	t.Cleanup(func() { httpClient = old })
}

func TestSearchDuckDuckGoChallengePageIsError(t *testing.T) {
	withCannedResponse(t, http.StatusAccepted, `<html><body>anomaly detected</body></html>`)
	results, err := searchDuckDuckGo(context.Background(), "golang net/http", 5)
	if !errors.Is(err, ErrSearchUnavailable) {
		t.Fatalf("expected ErrSearchUnavailable, got %v (results=%v)", err, results)
	}
}

func TestSearchDuckDuckGoChallengeMarkersWithOKStatusIsError(t *testing.T) {
	withCannedResponse(t, http.StatusOK, `<html><body>Unusual traffic from your network</body></html>`)
	_, err := searchDuckDuckGo(context.Background(), "golang net/http", 5)
	if !errors.Is(err, ErrSearchUnavailable) {
		t.Fatalf("expected ErrSearchUnavailable, got %v", err)
	}
}

func TestSearchDuckDuckGoEmptyAnomalyIsError(t *testing.T) {
	withCannedResponse(t, http.StatusOK, `<html><body>nothing useful here</body></html>`)
	_, err := searchDuckDuckGo(context.Background(), "golang net/http", 5)
	if !errors.Is(err, ErrSearchUnavailable) {
		t.Fatalf("expected ErrSearchUnavailable, got %v", err)
	}
}

func TestSearchDuckDuckGoNoResultsPageIsEmptyNotError(t *testing.T) {
	withCannedResponse(t, http.StatusOK, `<html><body><div class="no-results">No results.</div></body></html>`)
	results, err := searchDuckDuckGo(context.Background(), "asdfqwerzxcv", 5)
	if err != nil {
		t.Fatalf("expected no error for an explicit no-results page, got %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected zero results, got %d", len(results))
	}
}

func TestSearchDuckDuckGoParsesResults(t *testing.T) {
	page := `<html><body>` +
		`<a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fpkg.go.dev%2Fnet%2Fhttp">pkg.go.dev net/http</a>` +
		`<a class="result__snippet">Package http provides HTTP client and server implementations.</a>` +
		`</body></html>`
	withCannedResponse(t, http.StatusOK, page)
	results, err := searchDuckDuckGo(context.Background(), "golang net/http", 5)
	if err != nil {
		t.Fatalf("expected parsed results, got %v", err)
	}
	if len(results) != 1 || results[0].URL != "https://pkg.go.dev/net/http" {
		t.Fatalf("unexpected results: %+v", results)
	}
}

func TestSearchSearxNonJSONResponseIsError(t *testing.T) {
	withCannedResponse(t, http.StatusOK, `<html><body>search is offline</body></html>`)
	_, err := searchSearx(context.Background(), "http://searx.invalid", "golang net/http", 5)
	if !errors.Is(err, ErrSearchUnavailable) {
		t.Fatalf("expected ErrSearchUnavailable, got %v", err)
	}
}

func TestSearchSearxEmptyResultsIsError(t *testing.T) {
	withCannedResponse(t, http.StatusOK, `{"results":[]}`)
	_, err := searchSearx(context.Background(), "http://searx.invalid", "golang net/http", 5)
	if !errors.Is(err, ErrSearchUnavailable) {
		t.Fatalf("expected ErrSearchUnavailable, got %v", err)
	}
}

func TestSearchSearxChallengePageIsError(t *testing.T) {
	withCannedResponse(t, http.StatusOK, `<html><body>Please solve this captcha</body></html>`)
	_, err := searchSearx(context.Background(), "http://searx.invalid", "golang net/http", 5)
	if !errors.Is(err, ErrSearchUnavailable) {
		t.Fatalf("expected ErrSearchUnavailable, got %v", err)
	}
}

func TestSearchSearxParsesResults(t *testing.T) {
	withCannedResponse(t, http.StatusOK, `{"results":[{"title":"net/http","url":"https://pkg.go.dev/net/http","content":"Package http"}]}`)
	results, err := searchSearx(context.Background(), "http://searx.invalid", "golang net/http", 5)
	if err != nil {
		t.Fatalf("expected parsed results, got %v", err)
	}
	if len(results) != 1 || results[0].URL != "https://pkg.go.dev/net/http" {
		t.Fatalf("unexpected results: %+v", results)
	}
}

func TestSearchSearxNon2xxIsError(t *testing.T) {
	withCannedResponse(t, http.StatusServiceUnavailable, `nope`)
	_, err := searchSearx(context.Background(), "http://searx.invalid", "golang net/http", 5)
	if err == nil {
		t.Fatal("expected an error for a non-2xx response")
	}
	if errors.Is(err, ErrSearchUnavailable) {
		t.Fatalf("a transport-level status must not be ErrSearchUnavailable: %v", err)
	}
}
