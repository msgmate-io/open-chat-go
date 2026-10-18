package nativecoder

import (
	"fmt"
	"sort"
	"strings"
)

// recoveryagent.go is the machine-readable form of the ci#658 request for a
// SECOND open-chat deployment ("ci2", a-most the same setup/config as
// ci.msgmate.io) whose only purpose is a small, restricted coding bot that can
// DEBUG, RECOVER and MONITOR the ci.msgmate.io server when it crashes.
//
// The ticket asks for three things, and this file is the model for all three
// without carrying any secret:
//
//	(a) a deployment-target descriptor for the new server (`ci2.msgmate.io`,
//	    normally driven through the private Helm chart / open-chat config in the
//	    open-chat-go-ci repository), including the host it is allowed to watch
//	    (`ci.msgmate.io`) and the credential *references* (never values) the
//	    bot needs to reach it;
//	(b) a deliberately narrow capability subset ("a small sub-set of coding bot
//	    capabable of debugging, recovering and monitoring") instead of the full
//	    coder tool set;
//	(c) the two ways that agent is started: before a deployment
//	    (TriggerPreDeploy) and on demand from CI when monitoring detects that
//	    the watched server is down (TriggerMonitoringCrash).
//
// A profile is data-only and validated (ValidateRecoveryProfile), so the same
// descriptor can be unit tested here, mirrored by the private deployment
// tooling and documented in docs/emergency-recovery-agent.md.

// The deployment names/hosts the recovery agent understands.
const (
	// RecoveryAgentName is the deployment name of the new, second deployment.
	// It matches the `msgmate-io-<name>` deployment convention of the private
	// CI deploy workflow (ci#658).
	RecoveryAgentName = "ci2"
	// RecoveryAgentHost is the public host of the new deployment. It is the
	// server the restricted recovery bot runs ON.
	RecoveryAgentHost = "ci2.msgmate.io"
	// MonitoredHost is the server the recovery bot watches over. The new
	// deployment exists so a crash of the ci deployment can be recovered by a
	// bot that is not itself affected by that crash.
	MonitoredHost = "ci.msgmate.io"
	// ProductionHost is never a valid recovery target: a recovery bot that may
	// restart or mutate production would defeat the isolation the second
	// deployment buys.
	ProductionHost = "chat.msgmate.io"
)

// RecoveryAgentRole is the native coder role the recovery bot is dispatched as.
const RecoveryAgentRole = "admin"

// Recovery triggers: why the recovery agent is started.
const (
	// TriggerPreDeploy starts the recovery agent BEFORE a deployment so it is
	// already attached (and watching) while the deployment rolls.
	TriggerPreDeploy = "pre_deploy"
	// TriggerMonitoringCrash starts the recovery agent on demand, when the
	// monitoring of the watched host reports it down.
	TriggerMonitoringCrash = "monitoring_crash"
	// TriggerManual is the operator escape hatch (run it from CI by hand).
	TriggerManual = "manual"
)

// Recovery capability classes. Each probe/action belongs to exactly one, and a
// validated profile must cover every class (that is what keeps the subset
// "capable of debugging, recovering and monitoring" rather than a random tool
// grab bag).
const (
	// RecoveryCapabilityMonitor is read-only liveness/resource observation.
	RecoveryCapabilityMonitor = "monitor"
	// RecoveryCapabilityDiagnose is read-only fault investigation.
	RecoveryCapabilityDiagnose = "diagnose"
	// RecoveryCapabilityRecover is the mutating remedy step.
	RecoveryCapabilityRecover = "recover"
	// RecoveryCapabilityReport is telling a human/the owning chat what happened.
	RecoveryCapabilityReport = "report"
)

// RecoveryCapabilities is the ordered inventory a profile must cover.
var RecoveryCapabilities = []string{
	RecoveryCapabilityMonitor,
	RecoveryCapabilityDiagnose,
	RecoveryCapabilityRecover,
	RecoveryCapabilityReport,
}

// RecoveryDeploymentTarget is the new deployment and what it may touch. It
// carries references only: the credential is resolved by the runtime secret
// store from CredentialRef, so no token or key ever appears in a config file,
// a chat message or a unit test.
type RecoveryDeploymentTarget struct {
	// Name is the deployment name (RecoveryAgentName).
	Name string `json:"name"`
	// Host is the public host of the new deployment.
	Host string `json:"host"`
	// Monitors are the hosts this deployment is allowed to watch/recover.
	Monitors []string `json:"monitors"`
	// CredentialRef is the runtime-secret-store reference (eg the ssh-integration
	// server name or secret name) used to reach the monitored host. It is a
	// reference, never a secret value.
	CredentialRef string `json:"credential_ref"`
	// BaseURL is the public base URL of the new deployment, used so the
	// recovery interaction links and the capability-marker replies point at
	// ci2 and not at the watched host.
	BaseURL string `json:"base_url"`
}

// RecoveryProbe is one thing the restricted bot may do. A probe either observes
// (Mutating false, never gated) or mutates the watched server (Mutating true,
// always gated by the confirmation policy).
type RecoveryProbe struct {
	// ID is the stable probe identifier.
	ID string `json:"id"`
	// Capability is the RecoveryCapability* class this probe is evidence for.
	Capability string `json:"capability"`
	// Tool is the tool name the probe calls.
	Tool string `json:"tool"`
	// Command is the (secret-free) command/query template. It never carries a
	// credential.
	Command string `json:"command"`
	// Mutating reports whether the probe changes the watched server.
	Mutating bool `json:"mutating"`
	// RequiresConfirmation must be true for every mutating probe.
	RequiresConfirmation bool `json:"requires_confirmation"`
}

// RecoveryAgentProfile is the whole descriptor of the restricted recovering
// coding bot of a deployment.
type RecoveryAgentProfile struct {
	// Name is the deployment name this profile belongs to.
	Name string `json:"name"`
	// Role is the native coder role the bot is dispatched as.
	Role string `json:"role"`
	// Target is the deployment the bot runs on and what it watches.
	Target RecoveryDeploymentTarget `json:"target"`
	// Triggers are the ways the bot is started (pre-deploy, monitoring, manual).
	Triggers []string `json:"triggers"`
	// Tools is the deliberate, narrow tool subset (strictly smaller than the
	// full coder set).
	Tools []string `json:"tools"`
	// Probes are the allowed observations/remedies.
	Probes []RecoveryProbe `json:"probes"`
	// MaxRecoveryAttempts caps how often the bot may try to restart the watched
	// server per incident before it escalates to a human.
	MaxRecoveryAttempts int `json:"max_recovery_attempts"`
}

// allowedRecoveryToolPrefixes are the tool families a restricted recovery bot
// may use: the coder read/exec/git tools plus the ssh/service integration tools
// that reach the watched host. Anything else (web, mcp, sandbox provisioning,
// sub-agents) is intentionally excluded from the subset.
var allowedRecoveryToolPrefixes = []string{"coder_", "ssh_", "service_", "chat_", "github_"}

// DefaultRecoveryAgentProfile returns the profile for the ci2 deployment: the
// minimal set that can monitor ci.msgmate.io, diagnose a crash and recover it
// under confirmation.
func DefaultRecoveryAgentProfile() RecoveryAgentProfile {
	return RecoveryAgentProfile{
		Name: RecoveryAgentName,
		Role: RecoveryAgentRole,
		Target: RecoveryDeploymentTarget{
			Name:          RecoveryAgentName,
			Host:          RecoveryAgentHost,
			Monitors:      []string{MonitoredHost},
			CredentialRef: "ci-recovery-ssh",
			BaseURL:       "https://" + RecoveryAgentHost,
		},
		Triggers:            []string{TriggerPreDeploy, TriggerMonitoringCrash, TriggerManual},
		Tools:               []string{"coder_bash", "coder_read", "coder_list", "chat_send_message", "ssh_exec_command_run_short", "service_restart"},
		MaxRecoveryAttempts: 2,
		Probes:              defaultRecoveryProbes(),
	}
}

// defaultRecoveryProbes is the minimal probe set covering every recovery
// capability class.
func defaultRecoveryProbes() []RecoveryProbe {
	return []RecoveryProbe{
		{
			ID:         "health_http",
			Capability: RecoveryCapabilityMonitor,
			Tool:       "ssh_exec_command_run_short",
			Command:    "curl -sS -o /dev/null -w '%{http_code}' https://" + MonitoredHost + "/api/v1/metrics/health",
		},
		{
			ID:         "service_status",
			Capability: RecoveryCapabilityMonitor,
			Tool:       "ssh_exec_command_run_short",
			Command:    "systemctl is-active open-chat",
		},
		{
			ID:         "logs_tail",
			Capability: RecoveryCapabilityDiagnose,
			Tool:       "ssh_exec_command_run_short",
			Command:    "journalctl -u open-chat --since '-10min' --no-pager | tail -n 200",
		},
		{
			ID:         "disk_and_memory",
			Capability: RecoveryCapabilityDiagnose,
			Tool:       "ssh_exec_command_run_short",
			Command:    "df -h / /var/lib/open-chat; free -m",
		},
		{
			ID:                   "service_restart",
			Capability:           RecoveryCapabilityRecover,
			Tool:                 "service_restart",
			Command:              "restart the open-chat service on " + MonitoredHost,
			Mutating:             true,
			RequiresConfirmation: true,
		},
		{
			ID:         "incident_report",
			Capability: RecoveryCapabilityReport,
			Tool:       "chat_send_message",
			Command:    "post the incident summary (trigger, verbatim errors, remedy, verification) to the owning chat",
		},
	}
}

// recoveryMarkerToken is the nonce placeholder of the recovery capability
// scenario prompt, mirroring the coder capability plan.
const recoveryMarkerToken = capabilityMarkerToken

// isKnownRecoveryTool reports whether a tool belongs to the restricted recovery
// subset families.
func isKnownRecoveryTool(name string) bool {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return false
	}
	for _, prefix := range allowedRecoveryToolPrefixes {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}

// ValidateRecoveryProfile checks the invariants the ci#658 design promises: a
// non-production target, a credential *reference* (never a value), at least one
// trigger, a narrow known-tool subset, full capability coverage, and a
// confirmation gate on every mutating probe.
func ValidateRecoveryProfile(profile RecoveryAgentProfile) []error {
	errs := []error{}
	if strings.TrimSpace(profile.Name) == "" {
		errs = append(errs, fmt.Errorf("recovery profile: empty deployment name"))
	}
	if !containsString(knownRoles, profile.Role) {
		errs = append(errs, fmt.Errorf("recovery profile %q: unknown role %q", profile.Name, profile.Role))
	}

	target := profile.Target
	if strings.TrimSpace(target.Host) == "" {
		errs = append(errs, fmt.Errorf("recovery profile %q: target host is empty", profile.Name))
	}
	if strings.Contains(target.Host, ProductionHost) {
		errs = append(errs, fmt.Errorf("recovery profile %q: production host %q is never a recovery target", profile.Name, target.Host))
	}
	if strings.TrimSpace(target.Name) != strings.TrimSpace(profile.Name) {
		errs = append(errs, fmt.Errorf("recovery profile %q: target name %q does not match the profile", profile.Name, target.Name))
	}
	if len(target.Monitors) == 0 {
		errs = append(errs, fmt.Errorf("recovery profile %q: monitors no host", profile.Name))
	}
	for _, host := range target.Monitors {
		if strings.Contains(host, ProductionHost) {
			errs = append(errs, fmt.Errorf("recovery profile %q: may not monitor production host %q", profile.Name, host))
		}
	}
	if strings.TrimSpace(target.CredentialRef) == "" {
		errs = append(errs, fmt.Errorf("recovery profile %q: no credential reference", profile.Name))
	}
	if looksLikeSecret(target.CredentialRef) {
		errs = append(errs, fmt.Errorf("recovery profile %q: credential reference looks like a secret value; store it in the secret store and reference it", profile.Name))
	}
	if strings.TrimSpace(target.BaseURL) == "" {
		errs = append(errs, fmt.Errorf("recovery profile %q: no base URL", profile.Name))
	}

	if len(profile.Triggers) == 0 {
		errs = append(errs, fmt.Errorf("recovery profile %q: no trigger", profile.Name))
	}
	for _, trigger := range profile.Triggers {
		if !containsString([]string{TriggerPreDeploy, TriggerMonitoringCrash, TriggerManual}, trigger) {
			errs = append(errs, fmt.Errorf("recovery profile %q: unknown trigger %q", profile.Name, trigger))
		}
	}

	if len(profile.Tools) == 0 {
		errs = append(errs, fmt.Errorf("recovery profile %q: no tools", profile.Name))
	}
	for _, tool := range profile.Tools {
		if !isKnownRecoveryTool(tool) {
			errs = append(errs, fmt.Errorf("recovery profile %q: tool %q is outside the restricted recovery subset", profile.Name, tool))
		}
	}

	covered := map[string]bool{}
	for _, probe := range profile.Probes {
		label := probe.ID
		if strings.TrimSpace(label) == "" {
			errs = append(errs, fmt.Errorf("recovery profile %q: probe with empty id", profile.Name))
			label = "<empty id>"
		}
		if !containsString(RecoveryCapabilities, probe.Capability) {
			errs = append(errs, fmt.Errorf("recovery profile %q: probe %q has unknown capability %q", profile.Name, label, probe.Capability))
		} else {
			covered[probe.Capability] = true
		}
		if !isKnownRecoveryTool(probe.Tool) {
			errs = append(errs, fmt.Errorf("recovery profile %q: probe %q uses tool %q outside the restricted subset", profile.Name, label, probe.Tool))
		}
		if strings.TrimSpace(probe.Command) == "" {
			errs = append(errs, fmt.Errorf("recovery profile %q: probe %q has no command", profile.Name, label))
		}
		if looksLikeSecret(probe.Command) {
			errs = append(errs, fmt.Errorf("recovery profile %q: probe %q carries what looks like a secret", profile.Name, label))
		}
		if probe.Mutating && !probe.RequiresConfirmation {
			errs = append(errs, fmt.Errorf("recovery profile %q: mutating probe %q is not gated by the confirmation policy", profile.Name, label))
		}
	}
	for _, capability := range RecoveryCapabilities {
		if !covered[capability] {
			errs = append(errs, fmt.Errorf("recovery profile %q: capability %q is not covered by any probe", profile.Name, capability))
		}
	}

	if profile.MaxRecoveryAttempts <= 0 {
		errs = append(errs, fmt.Errorf("recovery profile %q: max recovery attempts must be positive", profile.Name))
	}
	return errs
}

// looksLikeSecret is a cheap "does this look like an embedded credential"
// check, so a config that pastes a token/key instead of a reference is rejected
// before it can be stored or logged.
func looksLikeSecret(value string) bool {
	lower := strings.ToLower(value)
	for _, marker := range []string{"-----begin", "ghp_", "github_pat_", "sk-", "bearer ", "password=", "private-key", "ssh-rsa aaaa"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// MutatingProbes returns the gated (mutating) probes of a profile, sorted by id.
func MutatingProbes(profile RecoveryAgentProfile) []RecoveryProbe {
	out := make([]RecoveryProbe, 0, len(profile.Probes))
	for _, probe := range profile.Probes {
		if probe.Mutating {
			out = append(out, probe)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// RecoveryPrompt renders the task message a recovery interaction is started
// with. It carries the run nonce so a reply can only satisfy the marker
// invariant by actually doing the work, names the trigger and states the
// hard limits (read-only until the recovery step, confirmation for every
// mutation, never point at production).
func RecoveryPrompt(profile RecoveryAgentProfile, trigger string) string {
	return fmt.Sprintf(
		"You are the restricted emergency recovery agent of the %s deployment (role %s). "+
			"Trigger: %s. You watch %s and you run on %s.\n"+
			"Procedure: (1) MONITOR: run the health and service-status probes and report the exact output. "+
			"(2) DIAGNOSE: only if a probe failed, collect the recent %s logs, disk and memory and state the "+
			"fault you can actually see; if you cannot see a cause, say so instead of guessing. "+
			"(3) RECOVER: propose at most %d remedy (a service restart), which MUST go through the confirmation "+
			"gate - if it is rejected, do not retry it. (4) REPORT: post the incident summary (trigger, verbatim "+
			"errors, remedy, verification) to this chat. Never touch %s and never invent probe output. "+
			"Finish with the exact marker %s on its own line.",
		profile.Target.Host, profile.Role, trigger, strings.Join(profile.Target.Monitors, ", "),
		RecoveryAgentHost, MonitoredHost, profile.MaxRecoveryAttempts, ProductionHost, recoveryMarkerToken,
	)
}
