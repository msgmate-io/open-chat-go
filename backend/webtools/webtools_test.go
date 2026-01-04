package webtools

import (
	"strings"
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
