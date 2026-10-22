package nativecoder

import (
	"strings"
	"testing"
)

// ci#807: the sandbox palette must stay internally consistent and must map
// every configured ci.msgmate.io git trigger to a variant that fixes its
// development process.

func TestSandboxPaletteIsValid(t *testing.T) {
	palette := SandboxPaletteSnapshot()
	if errs := ValidateSandboxPalette(palette); len(errs) != 0 {
		t.Fatalf("palette is invalid:\n%s", strings.Join(errs, "\n"))
	}
}

func TestSandboxPaletteCoversKnownVariants(t *testing.T) {
	// The variants the ticket's plan names must exist, so the trigger mapping
	// and the publish workflow cannot drift silently.
	want := []string{"minimal", "coding", "frontend", "nix", "k8s", "odoo", "android", "vnc"}
	for _, variant := range want {
		if !sandboxVariantExists(variant) {
			t.Errorf("palette is missing variant %q", variant)
		}
	}
	if got := SandboxVariantNames(); len(got) != len(sandboxPaletteVariants) {
		t.Errorf("SandboxVariantNames returned %d names, want %d", len(got), len(sandboxPaletteVariants))
	}
}

func TestSandboxDefaultVariantIsCoding(t *testing.T) {
	if SandboxDefaultVariant != "coding" {
		t.Fatalf("default variant = %q, want coding", SandboxDefaultVariant)
	}
	if !sandboxVariantExists(SandboxDefaultVariant) {
		t.Fatalf("default variant %q is not part of the palette", SandboxDefaultVariant)
	}
}

func TestSandboxVariantForRepo(t *testing.T) {
	cases := []struct {
		repo    string
		variant string
		mapped  bool
	}{
		{"msgmate-io/open-chat-go", "coding", true},
		{"MSGMATE-IO/Open-Chat-Go", "coding", true}, // case-insensitive
		{"msgmate-io/open-chat-go-frontend", "frontend", true},
		{"tbscode/tims-nix", "nix", true}, // tims-nix#10: nix was missing
		{"msgmate-io/open-chat-go-helm-chart", "k8s", true},
		{"msgmate-io/open-chat-go-mobile-client", "android", true},
		{"msgmate-io/open-chat-go-odoo-integration", "odoo", true},
		{"msgmate-io/unmapped-repo", "coding", false}, // falls back to the default
	}
	for _, tc := range cases {
		variant, mapped := SandboxVariantForRepo(tc.repo)
		if mapped != tc.mapped || variant != tc.variant {
			t.Errorf("SandboxVariantForRepo(%q) = (%q, %v), want (%q, %v)", tc.repo, variant, mapped, tc.variant, tc.mapped)
		}
	}
}

func TestSandboxImageRef(t *testing.T) {
	if got, want := SandboxImageRef("msgmate-io/open-chat-go", "nix"),
		"ghcr.io/msgmate-io/open-chat-go:sandbox-nix-latest"; got != want {
		t.Errorf("SandboxImageRef = %q, want %q", got, want)
	}
	if got := SandboxImageRef("msgmate-io/open-chat-go", "no-such-variant"); got != "" {
		t.Errorf("unknown variant should render no image ref, got %q", got)
	}
	if got := SandboxImageRef("not-a-slug", "coding"); got != "" {
		t.Errorf("malformed slug should render no image ref, got %q", got)
	}
	if got, want := SandboxImageTags("k8s"), "sandbox-k8s"; got != want {
		t.Errorf("SandboxImageTags = %q, want %q", got, want)
	}
}

func TestSandboxCommandsForVariantIsSorted(t *testing.T) {
	commands := SandboxCommandsForVariant("nix")
	if len(commands) == 0 {
		t.Fatal("nix variant guarantees no commands")
	}
	hasNix := false
	for _, command := range commands {
		if command == "nix" {
			hasNix = true
		}
	}
	if !hasNix {
		t.Fatalf("nix variant does not guarantee the nix command: %v", commands)
	}
	for i := 1; i < len(commands); i++ {
		if commands[i-1] > commands[i] {
			t.Fatalf("commands are not sorted: %v", commands)
		}
	}
	if got := SandboxCommandsForVariant("no-such-variant"); got != nil {
		t.Fatalf("unknown variant should have no commands, got %v", got)
	}
}

func TestSandboxPaletteGaps(t *testing.T) {
	palette := SandboxPaletteSnapshot()
	gaps := SandboxPaletteGaps(palette, []string{
		"msgmate-io/open-chat-go",
		"msgmate-io/unconfigured-repo",
		"msgmate-io/unconfigured-repo", // deduped
		"",
	})
	if len(gaps) != 1 || gaps[0] != "msgmate-io/unconfigured-repo" {
		t.Fatalf("SandboxPaletteGaps = %v, want [msgmate-io/unconfigured-repo]", gaps)
	}
}

func TestSandboxPaletteSnapshotIsACopy(t *testing.T) {
	first := SandboxPaletteSnapshot()
	first.Variants[0].Commands[0] = "mutated"
	first.Triggers[0].Repo = "mutated"
	second := SandboxPaletteSnapshot()
	if second.Variants[0].Commands[0] == "mutated" {
		t.Fatal("palette commands leaked through the snapshot")
	}
	if second.Triggers[0].Repo == "mutated" {
		t.Fatal("palette triggers leaked through the snapshot")
	}
}

func TestValidateSandboxPaletteReportsProblems(t *testing.T) {
	broken := SandboxPalette{
		Version:  SandboxPaletteVersion + 1,
		Registry: SandboxPaletteRegistry,
		Variants: []SandboxVariant{
			{Variant: "coding", BaseImage: "ubuntu:24.04"},
			{Variant: "coding", BaseImage: "ubuntu:24.04"}, // duplicate
			{Variant: "", BaseImage: "ubuntu:24.04"},       // empty name
			{Variant: "bad-base", BaseImage: "no-tag"},     // neither variant nor tagged image
		},
		Triggers: []SandboxTriggerMapping{
			{Repo: "owner/repo", Variant: "ghost"},   // unknown variant
			{Repo: "owner/repo", Variant: "coding"},  // duplicate repo
			{Repo: "not-a-slug", Variant: "coding"},  // not a slug
			{Repo: "owner/other", Variant: "coding"}, // unknown default coverage
		},
	}
	errs := ValidateSandboxPalette(broken)
	joined := strings.Join(errs, "\n")
	for _, want := range []string{
		"unexpected version",
		"declared twice",
		"empty name",
		"neither a palette variant nor a tagged image",
		"unknown variant",
		"mapped twice",
		"not an <owner>/<repo> slug",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("validation did not report %q; got:\n%s", want, joined)
		}
	}
}

func TestRenderSandboxPaletteMarkdown(t *testing.T) {
	palette := SandboxPaletteSnapshot()
	md := RenderSandboxPaletteMarkdown(palette)
	for _, want := range []string{
		"Sandbox palette (ci#807)",
		"| variant |",
		"`coding`",
		"`tbscode/tims-nix`",
		"`nix`",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("rendered palette markdown is missing %q", want)
		}
	}
}
