package nativecoder

import (
	"fmt"
	"sort"
	"strings"
)

// ci#807: the sandbox palette.
//
// Historically the msgmate coder agent had only a handful of sandbox images,
// so repositories whose development process needs toolchains that are not part
// of the generic image (nix, kubernetes manifests, the Odoo stack, Android
// builds, VNC displays, ...) could not be worked on reliably - see e.g.
// tbscode/tims-nix#10 where the agent repeatedly complained about a missing
// nix toolchain.
//
// This file is the single source of truth for:
//
//   - the set of sandbox variants (and the commands each one guarantees),
//   - the mapping from a ci.msgmate.io git-repo trigger to the variant that
//     fixes that repository's development process,
//   - the naming scheme of the published per-variant images so that
//     `.github/workflows/build-sandbox-images.yaml` can publish them
//     individually.
//
// Keeping the palette in one place lets the publish workflow, the CI trigger
// configuration and the runtime image selection stay in sync.

const (
	// SandboxPaletteVersion is bumped whenever the palette changes in a way
	// that requires operators to revisit the ci.msgmate.io trigger configs.
	SandboxPaletteVersion = 1

	// SandboxPaletteRegistry is the placeholder image registry prefix used to
	// render a per-repository sandbox image reference. `<owner>/<repo>` is
	// replaced with the lower-cased repository slug.
	SandboxPaletteRegistry = "ghcr.io/<owner>/<repo>"

	// SandboxDefaultVariant is used for repositories that have no explicit
	// mapping in the palette.
	SandboxDefaultVariant = "coding"

	// SandboxTagPrefix prefixes every published sandbox image tag, so that
	// per-variant images can never collide with the regular application image.
	SandboxTagPrefix = "sandbox"
)

// SandboxVariant describes one published sandbox image.
type SandboxVariant struct {
	// Variant is the short, stable name (also used as the image tag suffix and
	// as the `sandboxes/<variant>` directory name in the docker sandbox
	// integration).
	Variant string
	// BaseImage is either the name of another palette variant this variant is
	// layered on top of, or a fully tagged upstream image (e.g. ubuntu:24.04).
	BaseImage string
	// Commands is the sorted list of commands the variant guarantees to the
	// coder agent.
	Commands []string
	// Description explains which kind of repository the variant targets.
	Description string
}

// SandboxTriggerMapping maps a ci.msgmate.io git-repo trigger to a variant.
type SandboxTriggerMapping struct {
	Repo    string
	Variant string
	// Note records why this repository needs a non-default variant.
	Note string
}

// SandboxPalette is the full, renderable palette.
type SandboxPalette struct {
	Version  int
	Registry string
	Variants []SandboxVariant
	Triggers []SandboxTriggerMapping
}

// sandboxPaletteVariants is the authoritative variant list. Keep
// .github/workflows/build-sandbox-images.yaml's ALL list in sync with it.
var sandboxPaletteVariants = []SandboxVariant{
	{
		Variant:     "minimal",
		BaseImage:   "ubuntu:24.04",
		Commands:    []string{"bash", "curl", "git", "jq"},
		Description: "Smallest OpenCode-free image for very light tasks and smoke tests.",
	},
	{
		Variant:     "coding",
		BaseImage:   "ubuntu:24.04",
		Commands:    []string{"bash", "curl", "git", "go", "gofmt", "jq", "node", "npm", "python3", "rg"},
		Description: "Generic Go/Node/TypeScript development (the default variant).",
	},
	{
		Variant:     "frontend",
		BaseImage:   "coding",
		Commands:    []string{"bash", "curl", "git", "gofmt", "jq", "node", "npm", "npx", "pnpm", "python3", "rg", "tsserver"},
		Description: "Frontend/TypeScript work needing browser tooling and TS servers.",
	},
	{
		Variant:     "nix",
		BaseImage:   "coding",
		Commands:    []string{"bash", "curl", "git", "gofmt", "go", "jq", "nix", "nix-build", "nix-shell", "rg"},
		Description: "Nix/NixOS repositories (fixes tbscode/tims-nix#10).",
	},
	{
		Variant:     "k8s",
		BaseImage:   "coding",
		Commands:    []string{"bash", "curl", "git", "helm", "jq", "kubectl", "kustomize", "rg", "yq"},
		Description: "Kubernetes/Helm/Kustomize manifest repositories.",
	},
	{
		Variant:     "odoo",
		BaseImage:   "coding",
		Commands:    []string{"bash", "curl", "git", "jq", "odoo", "pg_ctl", "pip3", "postgres", "python3", "rg"},
		Description: "Odoo stack development (used by odoo_sandbox_image).",
	},
	{
		Variant:     "android",
		BaseImage:   "coding",
		Commands:    []string{"adb", "bash", "curl", "git", "gradle", "java", "jq", "kotlin", "rg", "sdkmanager"},
		Description: "Android/Kotlin mobile client builds.",
	},
	{
		Variant:     "vnc",
		BaseImage:   "coding",
		Commands:    []string{"bash", "curl", "git", "jq", "rg", "websockify", "x11vnc", "xvfb"},
		Description: "Headless desktop/UI verification over VNC.",
	},
}

// sandboxPaletteTriggers maps the configured ci.msgmate.io git-repo triggers
// to the variant that fixes their development process.
var sandboxPaletteTriggers = []SandboxTriggerMapping{
	{Repo: "msgmate-io/open-chat-go", Variant: "coding", Note: "monorepo default"},
	{Repo: "msgmate-io/open-chat-go-ci", Variant: "coding", Note: "CI tooling"},
	{Repo: "msgmate-io/open-chat-go-mcp-integration", Variant: "coding", Note: "Go integration"},
	{Repo: "msgmate-io/open-chat-go-rest-api-tool", Variant: "coding", Note: "Go integration"},
	{Repo: "msgmate-io/open-chat-go-go-client", Variant: "coding", Note: "generated Go client"},
	{Repo: "msgmate-io/open-chat-go-frontend", Variant: "frontend", Note: "TypeScript frontend"},
	{Repo: "msgmate-io/open-chat-go-helm-chart", Variant: "k8s", Note: "Helm/K8s manifests"},
	{Repo: "msgmate-io/open-chat-go-odoo-integration", Variant: "odoo", Note: "Odoo stack"},
	{Repo: "msgmate-io/open-chat-go-mobile-client", Variant: "android", Note: "Android/Kotlin"},
	{Repo: "msgmate-io/open-chat-go-vnc-client", Variant: "vnc", Note: "VNC desktop"},
	{Repo: "tbscode/tims-nix", Variant: "nix", Note: "nix toolchain was missing (tims-nix#10)"},
}

// sandboxVariantExists reports whether variant is part of the palette.
func sandboxVariantExists(variant string) bool {
	variant = strings.ToLower(strings.TrimSpace(variant))
	for _, v := range sandboxPaletteVariants {
		if v.Variant == variant {
			return true
		}
	}
	return false
}

// SandboxVariantNames returns every palette variant name in declaration order.
func SandboxVariantNames() []string {
	names := make([]string, 0, len(sandboxPaletteVariants))
	for _, v := range sandboxPaletteVariants {
		names = append(names, v.Variant)
	}
	return names
}

// SandboxPaletteSnapshot returns a deep copy of the palette that callers may
// freely mutate.
func SandboxPaletteSnapshot() SandboxPalette {
	palette := SandboxPalette{
		Version:  SandboxPaletteVersion,
		Registry: SandboxPaletteRegistry,
		Variants: make([]SandboxVariant, 0, len(sandboxPaletteVariants)),
		Triggers: make([]SandboxTriggerMapping, 0, len(sandboxPaletteTriggers)),
	}
	for _, v := range sandboxPaletteVariants {
		copyVariant := v
		copyVariant.Commands = append([]string(nil), v.Commands...)
		palette.Variants = append(palette.Variants, copyVariant)
	}
	palette.Triggers = append(palette.Triggers, sandboxPaletteTriggers...)
	return palette
}

// SandboxVariantForRepo resolves the palette variant for a repository slug.
// The lookup is case-insensitive; the second return value reports whether an
// explicit mapping exists (otherwise the default variant applies).
func SandboxVariantForRepo(repo string) (string, bool) {
	repo = strings.ToLower(strings.TrimSpace(repo))
	if repo == "" {
		return SandboxDefaultVariant, false
	}
	for _, t := range sandboxPaletteTriggers {
		if t.Repo == repo {
			return t.Variant, true
		}
	}
	return SandboxDefaultVariant, false
}

// SandboxImageTags returns the tag (without registry or digest suffix) under
// which the given variant is published.
func SandboxImageTags(variant string) string {
	variant = strings.ToLower(strings.TrimSpace(variant))
	if !sandboxVariantExists(variant) {
		return ""
	}
	return fmt.Sprintf("%s-%s", SandboxTagPrefix, variant)
}

// SandboxImageRef renders the movable `-latest` image reference for a variant
// within a repository slug.
func SandboxImageRef(slug, variant string) string {
	if !sandboxVariantExists(variant) {
		return ""
	}
	slug = strings.ToLower(strings.TrimSpace(slug))
	if slug == "" || strings.Count(slug, "/") != 1 || strings.ContainsAny(slug, " \t") {
		return ""
	}
	registry := strings.ReplaceAll(SandboxPaletteRegistry, "<owner>/<repo>", slug)
	return fmt.Sprintf("%s:%s-latest", registry, SandboxImageTags(variant))
}

// SandboxCommandsForVariant returns the sorted commands a variant guarantees.
// It returns nil for unknown variants.
func SandboxCommandsForVariant(variant string) []string {
	variant = strings.ToLower(strings.TrimSpace(variant))
	for _, v := range sandboxPaletteVariants {
		if v.Variant == variant {
			commands := append([]string(nil), v.Commands...)
			sort.Strings(commands)
			return commands
		}
	}
	return nil
}

func sandboxIsSlug(repo string) bool {
	repo = strings.TrimSpace(repo)
	if repo == "" || strings.Count(repo, "/") != 1 {
		return false
	}
	owner, name := repo[:strings.Index(repo, "/")], repo[strings.Index(repo, "/")+1:]
	return owner != "" && name != "" && !strings.ContainsAny(repo, " \t")
}

// ValidateSandboxPalette returns a list of human readable problems, empty when
// the palette is consistent.
func ValidateSandboxPalette(palette SandboxPalette) []string {
	var problems []string
	if palette.Version != SandboxPaletteVersion {
		problems = append(problems, fmt.Sprintf("unexpected version %d, want %d", palette.Version, SandboxPaletteVersion))
	}
	if strings.TrimSpace(palette.Registry) == "" {
		problems = append(problems, "empty registry")
	}

	seen := make(map[string]bool, len(palette.Variants))
	for i, variant := range palette.Variants {
		name := strings.TrimSpace(variant.Variant)
		if name == "" {
			problems = append(problems, fmt.Sprintf("variant %d has an empty name", i))
			continue
		}
		if seen[name] {
			problems = append(problems, fmt.Sprintf("variant %q declared twice", name))
		}
		seen[name] = true
		base := strings.TrimSpace(variant.BaseImage)
		if base != "" && !strings.Contains(base, ":") && !seen[base] {
			// A base image is either an already declared palette variant or a
			// fully tagged upstream image.
			problems = append(problems, fmt.Sprintf("variant %q base image %q is neither a palette variant nor a tagged image", name, base))
		}
		if len(variant.Commands) == 0 {
			problems = append(problems, fmt.Sprintf("variant %q guarantees no commands", name))
		}
	}

	repos := make(map[string]bool, len(palette.Triggers))
	for _, trigger := range palette.Triggers {
		if !sandboxIsSlug(trigger.Repo) {
			problems = append(problems, fmt.Sprintf("trigger %q is not an <owner>/<repo> slug", trigger.Repo))
		}
		if !seen[trigger.Variant] {
			problems = append(problems, fmt.Sprintf("trigger %q maps to unknown variant %q", trigger.Repo, trigger.Variant))
		}
		if repos[trigger.Repo] {
			problems = append(problems, fmt.Sprintf("trigger %q mapped twice", trigger.Repo))
		}
		repos[trigger.Repo] = true
	}
	return problems
}

// SandboxPaletteGaps returns every configured repository that has no explicit
// trigger mapping, in the order it was supplied and without duplicates.
func SandboxPaletteGaps(palette SandboxPalette, configured []string) []string {
	mapped := make(map[string]bool, len(palette.Triggers))
	for _, trigger := range palette.Triggers {
		mapped[strings.ToLower(strings.TrimSpace(trigger.Repo))] = true
	}
	var gaps []string
	seen := make(map[string]bool, len(configured))
	for _, repo := range configured {
		repo = strings.ToLower(strings.TrimSpace(repo))
		if repo == "" || seen[repo] {
			continue
		}
		seen[repo] = true
		if !mapped[repo] {
			gaps = append(gaps, repo)
		}
	}
	return gaps
}

// RenderSandboxPaletteMarkdown renders the palette as a documentation snippet.
func RenderSandboxPaletteMarkdown(palette SandboxPalette) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Sandbox palette (ci#807)\n\n")
	fmt.Fprintf(&b, "Palette version: %d. Published as `%s:sandbox-<variant>-latest`.\n\n", palette.Version, palette.Registry)
	b.WriteString("## Variants\n\n")
	b.WriteString("| variant | base | commands | description |\n")
	b.WriteString("| --- | --- | --- | --- |\n")
	for _, variant := range palette.Variants {
		fmt.Fprintf(&b, "| `%s` | `%s` | %s | %s |\n",
			variant.Variant,
			variant.BaseImage,
			"`"+strings.Join(variant.Commands, "`, `")+"`",
			variant.Description,
		)
	}
	b.WriteString("\n## ci.msgmate.io git-repo triggers\n\n")
	b.WriteString("| repository | variant | note |\n")
	b.WriteString("| --- | --- | --- |\n")
	for _, trigger := range palette.Triggers {
		fmt.Fprintf(&b, "| `%s` | `%s` | %s |\n", trigger.Repo, trigger.Variant, trigger.Note)
	}
	fmt.Fprintf(&b, "\nRepositories without an entry use the default `%s` variant.\n", SandboxDefaultVariant)
	return b.String()
}
