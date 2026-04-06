package publicdocs

// Guard for ci#738: the public documentation must stay human-facing. Internal
// design notes - migration playbooks, LLM/agent development context, staged
// refactor plans - are not user documentation, so they are neither published in
// frontend/pages/docs/content nor kept anywhere else in this public repository.
// They live in the private integration (msgmate-io/open-chat-go-msgmate-coder)
// and are served to a native coder interaction at runtime.
//
// The first entry is the ci#423 stable-Host migration design page
// (integration-host-migration), which ci#738 removed from the public docs.

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// internalDesignDocPaths are repository-relative paths that must not exist in
// this public repository: internal design/backlog documents that used to be
// committed here.
var internalDesignDocPaths = []string{
	"frontend/pages/docs/content/integration-host-migration.mdx",
}

// internalDesignDocLinks are anchors/links into removed internal docs. They may
// not appear in any file that remains in the public tree.
var internalDesignDocLinks = []string{
	"docs#integration-host-migration",
	"content/integration-host-migration",
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "DEVELOPMENT.md")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "backend")); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no repository root above %s", dir)
		}
		dir = parent
	}
}

// TestInternalDesignDocsStayOutOfThePublicTree fails when a removed internal
// design document reappears or something still links to it (ci#738).
func TestInternalDesignDocsStayOutOfThePublicTree(t *testing.T) {
	root := repoRoot(t)

	for _, rel := range internalDesignDocPaths {
		if _, err := os.Stat(filepath.Join(root, rel)); err == nil {
			t.Errorf("%s is committed again; internal design context belongs to the private integration (ci#738)", rel)
		}
	}

	skipDirs := map[string]bool{
		".git": true, "node_modules": true, "clients": true, "dist": true,
		"frontend_assets": true, ".integrations": true,
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path == root {
				return nil
			}
			name := d.Name()
			if skipDirs[name] || (strings.HasPrefix(name, ".") && name != ".github") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".md") && !strings.HasSuffix(d.Name(), ".mdx") && !strings.HasSuffix(d.Name(), ".py") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		for lineNo, line := range strings.Split(string(data), "\n") {
			for _, marker := range internalDesignDocLinks {
				if !strings.Contains(line, marker) {
					continue
				}
				// Naming the removed page in this guard is fine; pointing a
				// reader at it is not.
				if rel == "backend/publicdocs/publicdocs_test.go" {
					continue
				}
				t.Errorf("%s:%d: dangling reference %q to a removed internal design document (ci#738)\n\t%s",
					rel, lineNo+1, marker, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
}
