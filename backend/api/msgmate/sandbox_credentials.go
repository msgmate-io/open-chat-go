package msgmate

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// This file is the **credential lifecycle** half of the sandbox-pool policy
// (ci#614, the follow-up to the shared-sandbox networking design of ci#546 and
// the credential provisioning of ci#497/ci#549).
//
// The failure mode: credential provisioning was a one-shot side effect of
// sandbox *creation* (the start hook fired with the SSH-server UUID). A pooled
// backing sandbox is started once and then reused by many chats, so the hook
// fires before any workspace exists, and a pool pod that restarts (rollout, OOM
// kill, node eviction, reaper reclaim) comes back with a fresh container $HOME
// while the sandbox record still points at the recreated pod. Every late-joining
// workspace then fails with "failed to re-provision credentials for sandbox ssh
// server <uuid> ... credentials not ready". ci#603 made each workspace run under
// its own OS user/HOME, so credentials must be (re)provisioned for the
// workspace's identity, not only for the pool login user.
//
// The policy implemented here resolves *when* those per-workspace credentials
// must be (re)provisioned. It carries no credential value: it is a
// reference/metadata-only policy, and the enforcement lives in the sandbox
// provider/integration (fed by this resolver, never by an inline value).
//
// Invariants kept by this file and pinned by its test:
//
//  1. scope defaults to `per_agent` (ci#546: per-agent scoped credentials only,
//     never one long-lived pool-level secret). It is not a second, competing
//     mode switch — the default is the ci#546 rule.
//  2. the effective TTL must stay shorter than the ci#616/ci#662 reaper idle TTL
//     (2700s), otherwise a live agent could attach with an already-expired
//     credential; the report says so instead of silently accepting it.
//  3. env wins over the per-agent shared config, for the ci#620 reason: the
//     shared-config default merge materialises a literal into every agent config
//     and would otherwise shadow the deployment lever forever.
//  4. fail open: every lever unset means today's behaviour (per-agent scope,
//     restart re-provision on, documented defaults), because an unconfigured
//     deployment must keep working.
//
// Keys: `coder_sandbox_credential_scope`, `coder_sandbox_credential_ttl_seconds`,
// `coder_sandbox_credential_refresh_ahead_seconds`,
// `coder_sandbox_credential_reprovision_on_restart` and
// `coder_sandbox_credential_max_reprovisions_per_window`, overridable
// deployment-wide through the `OCI_MSGMATE_CODER_SANDBOX_CREDENTIAL_*` levers.

const (
	// SandboxCredentialScopeEnvKey is the deployment-wide credential scope.
	SandboxCredentialScopeEnvKey = "OCI_MSGMATE_CODER_SANDBOX_CREDENTIAL_SCOPE"
	// SandboxCredentialTTLEnvKey is the deployment-wide credential TTL in
	// seconds (must stay below the reaper idle TTL).
	SandboxCredentialTTLEnvKey = "OCI_MSGMATE_CODER_SANDBOX_CREDENTIAL_TTL_SECONDS"
	// SandboxCredentialRefreshAheadEnvKey is how long before expiry the next
	// attach re-provisions, so a fresh shell is never handed a dying credential.
	SandboxCredentialRefreshAheadEnvKey = "OCI_MSGMATE_CODER_SANDBOX_CREDENTIAL_REFRESH_AHEAD_SECONDS"
	// SandboxCredentialReprovisionOnRestartEnvKey switches restart-driven
	// re-provisioning off (it is on by default, which is the ci#614 fix).
	SandboxCredentialReprovisionOnRestartEnvKey = "OCI_MSGMATE_CODER_SANDBOX_CREDENTIAL_REPROVISION_ON_RESTART"
	// SandboxCredentialMaxReprovisionsEnvKey bounds how often one workspace
	// identity may be re-provisioned inside one window: a mass pool restart must
	// be reported, not looped.
	SandboxCredentialMaxReprovisionsEnvKey = "OCI_MSGMATE_CODER_SANDBOX_CREDENTIAL_MAX_REPROVISIONS_PER_WINDOW"
)

const (
	sandboxCredentialScopeConfigKey        = "coder_sandbox_credential_scope"
	sandboxCredentialTTLConfigKey          = "coder_sandbox_credential_ttl_seconds"
	sandboxCredentialRefreshAheadConfigKey = "coder_sandbox_credential_refresh_ahead_seconds"
	sandboxCredentialOnRestartConfigKey    = "coder_sandbox_credential_reprovision_on_restart"
	sandboxCredentialMaxReplaysConfigKey   = "coder_sandbox_credential_max_reprovisions_per_window"
)

const (
	// SandboxCredentialScopePerAgent is the default (and ci#546 rule): one
	// credential per workspace identity.
	SandboxCredentialScopePerAgent = "per_agent"
	// SandboxCredentialScopePool scopes credentials to the pool's own login
	// identity. It exists for compatibility and is never the default.
	SandboxCredentialScopePool = "pool"
	// DefaultSandboxCredentialTTLSeconds is 1800s: shorter than the 2700s
	// reaper idle TTL, so a live agent never attaches with an expired credential.
	DefaultSandboxCredentialTTLSeconds = 1800
	// DefaultSandboxCredentialRefreshAheadSeconds re-provisions 300s before
	// expiry.
	DefaultSandboxCredentialRefreshAheadSeconds = 300
	// DefaultSandboxCredentialReprovisionWindowSeconds is the window the
	// reprovision bound counts in.
	DefaultSandboxCredentialReprovisionWindowSeconds = 600
	// DefaultSandboxCredentialMaxReprovisionsPerWindow bounds a restart
	// stampede per workspace identity.
	DefaultSandboxCredentialMaxReprovisionsPerWindow = 4
	// SandboxReaperIdleTTLSeconds is the ci#616/ci#662 reaper idle TTL: a sandbox
	// idle for longer than this is reclaimed. A credential TTL must stay
	// strictly below it, otherwise a live agent can attach with an already
	// expired credential.
	SandboxReaperIdleTTLSeconds int64 = 2700
)

// SandboxCredentialConfig is the resolved, operator-readable credential
// lifecycle policy. It contains no credential material: only durations, a scope
// name and the winning config sources.
type SandboxCredentialConfig struct {
	// Scope is SandboxCredentialScopePerAgent (default) or ...Pool.
	Scope string `json:"scope"`
	// TTLSeconds is the effective credential lifetime.
	TTLSeconds int64 `json:"effective_ttl_seconds"`
	// RefreshAheadSeconds is when re-provisioning starts before expiry.
	RefreshAheadSeconds int64 `json:"refresh_ahead_seconds"`
	// ReprovisionOnRestart is the ci#614 fix: replay provisioning for every live
	// workspace bound to a pool when the pool (re)starts.
	ReprovisionOnRestart bool `json:"reprovision_on_restart"`
	// MaxReprovisionsPerWindow bounds replays of one workspace identity.
	MaxReprovisionsPerWindow int `json:"max_reprovisions_per_window"`
	// ReprovisionWindowSeconds is the window that bound counts in.
	ReprovisionWindowSeconds int64 `json:"reprovision_window_seconds"`
	// TTLExceedsReaperIdleTTL is true when the configured TTL is not shorter
	// than the reaper idle TTL: a live agent could then attach with an expired
	// credential. Reported, never silently accepted.
	TTLExceedsReaperIdleTTL bool `json:"ttl_exceeds_reaper_idle_ttl"`
	// Source names the values that actually won (env:..., agent:..., default).
	Source string `json:"source"`
}

// TTL returns the effective credential lifetime.
func (c SandboxCredentialConfig) TTL() time.Duration {
	return time.Duration(c.TTLSeconds) * time.Second
}

// RefreshAhead returns when re-provisioning starts before expiry.
func (c SandboxCredentialConfig) RefreshAhead() time.Duration {
	return time.Duration(c.RefreshAheadSeconds) * time.Second
}

// ReprovisionWindow returns the window the reprovision bound counts in.
func (c SandboxCredentialConfig) ReprovisionWindow() time.Duration {
	return time.Duration(c.ReprovisionWindowSeconds) * time.Second
}

// PerAgent reports whether credentials are scoped per workspace identity.
func (c SandboxCredentialConfig) PerAgent() bool {
	return c.Scope != SandboxCredentialScopePool
}

// String renders the policy as one operator-readable line.
func (c SandboxCredentialConfig) String() string {
	return fmt.Sprintf(
		"scope=%s effective_ttl_seconds=%d refresh_ahead_seconds=%d reprovision_on_restart=%t max_reprovisions_per_window=%d window_seconds=%d ttl_exceeds_reaper_idle_ttl=%t source=%s",
		c.Scope, c.TTLSeconds, c.RefreshAheadSeconds, c.ReprovisionOnRestart,
		c.MaxReprovisionsPerWindow, c.ReprovisionWindowSeconds,
		c.TTLExceedsReaperIdleTTL, c.Source,
	)
}

// ReportLines renders the `sandbox_credentials key=value` block of the CI
// report. It is reference/metadata-only: no token, no credential path beyond the
// policy knobs.
func (c SandboxCredentialConfig) ReportLines() []string {
	lines := []string{
		fmt.Sprintf("sandbox_credentials scope=%s reprovision_on_restart=%t effective_ttl_seconds=%d refresh_ahead_seconds=%d source=%s",
			c.Scope, c.ReprovisionOnRestart, c.TTLSeconds, c.RefreshAheadSeconds, c.Source),
		fmt.Sprintf("sandbox_credentials max_reprovisions_per_window=%d window_seconds=%d",
			c.MaxReprovisionsPerWindow, c.ReprovisionWindowSeconds),
	}
	if c.TTLExceedsReaperIdleTTL {
		lines = append(lines, fmt.Sprintf(
			"sandbox_credentials warning=ttl_not_shorter_than_reaper_idle_ttl effective_ttl_seconds=%d reaper_idle_ttl_seconds=%d",
			c.TTLSeconds, SandboxReaperIdleTTLSeconds))
	}
	return lines
}

// ResolveSandboxCredentialConfig resolves the credential lifecycle policy
// (ci#614). It is fail-open: unset levers keep the documented defaults, which
// are today's behaviour (per-agent scope, restart re-provisioning on).
func ResolveSandboxCredentialConfig(config map[string]interface{}) SandboxCredentialConfig {
	resolved := SandboxCredentialConfig{
		Scope:                    SandboxCredentialScopePerAgent,
		TTLSeconds:               DefaultSandboxCredentialTTLSeconds,
		RefreshAheadSeconds:      DefaultSandboxCredentialRefreshAheadSeconds,
		ReprovisionOnRestart:     true,
		MaxReprovisionsPerWindow: DefaultSandboxCredentialMaxReprovisionsPerWindow,
		ReprovisionWindowSeconds: DefaultSandboxCredentialReprovisionWindowSeconds,
	}
	sources := make([]string, 0, 5)

	if raw, source := sandboxCredentialString(config, SandboxCredentialScopeEnvKey, sandboxCredentialScopeConfigKey); raw != "" {
		if scope, ok := normalizeSandboxCredentialScope(raw); ok {
			resolved.Scope = scope
			sources = append(sources, source)
		}
	}
	if seconds, source := sandboxCredentialDuration(config, SandboxCredentialTTLEnvKey, sandboxCredentialTTLConfigKey); seconds > 0 {
		resolved.TTLSeconds = seconds
		sources = append(sources, source)
	}
	if seconds, source := sandboxCredentialDuration(config, SandboxCredentialRefreshAheadEnvKey, sandboxCredentialRefreshAheadConfigKey); seconds > 0 {
		resolved.RefreshAheadSeconds = seconds
		sources = append(sources, source)
	}
	if onRestart, source := sandboxCredentialBool(config, SandboxCredentialReprovisionOnRestartEnvKey, sandboxCredentialOnRestartConfigKey); source != "" {
		resolved.ReprovisionOnRestart = onRestart
		sources = append(sources, source)
	}
	if max, source := sandboxCredentialInt(config, SandboxCredentialMaxReprovisionsEnvKey, sandboxCredentialMaxReplaysConfigKey); max > 0 {
		resolved.MaxReprovisionsPerWindow = max
		sources = append(sources, source)
	}

	// A refresh-ahead window that is not strictly inside the TTL would make
	// every attach re-provision; halve it instead and say so.
	if resolved.RefreshAheadSeconds >= resolved.TTLSeconds {
		resolved.RefreshAheadSeconds = resolved.TTLSeconds / 2
		sources = append(sources, "refresh_ahead=clamped_to_half_ttl")
	}
	resolved.TTLExceedsReaperIdleTTL = resolved.TTLSeconds >= SandboxReaperIdleTTLSeconds

	if len(sources) == 0 {
		resolved.Source = "default"
	} else {
		resolved.Source = strings.Join(sources, "+")
	}
	return resolved
}

// normalizeSandboxCredentialScope accepts the two documented scope names; an
// unknown value falls back to the ci#546 per-agent rule.
func normalizeSandboxCredentialScope(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case SandboxCredentialScopePool:
		return SandboxCredentialScopePool, true
	case SandboxCredentialScopePerAgent, "per-agent", "agent":
		return SandboxCredentialScopePerAgent, true
	}
	return "", false
}

// sandboxCredentialString resolves one string lever: env wins, then per-agent
// shared config.
func sandboxCredentialString(config map[string]interface{}, envKey, configKey string) (string, string) {
	if raw := strings.TrimSpace(os.Getenv(envKey)); raw != "" {
		return raw, "env:" + envKey
	}
	if config != nil {
		if raw, ok := config[configKey].(string); ok && strings.TrimSpace(raw) != "" {
			return strings.TrimSpace(raw), "agent:" + configKey
		}
	}
	return "", ""
}

// sandboxCredentialDuration resolves one duration-in-seconds lever (env wins),
// returning 0 for unset/unparsable values so the default stays in force.
func sandboxCredentialDuration(config map[string]interface{}, envKey, configKey string) (int64, string) {
	if raw, source := sandboxCredentialString(config, envKey, configKey); raw != "" {
		if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil && seconds > 0 {
			return seconds, source
		}
	}
	return 0, ""
}

// sandboxCredentialInt resolves one positive-int lever (env wins).
func sandboxCredentialInt(config map[string]interface{}, envKey, configKey string) (int, string) {
	if raw, source := sandboxCredentialString(config, envKey, configKey); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil && value > 0 {
			return value, source
		}
	}
	return 0, ""
}

// sandboxCredentialBool resolves one boolean lever (env wins). The source is
// empty when nothing was configured, so the caller keeps its default.
func sandboxCredentialBool(config map[string]interface{}, envKey, configKey string) (bool, string) {
	raw, source := sandboxCredentialString(config, envKey, configKey)
	if raw == "" {
		return false, ""
	}
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "on", "enabled":
		return true, source
	case "0", "false", "no", "off", "disabled":
		return false, source
	}
	// An unparsable value must not silently disable re-provisioning.
	return true, source + "=unparsed_default_true"
}
