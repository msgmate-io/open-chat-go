package nativecoder

import "strings"

// ci#807: the sandbox palette.
//
// The native coder agents provision a per-chat sandbox from a *template* image.
// Until now a single image had to serve every repository (the OpenCode-free
// `minimal` template, ci#465), so an agent working on a repository whose build
// needs a heavier toolchain (Go + the integration manager, the Android SDK, Nix,
// a browser/VNC desktop) started in a sandbox that simply lacks the commands the
// task needs. The palette fixes that by naming a small set of purpose-built
// sandbox images and mapping each known development repository to the template
// its work actually needs.
//
// The core side of the hand-off mirrors the repository hand-off (ci#413) and the
// development set-up hand-off (ci#756): the dispatch path resolves the template
// for the interaction (an explicit `coder_sandbox_template` key or
// `sandbox:<template>` tag wins, otherwise the palette default for the resolved
// repository) and binds it into the interaction's shared config. The
// docker_sandbox integration keeps owning the actual provisioning and only ever
// sees a fully resolved `coder_sandbox_template`.
//
// This list is the single source of truth for the *core*; the publish workflow
// (.github/workflows/build-sandbox-images.yaml) carries a PALETTE_TEMPLATES
// mirror that a unit test keeps in sync with this slice, so a template added
// here can never silently miss its published image.
const (
	// SandboxTemplateConfigKey is the shared-config key the docker_sandbox
	// integration reads to pick the sandbox image/template for an interaction.
	SandboxTemplateConfigKey = "coder_sandbox_template"
	// SandboxTemplateMinimal is the image with no toolchain beyond a shell: the
	// safe fallback for a repository the palette does not know.
	SandboxTemplateMinimal = "minimal"
	// SandboxTemplateDefault is the template used when neither the caller nor
	// the palette names one.
	SandboxTemplateDefault = SandboxTemplateMinimal
)

// SandboxPaletteEntry is one sandbox image of the palette: the template name the
// docker_sandbox integration understands, what it is for, and the repository
// prefixes whose default development work it serves.
type SandboxPaletteEntry struct {
	// Template is the template/image discriminator (`coding`, `nix`, …), ie the
	// value of `coder_sandbox_template` and the `sandbox-<template>-latest` tag
	// of the published image.
	Template string
	// Label is a short human name for UIs and documentation.
	Label string
	// Purpose is one sentence describing the toolchain the image ships.
	Purpose string
	// RepoPrefixes are the `owner/name` prefixes this template is the default
	// for. The longest matching prefix wins, so a more specific entry overrides
	// a broader one (mirroring DefaultDevSetupProfiles).
	RepoPrefixes []string
}

// DefaultSandboxPalette is the set of sandbox images the fleet publishes and the
// per-repository default mapping. The first entry is the fallback for an unknown
// repository.
var DefaultSandboxPalette = []SandboxPaletteEntry{
	{
		Template: SandboxTemplateMinimal,
		Label:    "Minimal",
		Purpose:  "shell-only sandbox for file inspection, git archaeology and quick scripted checks; the safe fallback for an unknown repository",
	},
	{
		Template: "coding",
		Label:    "Coding",
		Purpose:  "Go, Node and Python toolchains plus the build tools (helm, kubectl, the integration manager) a monorepo/coder/CI/Helm checkout needs to build and test",
		RepoPrefixes: []string{
			"msgmate-io/open-chat-go-mobile",
			"msgmate-io/open-chat-go-ci",
			"msgmate-io/open-chat-go-helm-chart",
			"msgmate-io/open-chat-go-llm-coding-context",
			"msgmate-io/open-chat-go-build-tools",
			"msgmate-io/open-chat-go-docker-sandbox-integration",
			"msgmate-io/open-chat-go-msgmate-coder",
			"msgmate-io/open-chat-go",
		},
	},
	{
		Template: "nix",
		Label:    "Nix",
		Purpose:  "Nix flakes with the nix-daemon available, for repositories whose development shell (nix develop / nix build) is the build entry point",
		RepoPrefixes: []string{
			"tbscode/tims-nix",
		},
	},
	{
		Template: "android",
		Label:    "Android",
		Purpose:  "JDK, the Android SDK and gomobile for the mobile client (full-android profile) builds and instrumented tests",
		RepoPrefixes: []string{
			"msgmate-io/open-chat-go-android",
		},
	},
	{
		Template: "vnc",
		Label:    "VNC desktop",
		Purpose:  "a browser/VNC desktop for tasks that drive a graphical surface (UI walkthroughs, screenshots, manual QA of a deployed instance)",
	},
	{
		Template: "odoo",
		Label:    "Odoo",
		Purpose:  "the Odoo application stack (Python, PostgreSQL client, the Odoo CLI) for Odoo module development",
	},
}

// SandboxPaletteTemplates returns the template names of the palette, in order.
func SandboxPaletteTemplates() []string {
	out := make([]string, 0, len(DefaultSandboxPalette))
	for _, entry := range DefaultSandboxPalette {
		out = append(out, entry.Template)
	}
	return out
}

// SandboxPaletteEntryFor returns the palette entry for a template name,
// comparing case-insensitively and ignoring surrounding whitespace.
func SandboxPaletteEntryFor(template string) (SandboxPaletteEntry, bool) {
	normalized := normalizeSandboxTemplate(template)
	if normalized == "" {
		return SandboxPaletteEntry{}, false
	}
	for _, entry := range DefaultSandboxPalette {
		if entry.Template == normalized {
			return entry, true
		}
	}
	return SandboxPaletteEntry{}, false
}

// IsKnownSandboxTemplate reports whether a value names a palette template. An
// unknown template is ignored everywhere so a typo degrades to the configured
// default image instead of failing a whole sandbox provisioning.
func IsKnownSandboxTemplate(template string) bool {
	_, ok := SandboxPaletteEntryFor(template)
	return ok
}

// normalizeSandboxTemplate canonicalises a template value: trimmed and
// lower-cased (a template is an image-name fragment, never case-sensitive).
func normalizeSandboxTemplate(template string) string {
	return strings.ToLower(strings.TrimSpace(template))
}

// SandboxTemplateForRepo returns the palette default template for a repository,
// or "" when the palette does not know it. The longest matching prefix wins.
func SandboxTemplateForRepo(repo string) string {
	normalized := strings.ToLower(NormalizeRepo(repo))
	if normalized == "" {
		return ""
	}
	best := ""
	bestLen := -1
	for _, entry := range DefaultSandboxPalette {
		for _, prefix := range entry.RepoPrefixes {
			candidate := strings.ToLower(strings.TrimSpace(prefix))
			if candidate == "" || !strings.HasPrefix(normalized, candidate) {
				continue
			}
			if len(candidate) > bestLen {
				bestLen = len(candidate)
				best = entry.Template
			}
		}
	}
	return best
}

// SandboxTemplateValueFromTags returns the template named by the first
// `sandbox:<template>` tag, or "" when no tag names a known template.
func SandboxTemplateValueFromTags(tags []string) string {
	value := tagValueWithPrefix(tags, SandboxTagPrefix)
	if !IsKnownSandboxTemplate(value) {
		return ""
	}
	return normalizeSandboxTemplate(value)
}

// SandboxTemplateFromConfig returns the template a shared config declares
// explicitly, or "" when it declares none (or an unknown one).
func SandboxTemplateFromConfig(config map[string]interface{}) string {
	if config == nil {
		return ""
	}
	value, _ := config[SandboxTemplateConfigKey].(string)
	if !IsKnownSandboxTemplate(value) {
		return ""
	}
	return normalizeSandboxTemplate(value)
}

// SandboxTemplateForInteraction resolves the sandbox template an interaction
// should be provisioned with, in the same precedence order as the repository
// hand-off (ci#413):
//
//  1. a template already declared by the effective config (bot/trigger default)
//     or by the explicit config overrides is pinned by the caller, so "" is
//     returned and the dispatch leaves it alone;
//  2. an explicit `sandbox:<template>` tag wins over the configured repository;
//  3. otherwise the palette default for the repository the interaction works on
//     (`repo:<...>` tag else the config's repo key) applies.
//
// "" means the interaction keeps the deployment default image.
func SandboxTemplateForInteraction(config map[string]interface{}, tags []string, configOverrides map[string]interface{}) string {
	if SandboxTemplateFromConfig(config) != "" || SandboxTemplateFromConfig(configOverrides) != "" {
		return ""
	}
	if template := SandboxTemplateValueFromTags(tags); template != "" {
		return template
	}
	return SandboxTemplateForRepo(AutoSandboxRepoTarget(config, tags))
}

// WithAutoSandboxTemplate returns config whose `coder_sandbox_template` names
// the palette template the automatic per-chat sandbox should be provisioned
// from. It mirrors WithDevSetup/WithAutoSandboxRepo: a non-coder config and a
// config that already declares a template are returned unchanged, an unknown
// template is ignored, and the caller's map is copied, never mutated.
func WithAutoSandboxTemplate(config map[string]interface{}, template string) map[string]interface{} {
	if config == nil {
		return nil
	}
	normalized := normalizeSandboxTemplate(template)
	if normalized == "" || !IsKnownSandboxTemplate(normalized) {
		return config
	}
	if _, exists := config[SandboxTemplateConfigKey]; exists {
		return config
	}
	if !IsCoderAgentConfig(config) {
		return config
	}
	merged := make(map[string]interface{}, len(config)+1)
	for key, value := range config {
		merged[key] = value
	}
	merged[SandboxTemplateConfigKey] = normalized
	return merged
}
