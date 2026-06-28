package webtools

import "testing"

// ci#789: agents guessed near-identical spellings of the same dead URL, and
// every spelling was a fresh not-found cache miss, so each guess paid another
// 404 round-trip. The cache key must fold those trivial variants together.
func TestNotFoundKeyCanonicalisesTrivialVariants(t *testing.T) {
	cases := map[string]string{
		"https://example.com/old/page":            "https://example.com/old/page",
		"https://example.com/old/page/":           "https://example.com/old/page",
		"https://example.com/old/page#anchor":     "https://example.com/old/page",
		"  https://example.com/old/page/#anchor ": "https://example.com/old/page",
		"https://example.com":                     "https://example.com",
	}
	for in, want := range cases {
		if got := notFoundKey(in); got != want {
			t.Fatalf("notFoundKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDeadURLVariantsShareOneRememberedMiss(t *testing.T) {
	ResetNotFoundCache()
	defer ResetNotFoundCache()

	rememberNotFound("https://example.com/old/page")

	res, ok := cachedNotFound("https://example.com/old/page/#anchor")
	if !ok {
		t.Fatal("a trivial variant of a remembered dead URL must be served from the not-found cache")
	}
	if res.Status != 404 {
		t.Fatalf("remembered status = %d, want 404", res.Status)
	}
	if _, ok := cachedNotFound("https://example.com/other/page"); ok {
		t.Fatal("a different path must not be served from the not-found cache")
	}
}
