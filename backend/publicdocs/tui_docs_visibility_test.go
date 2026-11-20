package publicdocs

// Coverage guard for ci#1151 ("TUI Documentation page should be part of the
// private TUI integration").
//
// The terminal UI is owned by the private `tui` integration, not by the core
// docs. Its documentation page therefore has to be gated with
// `visibility: integration` + `integration: tui` so that anonymous visitors and
// users without access to the integration never see it, and the public pages
// may only mention the TUI in one short sentence that points at the
// integration's own docs - never as a client that ships with the public build.
//
// Phase A of ci#1151 keeps the page in this repository but gated; Phase B moves
// the prose into the private integration repository and deletes the page (that
// half lands in the integration repo and is tracked on the ticket).

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	// tuiDocsPage is the gated documentation page of the `tui` integration.
	tuiDocsPage = "frontend/pages/docs/content/tui.mdx"
	// tuiDocsContentDir holds every core `/docs` markdown page.
	tuiDocsContentDir = "frontend/pages/docs/content"
	// tuiDocsPathLink is the concrete page route. It only resolves for pages
	// that are in the public docs tree, so a gated page must not be linked
	// through it.
	tuiDocsPathLink = "/docs/tui"
)

// tuiPublicBuildClaims are phrasings that present the TUI as a client shipped
// with the public build. Public pages may not use any of them; the gated page
// may, as long as it does not claim the TUI is part of the standard build.
var tuiPublicBuildClaims = []string{
	"built-in TUI",
	"built into `open-chat tui`",
	"part of the standard `open-chat` build",
	"part of the standard build",
}

// TestTUIDocsPageIsGatedToTheIntegration checks that the TUI documentation page
// is private documentation of the `tui` integration.
func TestTUIDocsPageIsGatedToTheIntegration(t *testing.T) {
	root := repoRoot(t)

	raw, err := os.ReadFile(filepath.Join(root, tuiDocsPage))
	if err != nil {
		t.Fatalf("read tui docs page: %v", err)
	}
	page := string(raw)

	frontmatter, body, found := strings.Cut(page, "\n---\n")
	if !found {
		t.Fatalf("%s must start with a frontmatter block", tuiDocsPage)
	}

	for _, want := range []string{
		"visibility: integration",
		"integration: tui",
		"path: tui",
		"section: Clients",
		"order: 54",
	} {
		if !strings.Contains(frontmatter, want) {
			t.Errorf("docs frontmatter must contain %q", want)
		}
	}

	// `adminOnly: false` used to stand in for the missing visibility gate; it
	// must not come back as a substitute for `visibility: integration`.
	if strings.Contains(frontmatter, "adminOnly:") {
		t.Error("docs frontmatter must not carry adminOnly now that the page is gated per integration")
	}

	// What a holder needs stays in the page: the launcher, the config
	// directory override and the environment overrides.
	for _, want := range []string{
		"open-chat tui",
		"OPEN_CHAT_TUI_CONFIG_DIR",
		"OPENCHAT_SERVER_URL",
		"OPENCHAT_ACCESS_TOKEN",
		"OPENCHAT_CHAT_UUID",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("docs body must document %q", want)
		}
	}

	// Implementation details of the forwarding deployment belong to the
	// integration's own docs, not to the core page.
	for _, unwanted := range []string{
		"Forwarded TUI in a coding sandbox",
		"coder-tui",
		"SessionTUIConfigDir",
		"backend/cmd/tui_session.go",
	} {
		if strings.Contains(body, unwanted) {
			t.Errorf("docs body must not carry integration internals (%q); it has to point at the integration's own docs", unwanted)
		}
	}

	if !strings.Contains(body, "integration's own docs") {
		t.Error("docs body must point a holder at the integration's own docs for the deployment internals")
	}
}

// TestTUIDocsAreNotAdvertisedAsPublic checks that no core docs page links the
// gated page by its public path or advertises the TUI as part of the public
// build (ci#1151).
func TestTUIDocsAreNotAdvertisedAsPublic(t *testing.T) {
	root := repoRoot(t)

	contentDir := filepath.Join(root, tuiDocsContentDir)
	pages := 0

	err := filepath.WalkDir(contentDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".mdx") {
			return nil
		}
		pages++

		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)

		for lineNo, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, tuiDocsPathLink) {
				t.Errorf("%s:%d: %q links the gated TUI page by its public path; use the /docs#tui anchor or the integration's own docs (ci#1151)\n\t%s",
					rel, lineNo+1, tuiDocsPathLink, strings.TrimSpace(line))
			}
			if rel == tuiDocsPage {
				// The gated page itself names the client it documents.
				continue
			}
			for _, claim := range tuiPublicBuildClaims {
				if strings.Contains(line, claim) {
					t.Errorf("%s:%d: public page advertises the TUI as part of the build (%q); the TUI belongs to the private `tui` integration (ci#1151)\n\t%s",
						rel, lineNo+1, claim, strings.TrimSpace(line))
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", contentDir, err)
	}

	if pages == 0 {
		t.Fatalf("no .mdx pages found under %s; the docs layout changed", tuiDocsContentDir)
	}
}
