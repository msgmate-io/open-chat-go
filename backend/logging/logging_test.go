package logging

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"log/slog"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"":      slog.LevelInfo,
		"debug": slog.LevelDebug,
		"INFO":  slog.LevelInfo,
		"warn":  slog.LevelWarn,
		"error": slog.LevelError,
		"off":   Silent,
	}
	for raw, want := range cases {
		got, err := ParseLevel(raw)
		if err != nil {
			t.Fatalf("ParseLevel(%q): %v", raw, err)
		}
		if got != want {
			t.Fatalf("ParseLevel(%q) = %v, want %v", raw, got, want)
		}
	}
	if _, err := ParseLevel("verbose"); err == nil {
		t.Fatalf("expected an error for an unknown level")
	}
	if err := Configure(Options{Level: "info", Format: "yaml", Writer: io.Discard}); err == nil {
		t.Fatalf("expected an error for an unknown format")
	}
}

func TestLevelFilteringAndLegacyBridge(t *testing.T) {
	var buf bytes.Buffer
	if err := Configure(Options{Level: "warn", Format: "text", Writer: &buf}); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	t.Cleanup(func() {
		slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
		log.SetOutput(io.Discard)
		log.SetFlags(0)
	})

	log.Printf("legacy info record")
	Debugf("debug record")
	Infof("info record %d", 1)
	Warnf("warn record %d", 2)
	Errorf("error record")

	out := buf.String()
	for _, hidden := range []string{"legacy info record", "debug record", "info record"} {
		if strings.Contains(out, hidden) {
			t.Fatalf("level warn must not emit %q, got %q", hidden, out)
		}
	}
	if !strings.Contains(out, "warn record 2") {
		t.Fatalf("missing warn record in %q", out)
	}
	if !strings.Contains(out, "error record") {
		t.Fatalf("missing error record in %q", out)
	}
}

func TestDebugLevelShowsLegacyAndDebugRecords(t *testing.T) {
	var buf bytes.Buffer
	if err := Configure(Options{Level: "debug", Format: "json", Writer: &buf}); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	t.Cleanup(func() {
		slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
		log.SetOutput(io.Discard)
		log.SetFlags(0)
	})

	log.Printf("legacy info record")
	Debugf("debug record")
	Info("structured record", "chat", "abc")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 records, got %d: %q", len(lines), buf.String())
	}
	for _, line := range lines {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("record %q is not JSON: %v", line, err)
		}
		if _, ok := record["level"]; !ok {
			t.Fatalf("record %q has no level", line)
		}
	}
	if !strings.Contains(lines[1], "debug record") || !strings.Contains(lines[1], "DEBUG") {
		t.Fatalf("debug record missing or not at debug level: %q", lines[1])
	}
}
