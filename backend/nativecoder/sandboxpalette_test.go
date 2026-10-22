package nativecoder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSandboxPaletteShape(t *testing.T) {
	if len(DefaultSandboxPalette) < 2 {
		t.Fatalf("palette must offer more than one image, got %d", len(DefaultSandboxPalette))
	}
	if DefaultSandboxPalette[0].Template != SandboxTemplateDefault {
		t.Fatalf("first palette entry must be the fallback %q, got %q", SandboxTemplateDefault, DefaultSandboxPalette[0].Template)
	}
	seen := map[string]bool{}
	for _, entry := range DefaultSandboxPalette {
		if entry.Template == "" || entry.Template != strings.ToLower(entry.Template) {
			t.Errorf("template %q must be a non-empty lowercase slug", entry.Template)
		}
		if seen[entry.Template] {
			t.Errorf("template %q appears twice in the palette", entry.Template)
		}
		seen[entry.Template] = true
		if entry.Label == "" || entry.Purpose == "" {
			t.Errorf("palette entry %q must document a label and a purpose", entry.Template)
		}
	}
}

// TestSandboxPaletteMatchesPublishWorkflow keeps the core palette and the
// publish workflow's PALETTE_TEMPLATES mirror in sync (ci#807): a template
// added here can never silently miss its published sandbox image.
func TestSandboxPaletteMatchesPublishWorkflow(t *testing.T) {
	workflowPath := filepath.Join(repoRootForTest(t), ".github", "workflows", "build-sandbox-images.yaml")
	raw, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatalf("read %s: %v", workflowPath, err)
	}
	workflow := string(raw)
	const marker = "PALETTE_TEMPLATES:"
	idx := strings.Index(workflow, marker)
	if idx < 0 {
		t.Fatalf("%s must declare %s so the workflow builds the whole palette", workflowPath, marker)
	}
	rest := workflow[idx+len(marker):]
	end := strings.IndexByte(rest, '\n')
	if end < 0 {
		end = len(rest)
	}
	declared := strings.Fields(strings.Trim(strings.TrimSpace(rest[:end]), `"'`))
	want := SandboxPaletteTemplates()
	if strings.Join(declared, " ") != strings.Join(want, " ") {
		t.Fatalf("workflow PALETTE_TEMPLATES %v drifted from DefaultSandboxPalette %v", declared, want)
	}
}

// TestSandboxTemplateForRepo pins the per-repository default mapping: the
// heavy repositories get their toolchain image, the longest prefix wins and an
// unknown repository falls back to the deployment default.
func TestSandboxTemplateForRepo(t *testing.T) {
	cases := []struct {
		repo string
		want string
	}{
		{"msgmate-io/open-chat-go", "coding"},
		{"msgmate-io/open-chat-go-ci", "coding"},
		{"msgmate-io/open-chat-go-ci.git", "coding"},
		{"https://github.com/msgmate-io/open-chat-go", "coding"},
		{"msgmate-io/open-chat-go-android", "android"},
		{"tbscode/tims-nix", "nix"},
		{"tbscode/tims-nix-build", "nix"},
		{"someone/unknown-repo", ""},
		{"", ""},
	}
	for _, tc := range cases {
		if got := SandboxTemplateForRepo(tc.repo); got != tc.want {
			t.Errorf("SandboxTemplateForRepo(%q) = %q, want %q", tc.repo, got, tc.want)
		}
	}
}

// TestSandboxTemplateForInteraction pins the precedence order: an explicit
// config key (bot/trigger default or caller override) wins, then a
// `sandbox:<template>` tag, then the palette default for the resolved repo.
func TestSandboxTemplateForInteraction(t *testing.T) {
	coderConfig := map[string]interface{}{"coder_interaction": "autonomous", "repo": "msgmate-io/open-chat-go-ci"}
	cases := []struct {
		name      string
		config    map[string]interface{}
		tags      []string
		overrides map[string]interface{}
		want      string
	}{
		{name: "repo default", config: coderConfig, want: "coding"},
		{name: "repo tag wins over config repo", config: coderConfig, tags: []string{"repo:tbscode/tims-nix"}, want: "nix"},
		{name: "sandbox tag wins over repo", config: coderConfig, tags: []string{"sandbox:vnc"}, want: "vnc"},
		{name: "sandbox tag is case-insensitive", config: coderConfig, tags: []string{"Sandbox: Nix "}, want: "nix"},
		{name: "unknown sandbox tag is ignored", config: coderConfig, tags: []string{"sandbox:does-not-exist"}, want: "coding"},
		{name: "explicit config key pins the template", config: map[string]interface{}{"coder_interaction": "autonomous", "coder_sandbox_template": "odoo", "repo": "msgmate-io/open-chat-go-ci"}, want: ""},
		{name: "caller override pins the template", config: coderConfig, overrides: map[string]interface{}{"coder_sandbox_template": "minimal"}, want: ""},
		{name: "unknown repo keeps the deployment default", config: map[string]interface{}{"coder_interaction": "autonomous", "repo": "someone/else"}, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SandboxTemplateForInteraction(tc.config, tc.tags, tc.overrides); got != tc.want {
				t.Fatalf("SandboxTemplateForInteraction() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestWithAutoSandboxTemplate pins the binding contract: only a coder config
// that does not already pin a template is annotated, the caller's map is never
// mutated and an unknown template is a no-op.
func TestWithAutoSandboxTemplate(t *testing.T) {
	original := map[string]interface{}{"coder_interaction": "autonomous"}
	got := WithAutoSandboxTemplate(original, " Nix ")
	if got[SandboxTemplateConfigKey] != "nix" {
		t.Fatalf("expected template nix bound, got %v", got[SandboxTemplateConfigKey])
	}
	if _, mutated := original[SandboxTemplateConfigKey]; mutated {
		t.Fatal("WithAutoSandboxTemplate must not mutate the caller's config")
	}

	pinned := map[string]interface{}{"coder_interaction": "autonomous", SandboxTemplateConfigKey: "odoo"}
	if got := WithAutoSandboxTemplate(pinned, "coding"); got[SandboxTemplateConfigKey] != "odoo" {
		t.Fatalf("an explicitly pinned template must win, got %v", got[SandboxTemplateConfigKey])
	}

	if got := WithAutoSandboxTemplate(original, "not-a-template"); got[SandboxTemplateConfigKey] != nil {
		t.Fatalf("unknown template must be ignored, got %v", got[SandboxTemplateConfigKey])
	}

	nonCoder := map[string]interface{}{"model": "gpt-4o"}
	if got := WithAutoSandboxTemplate(nonCoder, "coding"); got[SandboxTemplateConfigKey] != nil {
		t.Fatal("non-coder config must be returned unchanged")
	}
}

// TestDefaultInteractionTagsStampsSandboxTemplate pins that the sandbox image a
// coder interaction was provisioned from is visible as a `sandbox:<template>`
// tag next to the per-repo tag.
func TestDefaultInteractionTagsStampsSandboxTemplate(t *testing.T) {
	config := map[string]interface{}{
		"coder_interaction":      "autonomous",
		"repo":                   "msgmate-io/open-chat-go",
		SandboxTemplateConfigKey: "coding",
	}
	tags := DefaultInteractionTags(config, "coder", nil)
	if !hasTagWithPrefix(tags, SandboxTagPrefix) {
		t.Fatalf("expected a %s tag, got %v", SandboxTagPrefix, tags)
	}
	explicit := DefaultInteractionTags(config, "coder", []string{"sandbox:nix"})
	if tag := tagFromTags(explicit, SandboxTagPrefix); tag != SandboxTagPrefix+"nix" {
		t.Fatalf("an explicit sandbox tag must win, got %v", explicit)
	}
}

func tagFromTags(tags []string, prefix string) string {
	for _, tag := range tags {
		if strings.HasPrefix(tag, prefix) {
			return tag
		}
	}
	return ""
}

func repoRootForTest(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	dir := wd
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "DEVELOPMENT.md")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("could not locate the repository root above %s", wd)
	return ""
}
