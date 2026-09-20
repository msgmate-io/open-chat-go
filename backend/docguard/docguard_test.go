package docguard

// Guard for ci#736: this repository is public, so its documentation must stay
// strictly human-facing (setup, build, run, test, deploy, contributing, public
// architecture). Every operational runbook for the development bots - the
// development lead, the auto-consolidation bot, provider admission/concurrency
// internals, the native-coder selfcheck/auto-continue loop, tool-call budgets,
// the bot label policy and the CI-error relay - is owned by the private
// integration (`msgmate-io/open-chat-go-msgmate-coder`, docs/agent/) and is
// served to a native `msgmate_coder` interaction at runtime by its `coder_docs`
// tool. Nothing agent-only may be re-added here.
//
// The test scans the public markdown/docs surface for agent-only markers and
// checks that the removed agent trees did not come back. It is intentionally
// cheap (no build, no network) so it can run in the default Go suite.

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// repoRoot walks up from a directory until it finds the repository root (the
// directory that holds DEVELOPMENT.md and the backend/ tree).
func repoRoot(t *testing.T, start string) string {
	t.Helper()
	dir, err := filepath.Abs(start)
	if err != nil {
		t.Fatalf("resolve %s: %v", start, err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "DEVELOPMENT.md")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "backend")); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no repository root above %s", start)
		}
		dir = parent
	}
}

// publicAgentDocMarkers are phrases that only occur in agent/development-bot
// operation instructions. A human-facing document never needs them.
var publicAgentDocMarkers = []*regexp.Regexp{
	regexp.MustCompile(`(?i)development[\s-]+lead`),
	regexp.MustCompile(`(?i)devlead`),
	regexp.MustCompile(`(?i)auto-consolidat`),
	regexp.MustCompile(`(?i)consolidation[- ]bot`),
	regexp.MustCompile(`(?i)provider[\s_-]+admission`),
	regexp.MustCompile(`(?i)native[\s-]+coder[\s-]+selfcheck`),
	regexp.MustCompile(`(?i)tool[\s_-]?call[\s_-]max[\s_-](total|failed)`),
	regexp.MustCompile(`(?i)tool[\s-]+budget`),
	regexp.MustCompile(`(?i)pr[\s-]+label[\s-]+policy`),
	regexp.MustCompile(`(?i)bot[\s-]+label[\s-]+policy`),
	regexp.MustCompile(`(?i)ci[\s-]+(error[\s-]+)?relay`),
}

// publicAgentDocPaths are paths that must not exist in the public repository:
// the removed devlead tooling (ci#736 predecessor r42) and any agent-only doc
// directory.
var publicAgentDocPaths = []string{
	"development/scripts/devlead",
	"docs/agent",
	"development/agent-docs",
	"agent-docs",
	// The ci#733/ci#730 playbooks r42 dropped from the public tree. Naming
	// them explicitly keeps a future "restore the docs" change from quietly
	// re-adding the exact files ci#736 moved to the private integration.
	"docs/ci-733-devlead-fleet-moderation-playbook.md",
	"docs/ci730-coder-logic-extraction-plan.md",
	// r65: the ci#622 native-coder selfcheck triage note was added to the
	// public tree by mistake (PR #1018) and failed this guard in PR CI. The
	// note is a development-bot runbook; it lives in the private integration
	// (msgmate-io/open-chat-go-msgmate-coder, docs/agent/). Naming the path
	// keeps a future "restore the doc" change from re-adding it silently.
	"docs/native-coder-selfcheck-ci622.md",
			"docs/native-coder-selfcheck-ci927.md",
}

// publicDocFiles collects the documentation surface a human reads: the repo
// root markdown files, the published docs pages and any markdown under
// development/. Vendored/private checkouts (development/ci, clients/, node_modules
// and the integration caches) are skipped - that material is not ours to police
// and is not part of the public repo.
func publicDocFiles(t *testing.T, root string) []string {
	t.Helper()

	skipDirs := map[string]bool{
		".git": true, "node_modules": true, "clients": true, "ci": true,
		".integrations": true, "frontend_assets": true, "dist": true,
	}

	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path == root {
				return nil
			}
			if skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".") && d.Name() != ".github" {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".md") && !strings.HasSuffix(name, ".mdx") {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return files
}

// TestPublicDocsStayHumanFacing fails when an agent-only operation section is
// re-added to the public documentation surface or when the removed agent trees
// reappear (ci#736).
func TestPublicDocsStayHumanFacing(t *testing.T) {
	root := repoRoot(t, ".")

	for _, rel := range publicAgentDocPaths {
		if _, err := os.Stat(filepath.Join(root, rel)); err == nil {
			t.Errorf("%s exists in the public repository; agent/development-bot material belongs to the private integration (ci#736)", rel)
		}
	}

	for _, path := range publicDocFiles(t, root) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		rel, _ := filepath.Rel(root, path)
		for lineNo, line := range strings.Split(string(data), "\n") {
			for _, marker := range publicAgentDocMarkers {
				if !marker.MatchString(line) {
					continue
				}
				// An explicit pointer to the private integration is fine: the
				// human-facing docs may say where the runbooks live, they just
				// must not contain them.
				if strings.Contains(line, "msgmate-coder") || strings.Contains(line, "coder_docs") {
					continue
				}
				t.Errorf("%s:%d: agent-only documentation marker %q in a public document; move the section to the private integration (ci#736)\n\t%s",
					rel, lineNo+1, marker.String(), strings.TrimSpace(line))
			}
		}
	}
}
