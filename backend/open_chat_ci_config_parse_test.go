package main

import (
	"os"
	"testing"
)

// TestLoadLiveCIConfigParses validates that the gitignored ci.msgmate.io
// open-chat config parses under the strict schema (including anchors and the
// bootstrap.users/bots/git sections). It is skipped when the config is absent
// (e.g. public CI), so it only guards local/ops edits.
func TestLoadLiveCIConfigParses(t *testing.T) {
	paths := []string{
		"../development/ci/open-chat-ci.yaml",
		"development/ci/open-chat-ci.yaml",
	}
	var raw []byte
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err == nil {
			raw = data
			break
		}
	}
	if len(raw) == 0 {
		t.Skip("open-chat-ci.yaml not present; skipping live config parse check")
	}

	cfg, err := loadOpenChatConfig(raw, "development/ci/open-chat-ci.yaml")
	if err != nil {
		t.Fatalf("live ci config failed to parse: %v", err)
	}
	if cfg.Bootstrap == nil {
		t.Fatalf("expected bootstrap section")
	}
}
