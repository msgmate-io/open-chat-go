package sandboxsession

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// This file implements the ci#614 re-provisioning replay for pooled sandboxes,
// the follow-up to the ci#546 shared-sandbox networking design and the ci#497 /
// ci#549 credential provisioning.
//
// The failure mode it fixes: credential provisioning was a one-shot side effect
// of sandbox *creation*. A pooled backing sandbox is started once and then
// reused by many chats, so the start hook fires before any workspace exists, and
// a pool pod that restarts (rollout, OOM kill, node eviction, reaper reclaim)
// comes back with a fresh container $HOME while the sandbox record still points
// at the recreated pod. Every late-joining workspace then fails with
// "failed to re-provision credentials for sandbox ssh server <uuid> ... credentials
// not ready". ci#603 made each workspace run under its own OS user/HOME, so
// credentials must be (re)provisioned for the workspace's identity, not only for
// the pool login user.
//
// The rule enforced here, and pinned by reprovision_test.go:
//
//  1. when a pool (re)starts, provisioning is *replayed* for every live workspace
//     bound to that pool, into each workspace's own per-agent HOME;
//  2. the replay never mints a new lease: the workspace keeps the binding it
//     already had, which is why "an existing shared workspace can gh/git push
//     again without a new lease";
//  3. credentials are carried as *references* (CredentialRef): a name, an env-var
//     list, a source key and a per-agent HOME path. A literal token in a
//     reference is rejected, so nothing here can log or expose the token;
//  4. a fresh shell is authenticated before any `gh` config exists because the
//     session-scoped GH_TOKEN/GITHUB_TOKEN exports are re-applied to the session
//     environment (never to argv, so the token never reaches the sandbox process
//     list).

// Sentinel errors callers can match with errors.Is.
var (
	// ErrInvalidCredentialRef marks a credential reference that is not usable as
	// a reference, eg because it embeds a literal secret value.
	ErrInvalidCredentialRef = errors.New("sandboxsession: invalid credential reference")
	// ErrInvalidPoolBinding marks a pool binding without a workspace identity.
	ErrInvalidPoolBinding = errors.New("sandboxsession: invalid pool binding")
	// ErrSecretInArgv marks a shell argv that would put a credential value into
	// the sandbox process list.
	ErrSecretInArgv = errors.New("sandboxsession: credential value in shell argv")
	// ErrCredentialUnavailable marks a credential the resolver could not produce.
	ErrCredentialUnavailable = errors.New("sandboxsession: credential unavailable")
)

// CredentialRef identifies a per-workspace credential the pool must (re)provision
// on attach and on every pool restart.
//
// It is a reference only: it never carries the credential value. The value is
// resolved by the provider at execute time from Source, written to the
// per-agent HOME path and exported into the session environment.
type CredentialRef struct {
	// Name identifies the credential, eg "github".
	Name string
	// EnvVars are the session-scoped variables the credential is exported as for
	// a fresh shell, eg ["GITHUB_TOKEN", "GH_TOKEN"]. Names only.
	EnvVars []string
	// Source is the integration-side secret source key the provider resolves the
	// value from. It is a reference: a literal secret value is rejected.
	Source string
	// HomeRelPath is the file path relative to the workspace's per-agent HOME
	// that receives the credential config, eg ".config/gh/hosts.yml".
	HomeRelPath string
}

// Validate rejects references that are not usable as references: an empty name,
// no env var, an empty source or a source/env value that looks like a literal
// secret. It is the guard that keeps the token out of logs and out of this
// package's serialised forms.
func (c CredentialRef) Validate() error {
	if strings.TrimSpace(c.Name) == "" {
		return fmt.Errorf("%w: name is empty", ErrInvalidCredentialRef)
	}
	if strings.TrimSpace(c.Source) == "" {
		return fmt.Errorf("%w: %s: source is empty", ErrInvalidCredentialRef, c.Name)
	}
	if LooksLikeSecretValue(c.Source) {
		return fmt.Errorf("%w: %s: source must be a reference, not a literal secret", ErrInvalidCredentialRef, c.Name)
	}
	if len(c.EnvVars) == 0 {
		return fmt.Errorf("%w: %s: no session env vars", ErrInvalidCredentialRef, c.Name)
	}
	for i, env := range c.EnvVars {
		if strings.TrimSpace(env) == "" {
			return fmt.Errorf("%w: %s: env_vars[%d] is empty", ErrInvalidCredentialRef, c.Name, i)
		}
		if LooksLikeSecretValue(env) {
			return fmt.Errorf("%w: %s: env_vars[%d] must be a name, not a literal value", ErrInvalidCredentialRef, c.Name, i)
		}
		if strings.ContainsAny(env, " \t=") {
			return fmt.Errorf("%w: %s: env_vars[%d] is not a variable name", ErrInvalidCredentialRef, c.Name, i)
		}
	}
	if c.HomeRelPath != "" {
		if strings.HasPrefix(c.HomeRelPath, "/") {
			return fmt.Errorf("%w: %s: home_rel_path %q must be relative to the per-agent HOME", ErrInvalidCredentialRef, c.Name, c.HomeRelPath)
		}
		if strings.Contains(c.HomeRelPath, "..") {
			return fmt.Errorf("%w: %s: home_rel_path %q must not traverse", ErrInvalidCredentialRef, c.Name, c.HomeRelPath)
		}
	}
	return nil
}

// WorkspaceIdentity is the ci#603 per-workspace OS identity. Credentials are
// provisioned for this identity, not for the pool login user.
type WorkspaceIdentity struct {
	// WorkspaceUUID identifies the workspace (and its lease).
	WorkspaceUUID string
	// OwnerUUID is the user the workspace belongs to.
	OwnerUUID string
	// OSUser is the ci#603 per-workspace OS user.
	OSUser string
	// Home is the workspace's own HOME. It is empty until the workspace has been
	// provisioned (a fresh pool pod has none).
	Home string
}

// Validate rejects an identity that cannot be provisioned for.
func (w WorkspaceIdentity) Validate() error {
	if strings.TrimSpace(w.WorkspaceUUID) == "" {
		return fmt.Errorf("%w: workspace_uuid is empty", ErrInvalidPoolBinding)
	}
	if strings.TrimSpace(w.Home) == "" {
		return fmt.Errorf("%w: workspace %s: home is empty", ErrInvalidPoolBinding, w.WorkspaceUUID)
	}
	if !strings.HasPrefix(w.Home, "/") {
		return fmt.Errorf("%w: workspace %s: home %q must be absolute", ErrInvalidPoolBinding, w.WorkspaceUUID, w.Home)
	}
	return nil
}

// CredentialPaths returns the absolute per-agent HOME paths of the binding's
// credential files. Paths only: never contents.
func (w WorkspaceIdentity) CredentialPaths(creds []CredentialRef) []string {
	out := make([]string, 0, len(creds))
	for _, cred := range creds {
		if cred.HomeRelPath == "" {
			continue
		}
		out = append(out, JoinHomePath(w.Home, cred.HomeRelPath))
	}
	sort.Strings(out)
	return out
}

// JoinHomePath joins a per-agent HOME with a credential's relative path.
func JoinHomePath(home, rel string) string {
	home = strings.TrimSuffix(home, "/")
	if home == "" {
		return rel
	}
	if rel == "" {
		return home
	}
	return home + "/" + strings.TrimPrefix(rel, "/")
}

// PoolBinding is one live workspace bound to a pooled sandbox.
//
// A pool restart replays provisioning for every binding that is not released:
// the binding (and the lease behind it) is reused as-is, so no new lease is
// minted and the attach still works.
type PoolBinding struct {
	// Identity is the workspace identity the credentials belong to.
	Identity WorkspaceIdentity
	// Credentials are the references to (re)provision for that identity.
	Credentials []CredentialRef
	// BoundAt is when the workspace bound to the pool.
	BoundAt time.Time
	// LastReplayedAt is when this identity was last re-provisioned successfully.
	// Zero means "never replayed", which a restart must always serve.
	LastReplayedAt time.Time
	// LastReplayContainerID identifies the container generation the last replay
	// served. It is what makes a restart replay idempotent: re-observing the
	// *same* generation is a no-op, while a restart into a fresh container (new
	// ID, empty $HOME) is always replayed — regardless of how recently the
	// workspace was served.
	LastReplayContainerID string
	// ReplayCount is how often this identity was replayed inside the current
	// window; ReplayWindowStartedAt is when that window opened. Together they
	// bound a restart stampede instead of letting it loop.
	ReplayCount           int
	ReplayWindowStartedAt time.Time
	// Released marks a workspace that gave up its pool binding (lease ended or
	// workspace deleted). Released bindings are never replayed.
	Released bool
}

// Validate rejects a binding without a usable identity or credential reference.
func (b PoolBinding) Validate() error {
	if err := b.Identity.Validate(); err != nil {
		return err
	}
	if len(b.Credentials) == 0 {
		return fmt.Errorf("%w: workspace %s: no credentials", ErrInvalidPoolBinding, b.Identity.WorkspaceUUID)
	}
	for _, cred := range b.Credentials {
		if err := cred.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// Live reports whether a restart must replay provisioning for this binding.
func (b PoolBinding) Live() bool { return !b.Released }

// ReplayPolicy bounds restart-driven re-provisioning so a mass pool restart is
// reported instead of looped.
type ReplayPolicy struct {
	// ReprovisionOnRestart is the ci#614 fix. False reports instead of replaying.
	ReprovisionOnRestart bool
	// RefreshAhead suppresses a replay that happened this recently: the
	// credential is still fresh, so a second restart is a no-op.
	RefreshAhead time.Duration
	// MaxPerWindow bounds replays of one workspace identity inside Window.
	MaxPerWindow int
	// Window is the window MaxPerWindow counts in.
	Window time.Duration
	// Now defaults to time.Now.
	Now func() time.Time
}

// DefaultReplayPolicy is the policy used when a caller does not configure one:
// restart re-provisioning is on, a replay inside the 300s refresh-ahead window
// is suppressed and one identity is replayed at most 4 times per 600s window.
func DefaultReplayPolicy() ReplayPolicy {
	return ReplayPolicy{
		ReprovisionOnRestart: true,
		RefreshAhead:         300 * time.Second,
		MaxPerWindow:         4,
		Window:               600 * time.Second,
		Now:                  time.Now,
	}
}

func (p ReplayPolicy) now() time.Time {
	if p.Now == nil {
		return time.Now()
	}
	return p.Now()
}

// PoolRestart describes a pool (re)start. ContainerID/Generation identity the
// fresh container whose $HOME is empty: reusing it marks a replay that already
// happened for that generation.
type PoolRestart struct {
	// Provider is the sandbox provider name, eg "docker_sandbox".
	Provider string
	// SandboxUUID is the pooled sandbox that (re)started.
	SandboxUUID string
	// ContainerID identifies the restarted container/generation.
	ContainerID string
	// OccurredAt is when the restart was observed. Zero means now.
	OccurredAt time.Time
}

// ReplayTarget is one workspace the restart must re-provision, with the exact
// per-agent HOME paths written and the session env names exported.
type ReplayTarget struct {
	Identity WorkspaceIdentity
	// Credentials are the references to (re)provision.
	Credentials []CredentialRef
	// HomePaths are the absolute per-agent HOME credential paths. Paths only.
	HomePaths []string
	// EnvVars are the session-scoped names re-exported for a fresh shell.
	EnvVars []string
	// BoundAt is unchanged from the binding: no new lease is minted by a replay.
	BoundAt time.Time
}

// DeferredReplay is a binding a restart did not replay, with the reason.
type DeferredReplay struct {
	Identity WorkspaceIdentity
	Reason   string
}

// ReplayPlan is what a pool (re)start must replay. It is safe to log: it carries
// identity and paths, never a credential value.
type ReplayPlan struct {
	Provider    string
	SandboxUUID string
	ContainerID string
	OccurredAt  time.Time
	// Policy is the policy that produced the plan, for the CI report.
	Policy ReplayPolicy
	// Targets are the workspaces to re-provision now.
	Targets []ReplayTarget
	// Deferred are the live bindings that were intentionally skipped.
	Deferred []DeferredReplay
	// SkippedReleased counts released bindings, which are never replayed.
	SkippedReleased int
}

// Empty reports whether the restart had nothing to replay.
func (p ReplayPlan) Empty() bool { return len(p.Targets) == 0 }

// ReportLines renders the `sandbox_credentials_replay key=value` block of the
// CI report: counts and reasons only.
func (p ReplayPlan) ReportLines() []string {
	lines := []string{
		fmt.Sprintf("sandbox_credentials_replay provider=%s sandbox_uuid=%s targets=%d deferred=%d skipped_released=%d reprovision_on_restart=%t",
			p.Provider, p.SandboxUUID, len(p.Targets), len(p.Deferred), p.SkippedReleased, p.Policy.ReprovisionOnRestart),
	}
	for _, target := range p.Targets {
		lines = append(lines, fmt.Sprintf("sandbox_credentials_replay target workspace_uuid=%s os_user=%s home=%s env=%s",
			target.Identity.WorkspaceUUID, target.Identity.OSUser, target.Identity.Home, strings.Join(target.EnvVars, ",")))
	}
	for _, deferred := range p.Deferred {
		lines = append(lines, fmt.Sprintf("sandbox_credentials_replay deferred workspace_uuid=%s reason=%s",
			deferred.Identity.WorkspaceUUID, deferred.Reason))
	}
	return lines
}

// PoolRegistry tracks the live workspace bindings per pooled sandbox and plans
// the restart replay. It is the pool-side counterpart of the one-shot start hook:
// bindings are recorded on attach, so a restart knows every identity to serve.
type PoolRegistry struct {
	mu       sync.Mutex
	bindings map[string][]PoolBinding
}

// NewPoolRegistry returns an empty registry.
func NewPoolRegistry() *PoolRegistry {
	return &PoolRegistry{bindings: make(map[string][]PoolBinding)}
}

func poolKey(provider, sandboxUUID string) string {
	return provider + "/" + sandboxUUID
}

// Bind records or refreshes the binding of one workspace to a pooled sandbox.
// Rebinding the same identity keeps the original BoundAt/LastReplayedAt so a
// restart cannot be used to bypass the replay bound.
func (r *PoolRegistry) Bind(provider, sandboxUUID string, binding PoolBinding) error {
	if err := binding.Validate(); err != nil {
		return err
	}
	key := poolKey(provider, sandboxUUID)
	r.mu.Lock()
	defer r.mu.Unlock()
	existing := r.bindings[key]
	for i, current := range existing {
		if current.Identity.WorkspaceUUID != binding.Identity.WorkspaceUUID {
			continue
		}
		if binding.BoundAt.IsZero() {
			binding.BoundAt = current.BoundAt
		}
		if binding.LastReplayedAt.IsZero() {
			binding.LastReplayedAt = current.LastReplayedAt
		}
		existing[i] = binding
		r.bindings[key] = existing
		return nil
	}
	if binding.BoundAt.IsZero() {
		binding.BoundAt = time.Now()
	}
	r.bindings[key] = append(existing, binding)
	return nil
}

// Unbind drops one workspace binding, eg when its lease ends.
func (r *PoolRegistry) Unbind(provider, sandboxUUID, workspaceUUID string) {
	key := poolKey(provider, sandboxUUID)
	r.mu.Lock()
	defer r.mu.Unlock()
	existing := r.bindings[key]
	kept := existing[:0]
	for _, binding := range existing {
		if binding.Identity.WorkspaceUUID == workspaceUUID {
			continue
		}
		kept = append(kept, binding)
	}
	if len(kept) == 0 {
		delete(r.bindings, key)
		return
	}
	r.bindings[key] = kept
}

// Bindings returns a copy of the pool's bindings, oldest first.
func (r *PoolRegistry) Bindings(provider, sandboxUUID string) []PoolBinding {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := append([]PoolBinding(nil), r.bindings[poolKey(provider, sandboxUUID)]...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].BoundAt.Before(out[j].BoundAt) })
	return out
}

// BindingsFor returns the bindings of one workspace across every pool.
func (r *PoolRegistry) BindingsFor(workspaceUUID string) []PoolBinding {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []PoolBinding
	for _, bindings := range r.bindings {
		for _, binding := range bindings {
			if binding.Identity.WorkspaceUUID == workspaceUUID {
				out = append(out, binding)
			}
		}
	}
	return out
}

// RecordReplay marks a successful replay for one identity: it stamps the
// container generation served, refreshes the replay timestamp and advances the
// per-window counter so a mass restart cannot loop. It is the hook a provider
// calls after re-provisioning succeeded, and it never sees a credential value.
func (r *PoolRegistry) RecordReplay(provider, sandboxUUID, workspaceUUID, containerID string, at time.Time, policy ReplayPolicy) bool {
	key := poolKey(provider, sandboxUUID)
	r.mu.Lock()
	defer r.mu.Unlock()
	bindings := r.bindings[key]
	for i := range bindings {
		if bindings[i].Identity.WorkspaceUUID != workspaceUUID {
			continue
		}
		if !bindings[i].ReplayWindowStartedAt.IsZero() && policy.Window > 0 &&
			at.Sub(bindings[i].ReplayWindowStartedAt) >= policy.Window {
			bindings[i].ReplayWindowStartedAt = time.Time{}
			bindings[i].ReplayCount = 0
		}
		if bindings[i].ReplayWindowStartedAt.IsZero() {
			bindings[i].ReplayWindowStartedAt = at
		}
		bindings[i].ReplayCount++
		bindings[i].LastReplayedAt = at
		bindings[i].LastReplayContainerID = containerID
		r.bindings[key] = bindings
		return true
	}
	return false
}

// PlanReplay computes the re-provisioning replay for a pool (re)start. It is
// idempotent per container generation and bounded per identity, and it never
// mutates a binding: only RecordReplay does, after the provider succeeded.
func (r *PoolRegistry) PlanReplay(restart PoolRestart, policy ReplayPolicy) ReplayPlan {
	now := restart.OccurredAt
	if now.IsZero() {
		now = policy.now()
	}
	plan := ReplayPlan{
		Provider:    restart.Provider,
		SandboxUUID: restart.SandboxUUID,
		ContainerID: restart.ContainerID,
		OccurredAt:  now,
		Policy:      policy,
	}
	for _, binding := range r.Bindings(restart.Provider, restart.SandboxUUID) {
		switch {
		case binding.Released:
			plan.SkippedReleased++
		case !policy.ReprovisionOnRestart:
			plan.Deferred = append(plan.Deferred, DeferredReplay{Identity: binding.Identity, Reason: "reprovision_on_restart_disabled"})
		case replaySuppressed(binding, restart, policy, now):
			plan.Deferred = append(plan.Deferred, DeferredReplay{Identity: binding.Identity, Reason: "already_reprovisioned_for_container"})
		case replayBoundReached(binding, policy, now):
			plan.Deferred = append(plan.Deferred, DeferredReplay{Identity: binding.Identity, Reason: "replay_bound_reached"})
		default:
			plan.Targets = append(plan.Targets, ReplayTarget{
				Identity:    binding.Identity,
				Credentials: append([]CredentialRef(nil), binding.Credentials...),
				HomePaths:   binding.Identity.CredentialPaths(binding.Credentials),
				EnvVars:     EnvVarsOf(binding.Credentials),
				BoundAt:     binding.BoundAt,
			})
		}
	}
	return plan
}

// replaySuppressed reports whether a replay for this binding would only churn
// the per-agent HOME. A restart into a *fresh* container always needs a replay
// (its $HOME is empty and the credentials are gone), so only a re-observation of
// the very same container generation with a still-fresh credential is a no-op.
func replaySuppressed(binding PoolBinding, restart PoolRestart, policy ReplayPolicy, now time.Time) bool {
	if restart.ContainerID == "" || binding.LastReplayContainerID != restart.ContainerID {
		return false
	}
	if policy.RefreshAhead <= 0 || binding.LastReplayedAt.IsZero() {
		return false
	}
	if binding.LastReplayedAt.After(now) {
		return true
	}
	return now.Sub(binding.LastReplayedAt) < policy.RefreshAhead
}

// replayBoundReached reports whether this identity used up its replays in the
// current window, so a mass pool restart is reported instead of looped.
func replayBoundReached(binding PoolBinding, policy ReplayPolicy, now time.Time) bool {
	if policy.MaxPerWindow <= 0 || policy.Window <= 0 {
		return false
	}
	if binding.ReplayWindowStartedAt.IsZero() {
		return false
	}
	if now.Sub(binding.ReplayWindowStartedAt) >= policy.Window {
		// The window rolled over: the count restarts with the next replay.
		return false
	}
	return binding.ReplayCount >= policy.MaxPerWindow
}

// EnvVarsOf returns the sorted, de-duplicated session env names of a credential
// set: the exact names a fresh shell gets. Names only.
func EnvVarsOf(creds []CredentialRef) []string {
	seen := make(map[string]struct{}, len(creds))
	out := make([]string, 0, len(creds))
	for _, cred := range creds {
		for _, env := range cred.EnvVars {
			if _, ok := seen[env]; ok {
				continue
			}
			seen[env] = struct{}{}
			out = append(out, env)
		}
	}
	sort.Strings(out)
	return out
}

// CredentialResolver resolves a credential value for one workspace identity at
// execute time. The value is handed to the provider and exported into the
// session environment: it is never logged and never placed in argv.
type CredentialResolver func(identity WorkspaceIdentity, cred CredentialRef) (string, error)

// SessionScopedEnv resolves the binding's credentials into the session-scoped
// environment of a fresh shell, so a shell is authenticated before any `gh`
// config exists.
func SessionScopedEnv(identity WorkspaceIdentity, creds []CredentialRef, resolve CredentialResolver) (map[string]string, error) {
	if resolve == nil {
		return nil, fmt.Errorf("%w: no resolver", ErrCredentialUnavailable)
	}
	env := make(map[string]string, len(creds))
	for _, cred := range creds {
		if err := cred.Validate(); err != nil {
			return nil, err
		}
		value, err := resolve(identity, cred)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %v", ErrCredentialUnavailable, cred.Name, err)
		}
		if strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("%w: %s: empty value", ErrCredentialUnavailable, cred.Name)
		}
		for _, name := range cred.EnvVars {
			env[name] = value
		}
	}
	return env, nil
}

// ApplySessionEnv merges the session-scoped credential exports into a shell
// spec, overwriting any stale value so a recycled session cannot keep an old
// token. It returns the names it set.
func ApplySessionEnv(spec *ShellSpec, env map[string]string) []string {
	if spec == nil || len(env) == 0 {
		return nil
	}
	if spec.Env == nil {
		spec.Env = make(map[string]string, len(env))
	}
	names := make([]string, 0, len(env))
	for name, value := range env {
		spec.Env[name] = value
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// CredentialArgvViolation reports any credential value that leaked into a shell
// argv. The token must stay session-only: an env-only export keeps it out of the
// sandbox process list.
func CredentialArgvViolation(argv []string, secrets []string) error {
	for i, arg := range argv {
		for _, secret := range secrets {
			if strings.TrimSpace(secret) == "" {
				continue
			}
			if strings.Contains(arg, secret) {
				return fmt.Errorf("%w: argv[%d]", ErrSecretInArgv, i)
			}
		}
	}
	return nil
}

// Redact is the only rendering a credential value may use in logs or reports.
func Redact(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return "[redacted]"
}

// SafeLogFields renders credential references for logging: names, env-var names
// and per-agent HOME paths, never a value.
func SafeLogFields(identity WorkspaceIdentity, creds []CredentialRef) []string {
	out := make([]string, 0, len(creds)*2)
	for _, cred := range creds {
		line := fmt.Sprintf("credential name=%s source=%s env=%s", cred.Name, cred.Source, strings.Join(cred.EnvVars, ","))
		if path := JoinHomePath(identity.Home, cred.HomeRelPath); cred.HomeRelPath != "" {
			line += " path=" + path
		}
		out = append(out, line)
	}
	sort.Strings(out)
	return out
}

// secretValuePatterns match the literal credential shapes that must never appear
// in a reference, a report line or an argv. They are deliberately shape-based,
// so no real token is ever needed (or accepted) in a call site.
var secretValuePatterns = []*regexp.Regexp{
	regexp.MustCompile(`^gh[pousr]_[A-Za-z0-9]{16,}$`),     // GitHub classic/oauth tokens
	regexp.MustCompile(`^github_pat_[A-Za-z0-9_]{20,}$`),   // GitHub fine-grained PATs
	regexp.MustCompile(`^[A-Za-z0-9+/]{40,}={0,2}$`),       // base64 blob
	regexp.MustCompile(`^[0-9a-f]{32,}$`),                  // hex blob
	regexp.MustCompile(`(?i)^(bearer|token)[ :=]+[^\s]+$`), // "Bearer <value>"
}

// LooksLikeSecretValue reports whether a string looks like credential material
// rather than a reference/name. It guards CredentialRef, the session env names
// and the replay report.
func LooksLikeSecretValue(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	for _, pattern := range secretValuePatterns {
		if pattern.MatchString(trimmed) {
			return true
		}
	}
	return false
}
