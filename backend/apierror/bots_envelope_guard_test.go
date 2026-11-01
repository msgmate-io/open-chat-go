package apierror

// Guard for open-chat-go-ci#865: the bot API surface must never fall back to a
// plain-text http.Error. Generated clients (and the Live Runtime Canary) call
// response.json() on the response regardless of the status code, so a plain
// text rejection body turns an expected 4xx into a client-side JSONDecodeError
// and hides the real cause.
//
// The second guard pins the success-path contract the canary also depends on:
// every JSON write on the bot surface goes through an envelope-aware writer, so
// a client can always detect a connection drop or proxy cut-off as a parse
// failure instead of silently continuing on an empty 200 body.
//
// The scans are deliberately cheap and dependency free (stdlib + a relative
// directory walk) because they must stay runnable in the default Go suite, without
// the private integration module graph that `api/bots` itself needs to compile.
// They are skipped under `-short` (the default PR preflight) and run under the
// full suite run (the authoritative gate), where `api/bots` sources are present.
import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var plainTextErrorCall = regexp.MustCompile(`(^|[^\w.])http\.Error\(`)

func TestBotSurfaceHasNoPlainTextError(t *testing.T) {
	if testing.Short() {
		t.Skip("guard scan skipped under -short (runs in the full suite)")
	}
	const botsDir = "../api/bots"

	entries, err := os.ReadDir(botsDir)
	if err != nil {
		t.Fatalf("read %s: %v", botsDir, err)
	}

	scanned := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(botsDir, name)
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		body := stripComments(string(src))
		for i, line := range strings.Split(body, "\n") {
			if plainTextErrorCall.MatchString(line) {
				t.Errorf("%s:%d uses plain-text http.Error; use writeBotAPIError so "+
					"the rejection carries the JSON envelope (ci#865): %s",
					path, i+1, strings.TrimSpace(line))
			}
		}
		scanned++
	}

	if scanned == 0 {
		t.Fatalf("no Go sources found in %s", botsDir)
	}
}

// directJSONEncode is the success-path analogue of plainTextErrorCall: a naked
// json.NewEncoder(...).Encode(...) that bypasses the envelope-aware writer.
// A client that calls response.json() on such a response can get an empty 200
// (connection dropped between WriteHeader and Encode) and silently continue,
// exactly the kind of masking failure the canary hit on the error path.
var directJSONEncode = regexp.MustCompile(`json\.NewEncoder\(`)

func TestBotSurfaceJSONWritesGoThroughEnvelopeAwareWriter(t *testing.T) {
	if testing.Short() {
		t.Skip("guard scan skipped under -short (runs in the full suite)")
	}
	const botsDir = "../api/bots"

	entries, err := os.ReadDir(botsDir)
	if err != nil {
		t.Fatalf("read %s: %v", botsDir, err)
	}

	scanned := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(botsDir, name)
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		body := stripComments(string(src))
		for i, line := range strings.Split(body, "\n") {
			if directJSONEncode.MatchString(line) {
				t.Errorf("%s:%d writes JSON directly; use the envelope-aware writer so "+
					"the success body is always parseable (ci#865): %s",
					path, i+1, strings.TrimSpace(line))
			}
		}
		scanned++
	}

	if scanned == 0 {
		t.Fatalf("no Go sources found in %s", botsDir)
	}
}

// stripComments removes // line comments and /* */ block comments so that the
// guard does not trip on prose (or a doc comment) that merely mentions the
// deprecated helper.
func stripComments(src string) string {
	var b strings.Builder
	b.Grow(len(src))

	const (
		code = iota
		lineComment
		blockComment
	)
	state := code

	for i := 0; i < len(src); i++ {
		c := src[i]
		switch state {
		case code:
			if c == '/' && i+1 < len(src) && src[i+1] == '/' {
				state = lineComment
				i++
				continue
			}
			if c == '/' && i+1 < len(src) && src[i+1] == '*' {
				state = blockComment
				i++
				continue
			}
			b.WriteByte(c)
		case lineComment:
			if c == '\n' {
				state = code
				b.WriteByte(c)
			}
		case blockComment:
			// Keep newlines so reported line numbers stay accurate.
			if c == '\n' {
				b.WriteByte(c)
			}
			if c == '*' && i+1 < len(src) && src[i+1] == '/' {
				state = code
				i++
			}
		}
	}

	return b.String()
}
