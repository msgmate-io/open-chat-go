package runtimecfg

import "testing"

func TestParseCORSAllowedOrigins(t *testing.T) {
	t.Run("empty input", func(t *testing.T) {
		origins, err := ParseCORSAllowedOrigins("")
		if err != nil || origins != nil {
			t.Fatalf("expected nil origins without error, got %v (%v)", origins, err)
		}
	})

	t.Run("valid origins normalized", func(t *testing.T) {
		origins, err := ParseCORSAllowedOrigins("https://Admin.Example.com/, http://localhost:3000")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(origins) != 2 || origins[0] != "https://admin.example.com" || origins[1] != "http://localhost:3000" {
			t.Fatalf("unexpected origins: %v", origins)
		}
	})

	t.Run("wildcard rejected", func(t *testing.T) {
		if _, err := ParseCORSAllowedOrigins("https://*.example.com"); err == nil {
			t.Fatalf("expected wildcard origin to be rejected")
		}
		if _, err := ParseCORSAllowedOrigins("*"); err == nil {
			t.Fatalf("expected bare wildcard to be rejected")
		}
	})

	t.Run("origin without scheme rejected", func(t *testing.T) {
		if _, err := ParseCORSAllowedOrigins("admin.example.com"); err == nil {
			t.Fatalf("expected scheme-less origin to be rejected")
		}
	})
}
