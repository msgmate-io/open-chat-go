package webtools

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
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

// countingTransport serves a fixed status/body and records how many requests
// reached the network, so caching behaviour can be asserted offline.
type countingTransport struct {
	status int
	body   string
	calls  int
}

func (c *countingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	c.calls++
	hdr := make(http.Header)
	hdr.Set("Content-Type", "text/plain; charset=utf-8")
	return &http.Response{
		StatusCode: c.status,
		Header:     hdr,
		Body:       io.NopCloser(strings.NewReader(c.body)),
	}, nil
}

func withCountingResponse(t *testing.T, status int, body string) *countingTransport {
	t.Helper()
	ResetNotFoundCache()
	t.Cleanup(ResetNotFoundCache)
	transport := &countingTransport{status: status, body: body}
	old := httpClient
	httpClient = &http.Client{Transport: transport}
	t.Cleanup(func() { httpClient = old })
	return transport
}

func TestFetchNotFoundIsErrorNotDocument(t *testing.T) {
	transport := withCountingResponse(t, http.StatusNotFound, "<html><body>404 not found</body></html>")
	result, err := Fetch(context.Background(), "https://example.invalid/missing", 1024)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if result.Status != http.StatusNotFound {
		t.Fatalf("expected the 404 status to be reported, got %d", result.Status)
	}
	if strings.Contains(result.Content, "not found") {
		t.Fatalf("the error page must not be returned as content, got %q", result.Content)
	}
	if !strings.Contains(err.Error(), "do not re-fetch") {
		t.Fatalf("the error should tell the caller what to do next, got %v", err)
	}
	if transport.calls != 1 {
		t.Fatalf("expected a single network attempt, got %d", transport.calls)
	}
}

func TestFetchDeadURLIsCachedAcrossCalls(t *testing.T) {
	transport := withCountingResponse(t, http.StatusNotFound, "nope")
	for i := 0; i < 3; i++ {
		_, err := Fetch(context.Background(), "https://example.invalid/missing", 1024)
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("call %d: expected ErrNotFound, got %v", i, err)
		}
	}
	if transport.calls != 1 {
		t.Fatalf("a repeated dead URL must be served from the not-found cache, got %d network calls", transport.calls)
	}
}

func TestFetchGoneIsNotFound(t *testing.T) {
	withCountingResponse(t, http.StatusGone, "gone")
	if _, err := Fetch(context.Background(), "https://example.invalid/removed", 1024); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected HTTP 410 to be reported as not found, got %v", err)
	}
}

func TestFetchErrorStatusIsNotReturnedAsContent(t *testing.T) {
	withCountingResponse(t, http.StatusInternalServerError, "boom")
	result, err := Fetch(context.Background(), "https://example.invalid/boom", 1024)
	if !errors.Is(err, ErrBadStatus) {
		t.Fatalf("expected ErrBadStatus, got %v", err)
	}
	if result.Content != "" {
		t.Fatalf("an error page must not be returned as content, got %q", result.Content)
	}
}

func TestFetchSuccessReturnsContent(t *testing.T) {
	transport := withCountingResponse(t, http.StatusOK, "plain body")
	result, err := Fetch(context.Background(), "https://example.invalid/doc", 1024)
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if result.Content != "plain body" {
		t.Fatalf("unexpected content %q", result.Content)
	}
	if transport.calls != 1 {
		t.Fatalf("expected one attempt, got %d", transport.calls)
	}
}

// ci#789 follow-up: an agent copies a dead link out of a page (tracking params,
// fragment, mixed-case host) and re-fetches what is logically the same URL. The
// not-found cache key must be normalized so that costs no network round-trip.
func TestFetchDeadURLCopiesAreAnsweredFromCache(t *testing.T) {
	transport := withCountingResponse(t, http.StatusNotFound, "nope")
	if _, err := Fetch(context.Background(), "https://example.invalid/Missing", 1024); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	copies := []string{
		"https://example.invalid/Missing#install",
		"https://EXAMPLE.invalid/Missing",
		"https://example.invalid/Missing?utm_source=newsletter&utm_campaign=docs",
	}
	for _, copyURL := range copies {
		if _, err := Fetch(context.Background(), copyURL, 1024); !errors.Is(err, ErrNotFound) {
			t.Fatalf("%s: expected ErrNotFound, got %v", copyURL, err)
		}
	}
	if transport.calls != 1 {
		t.Fatalf("copies of the same dead URL must be served from the cache, got %d network calls", transport.calls)
	}
}

// A different path is a genuinely different guess and must still hit the network
// (the cache must not over-collapse), but its error names the paths this host is
// already known to be missing so the walk of guesses stops early.
func TestFetchNotFoundErrorNamesDeadPathsOnSameHost(t *testing.T) {
	transport := withCountingResponse(t, http.StatusNotFound, "nope")
	if _, err := Fetch(context.Background(), "https://example.invalid/api/v1/docs", 1024); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	_, err := Fetch(context.Background(), "https://example.invalid/api/v2/docs", 1024)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if transport.calls != 2 {
		t.Fatalf("a different path must be fetched, got %d network calls", transport.calls)
	}
	if !strings.Contains(err.Error(), "already known dead on this host") {
		t.Fatalf("expected the error to name the dead sibling paths, got %q", err.Error())
	}
	siblings := err.Error()[strings.Index(err.Error(), "already known dead on this host"):]
	if !strings.Contains(siblings, "https://example.invalid/api/v1/docs") {
		t.Fatalf("expected the earlier dead path to be named, got %q", err.Error())
	}
	if strings.Contains(siblings, "api/v2/docs") {
		t.Fatalf("the failing path must not be listed as its own sibling, got %q", err.Error())
	}
}

// Dead paths of another host are unrelated and must not be suggested.
func TestFetchNotFoundErrorIgnoresOtherHosts(t *testing.T) {
	withCountingResponse(t, http.StatusNotFound, "nope")
	if _, err := Fetch(context.Background(), "https://docs.example.invalid/old", 1024); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	_, err := Fetch(context.Background(), "https://example.invalid/api/v2", 1024)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if strings.Contains(err.Error(), "docs.example.invalid/old") {
		t.Fatalf("a dead path of another host must not be suggested, got %q", err.Error())
	}
}

func TestNormalizeNotFoundKey(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "fragment dropped",
			in:   "https://example.invalid/page#section",
			want: "https://example.invalid/page",
		},
		{
			name: "host and scheme lowercased",
			in:   "HTTPS://Example.Invalid/Page",
			want: "https://example.invalid/Page",
		},
		{
			name: "tracking params dropped, others kept sorted",
			in:   "https://example.invalid/page?b=2&utm_source=x&a=1&ref=hn",
			want: "https://example.invalid/page?a=1&b=2",
		},
		{
			name: "no host falls back to the trimmed url",
			in:   "  /relative/path  ",
			want: "/relative/path",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeNotFoundKey(tc.in); got != tc.want {
				t.Fatalf("normalizeNotFoundKey(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
	if got := normalizeNotFoundKey("https://example.invalid/a?utm_source=x"); got != normalizeNotFoundKey("https://EXAMPLE.invalid/a") {
		t.Fatalf("a tracked copy must share the cache key of the plain URL: %q", got)
	}
}

// The sibling list is bounded, so a long walk of guesses cannot make the error
// grow without limit.
func TestDeadPathSiblingsAreBounded(t *testing.T) {
	withCountingResponse(t, http.StatusNotFound, "nope")
	for i := 0; i < deadPathSiblingMax+3; i++ {
		url := "https://example.invalid/guess/" + strconv.Itoa(i)
		if _, err := Fetch(context.Background(), url, 1024); !errors.Is(err, ErrNotFound) {
			t.Fatalf("%s: expected ErrNotFound, got %v", url, err)
		}
	}
	siblings := deadPathSiblings(normalizeNotFoundKey("https://example.invalid/guess/final"))
	if len(siblings) > deadPathSiblingMax {
		t.Fatalf("expected at most %d siblings, got %d", deadPathSiblingMax, len(siblings))
	}
	if len(siblings) == 0 {
		t.Fatal("expected the previously guessed paths to be named")
	}
}
