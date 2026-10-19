package nativecoder

import (
	"fmt"
	"strings"

	goyaml "go.yaml.in/yaml/v3"
)

// recoverydeployment.go is the deployment half of ci#658: the machine-readable
// (and therefore testable) description of the SECOND open-chat deployment
// ("ci2", a-most the same setup/config as ci.msgmate.io) and of the exact delta
// from the existing ci deployment.
//
// recoveryagent.go describes *what the restricted bot may do*; this file
// describes *where it runs and which configuration the new release gets*, so a
// maintainer can:
//
//	(a) see the delta from `values.msgmate-io-ci.yaml` as data
//	    (CIDeploymentDelta) instead of diffing two private value files by hand;
//	(b) render the secret-free Helm overlay of the new deployment
//	    (RenderCI2HelmValues) - the overlay references the runtime secret store
//	    (`credentialRef`), it never carries a credential value;
//	(c) follow the ordered provisioning checklist (CI2ProvisioningChecklist),
//	    which includes the monitoring watchdog that starts the recovery agent
//	    when the watched server goes down;
//	(d) confirm that nothing here changes production
//	    (ValidateDeploymentDelta rejects a production target/path).
//
// The private Helm chart (msgmate-io/open-chat-go-helm-chart, checked out as
// development/helm) owns the real schema; this file owns the *delta* and the
// invariants around it, all of which are unit tested and mirrored by
// the "Second CI deployment: ci2" section of DEVELOPMENT.md.

// The deployment identity of the new (second) CI deployment.
const (
	// CI2DeploymentName is the release/deployment name of the new deployment. It
	// follows the `msgmate-io-<name>` convention of the private CI deploy
	// workflow, next to msgmate-io-ci / -staging / -production.
	CI2DeploymentName = "msgmate-io-ci2"
	// CI2Host is the public host of the new deployment (the server the restricted
	// recovery bot runs ON).
	CI2Host = RecoveryAgentHost
	// CIDeploymentName is the deployment the new one is a copy of.
	CIDeploymentName = "msgmate-io-ci"
	// CI2HelmValuesFile is the Helm values file of the new deployment.
	CI2HelmValuesFile = "development/helm/backend/values.msgmate-io-ci2.yaml"
	// CIHelmValuesFile is the Helm values file the new one is derived from.
	CIHelmValuesFile = "development/helm/backend/values.msgmate-io-ci.yaml"
	// ProductionHelmValuesFile is never a valid delta target.
	ProductionHelmValuesFile = "development/helm/backend/values.msgmate-io-production.yaml"
	// CI2HelmValuesFixture is the committed copy of the rendered overlay. It is
	// what records the new deployment's config in *this* repo: a test asserts it
	// equals RenderCI2HelmValues() byte for byte, so the deployed overlay and the
	// code can not drift (and the delta stays reviewable in a PR diff).
	CI2HelmValuesFixture = "backend/nativecoder/testdata/ci2-helm-values.yaml"
)

// CI2Watchdog* describe how a crash of the watched server is turned into a
// recovery-agent run without a human in the loop.
const (
	// CI2WatchdogSchedule is the cron schedule of the health watchdog.
	CI2WatchdogSchedule = "*/5 * * * *"
	// CI2WatchdogIntervalText is the human-readable form of CI2WatchdogSchedule
	// used in the recorded docs and the watchdog comment.
	CI2WatchdogIntervalText = "every 5 minutes"
	// CI2HealthPath is the liveness endpoint the watchdog polls on the watched
	// host (the same one the profile's health_http probe curls).
	CI2HealthPath = "/api/v1/metrics/health"
)

// RecoveryDeltaKind classifies one row of the ci -> ci2 delta, so a reader (and
// the tests) can tell a deliberate divergence from a pinned-identical value.
const (
	// RecoveryDeltaKindIdentity is the deployment identity (name, host, base URL).
	RecoveryDeltaKindIdentity = "identity"
	// RecoveryDeltaKindCapability is the narrowed native-coder capability set.
	RecoveryDeltaKindCapability = "capability"
	// RecoveryDeltaKindWatchdog is the crash watchdog / trigger wiring.
	RecoveryDeltaKindWatchdog = "watchdog"
	// RecoveryDeltaKindCredentialRef is a runtime secret-store *reference*.
	RecoveryDeltaKindCredentialRef = "credential-ref"
	// RecoveryDeltaKindPinnedSame is a value that must stay byte-identical to ci
	// (image, chart, storage, resources). Listing it is what keeps the copy
	// honest: the two deployments differ only in the documented rows.
	RecoveryDeltaKindPinnedSame = "same-as-ci"
)

// DeploymentValueDelta is one documented difference between the ci and the ci2
// release. CIValue/CI2Value are rendered strings and never contain a credential.
type DeploymentValueDelta struct {
	// Key is the (chart) values key, eg `ingress.host`.
	Key string `json:"key"`
	// Kind is the RecoveryDeltaKind* classification.
	Kind string `json:"kind"`
	// CIValue is the value in the existing ci deployment.
	CIValue string `json:"ci_value"`
	// CI2Value is the value in the new deployment.
	CI2Value string `json:"ci2_value"`
	// Note explains why the row exists.
	Note string `json:"note,omitempty"`
}

// CIDeploymentDelta returns the ordered, complete delta from the ci deployment to
// the new ci2 deployment. The recovery profile supplies the capability and
// credential-ref rows, so the bot's permissions and the deployment config can
// not drift apart.
func CIDeploymentDelta() []DeploymentValueDelta {
	profile := DefaultRecoveryAgentProfile()
	return []DeploymentValueDelta{
		{
			Key: "deployment.name", Kind: RecoveryDeltaKindIdentity,
			CIValue: CIDeploymentName, CI2Value: CI2DeploymentName,
			Note: "second deployment, same setup: it must NOT be the crashed one",
		},
		{
			Key: "ingress.host", Kind: RecoveryDeltaKindIdentity,
			CIValue: MonitoredHost, CI2Value: CI2Host,
			Note: "new public host; the recovery bot runs here, it watches " + MonitoredHost,
		},
		{
			Key: "runtime.PUBLIC_BASE_URL", Kind: RecoveryDeltaKindIdentity,
			CIValue: "https://" + MonitoredHost, CI2Value: "https://" + CI2Host,
			Note: "interaction links and capability markers point at ci2, not at the watched host",
		},
		{
			Key: "ingress.tls.hosts.monitored", Kind: RecoveryDeltaKindPinnedSame,
			CIValue: MonitoredHost, CI2Value: MonitoredHost,
			Note: "the watched host is an ingress route of the new release",
		},
		{
			Key: "coder.fullCoderEnabled", Kind: RecoveryDeltaKindCapability,
			CIValue: "true", CI2Value: "false",
			Note: "only a small sub-set of coding bot capability is configured here",
		},
		{
			Key: "recoveryAgent.enabled", Kind: RecoveryDeltaKindCapability,
			CIValue: "false", CI2Value: "true",
			Note: "the restricted monitoring/diagnose/recover profile is what runs on ci2",
		},
		{
			Key: "recoveryAgent.tools", Kind: RecoveryDeltaKindCapability,
			CIValue: "full coder tool set", CI2Value: strings.Join(profile.Tools, ","),
			Note: "narrow, prefix-checked subset (see ValidateRecoveryProfile)",
		},
		{
			Key: "recoveryAgent.maxRecoveryAttempts", Kind: RecoveryDeltaKindCapability,
			CIValue: "0", CI2Value: fmt.Sprintf("%d", profile.MaxRecoveryAttempts),
			Note: "bounded restart budget per incident, then escalate to a human",
		},
		{
			Key: "recoveryAgent.monitors", Kind: RecoveryDeltaKindCapability,
			CIValue: "", CI2Value: strings.Join(profile.Target.Monitors, ","),
			Note: "exactly the crash target; production is never in this list",
		},
		{
			Key: "recoveryAgent.credentialRef", Kind: RecoveryDeltaKindCredentialRef,
			CIValue: "", CI2Value: profile.Target.CredentialRef,
			Note: "runtime secret-store reference to reach " + MonitoredHost + " (never a value)",
		},
		{
			Key: "watchdog.schedule", Kind: RecoveryDeltaKindWatchdog,
			CIValue: "", CI2Value: CI2WatchdogSchedule,
			Note: "health watchdog that dispatches the recovery agent on a crash",
		},
		{
			Key: "watchdog.dispatchTrigger", Kind: RecoveryDeltaKindWatchdog,
			CIValue: "", CI2Value: TriggerMonitoringCrash,
			Note: "same trigger the CI job uses when monitoring reports the server down",
		},
		{
			Key: "image.repository", Kind: RecoveryDeltaKindPinnedSame,
			CIValue: "same", CI2Value: "same",
			Note: "same image/release flow as ci - the new deployment is not a fork",
		},
		{
			Key: "persistence.storageClass", Kind: RecoveryDeltaKindPinnedSame,
			CIValue: "same", CI2Value: "same",
			Note: "same SQLite volume setup, strategy Recreate (see DEVELOPMENT.md)",
		},
		{
			Key: "resources", Kind: RecoveryDeltaKindPinnedSame,
			CIValue: "same", CI2Value: "same",
			Note: "an a-most-the-same copy; capacity work is tracked separately",
		},
	}
}

// ValidateDeploymentDelta checks the invariants the ticket promises for the new
// deployment: a real values-file target that is not production, a complete delta
// covering identity/capability/watchdog/credential rows, identical rows only
// where pinned, and no credential value anywhere in it.
func ValidateDeploymentDelta(valuesFile string, delta []DeploymentValueDelta) []error {
	errs := []error{}
	trimmedFile := strings.TrimSpace(valuesFile)
	if trimmedFile == "" {
		errs = append(errs, fmt.Errorf("ci2 deployment: empty Helm values file"))
	}
	if strings.Contains(trimmedFile, "production") {
		errs = append(errs, fmt.Errorf("ci2 deployment: %q would change production; the new deployment is a copy of ci, not of production", valuesFile))
	}
	if !strings.Contains(trimmedFile, "ci2") {
		errs = append(errs, fmt.Errorf("ci2 deployment: Helm values file %q does not name the ci2 deployment", valuesFile))
	}

	if len(delta) == 0 {
		errs = append(errs, fmt.Errorf("ci2 deployment: empty delta from the ci deployment"))
		return errs
	}

	seen := map[string]bool{}
	kinds := map[string]bool{
		RecoveryDeltaKindIdentity:      false,
		RecoveryDeltaKindCapability:    false,
		RecoveryDeltaKindWatchdog:      false,
		RecoveryDeltaKindCredentialRef: false,
	}
	for _, row := range delta {
		key := strings.TrimSpace(row.Key)
		if key == "" {
			errs = append(errs, fmt.Errorf("ci2 deployment: delta row with empty key"))
			continue
		}
		if seen[key] {
			errs = append(errs, fmt.Errorf("ci2 deployment: delta lists %q twice", key))
		}
		seen[key] = true
		if _, ok := kinds[row.Kind]; ok {
			kinds[row.Kind] = true
		} else if row.Kind != RecoveryDeltaKindPinnedSame {
			errs = append(errs, fmt.Errorf("ci2 deployment: delta row %q has unknown kind %q", key, row.Kind))
		}
		if row.Kind != RecoveryDeltaKindPinnedSame && row.CIValue == row.CI2Value {
			errs = append(errs, fmt.Errorf("ci2 deployment: delta row %q does not differ from ci (%q)", key, row.CIValue))
		}
		if row.Kind == RecoveryDeltaKindPinnedSame && row.CIValue != row.CI2Value {
			errs = append(errs, fmt.Errorf("ci2 deployment: pinned row %q diverges from ci (%q != %q)", key, row.CIValue, row.CI2Value))
		}
		for _, value := range []string{row.CIValue, row.CI2Value, row.Note} {
			if looksLikeSecret(value) {
				errs = append(errs, fmt.Errorf("ci2 deployment: delta row %q carries what looks like a secret", key))
				break
			}
		}
		for _, value := range []string{row.CI2Value, row.Note} {
			if strings.Contains(value, ProductionHost) {
				errs = append(errs, fmt.Errorf("ci2 deployment: delta row %q points the new deployment at production (%q)", key, value))
			}
		}
	}

	for kind, present := range kinds {
		if !present {
			errs = append(errs, fmt.Errorf("ci2 deployment: delta has no %q row", kind))
		}
	}
	return errs
}

// CI2Watchdog is the crash-detection wiring of the new deployment: a periodic
// liveness check on the watched host that dispatches the recovery agent.
type CI2Watchdog struct {
	Schedule        string   `yaml:"schedule" json:"schedule"`
	Host            string   `yaml:"host" json:"host"`
	HealthPath      string   `yaml:"healthPath" json:"health_path"`
	DispatchTrigger string   `yaml:"dispatchTrigger" json:"dispatch_trigger"`
	DispatchRole    string   `yaml:"dispatchRole" json:"dispatch_role"`
	Monitors        []string `yaml:"monitors" json:"monitors"`
}

// CI2RecoveryWatchdog returns the watchdog configuration of the new deployment.
func CI2RecoveryWatchdog() CI2Watchdog {
	profile := DefaultRecoveryAgentProfile()
	return CI2Watchdog{
		Schedule:        CI2WatchdogSchedule,
		Host:            strings.Join(profile.Target.Monitors, ","),
		HealthPath:      CI2HealthPath,
		DispatchTrigger: TriggerMonitoringCrash,
		DispatchRole:    profile.Role,
		Monitors:        append([]string{}, profile.Target.Monitors...),
	}
}

// The secret-free Helm overlay of the new deployment. The field order is the
// render order, so the rendered file is byte-stable and reviewable in a diff.
type ci2HelmValues struct {
	Deployment    ci2DeploymentSection `yaml:"deployment"`
	Ingress       ci2IngressSection    `yaml:"ingress"`
	RecoveryAgent ci2RecoverySection   `yaml:"recoveryAgent"`
	Watchdog      CI2Watchdog          `yaml:"watchdog"`
	Coder         ci2CoderSection      `yaml:"coder"`
}

type ci2DeploymentSection struct {
	Name          string `yaml:"name"`
	PublicBaseURL string `yaml:"publicBaseURL"`
}

type ci2IngressSection struct {
	Host string   `yaml:"host"`
	TLS  []string `yaml:"tlsHosts"`
}

type ci2RecoverySection struct {
	Enabled             bool            `yaml:"enabled"`
	Role                string          `yaml:"role"`
	CredentialRef       string          `yaml:"credentialRef"`
	Monitors            []string        `yaml:"monitors"`
	Triggers            []string        `yaml:"triggers"`
	Tools               []string        `yaml:"tools"`
	MaxRecoveryAttempts int             `yaml:"maxRecoveryAttempts"`
	Probes              []ci2ProbeEntry `yaml:"probes"`
}

type ci2ProbeEntry struct {
	ID                   string `yaml:"id"`
	Capability           string `yaml:"capability"`
	Tool                 string `yaml:"tool"`
	Command              string `yaml:"command"`
	Mutating             bool   `yaml:"mutating"`
	RequiresConfirmation bool   `yaml:"requiresConfirmation"`
}

type ci2CoderSection struct {
	FullCoderEnabled bool `yaml:"fullCoderEnabled"`
}

// RenderCI2HelmValues renders the Helm values overlay of the new deployment from
// the restricted recovery profile: identity/host overrides, the narrow agent
// config (tools, probes, triggers, credential *reference*) and the watchdog.
// It carries no secret and is deterministic, so it can be committed, reviewed
// and asserted in tests.
func RenderCI2HelmValues() (string, error) {
	profile := DefaultRecoveryAgentProfile()
	probes := make([]ci2ProbeEntry, 0, len(profile.Probes))
	for _, probe := range profile.Probes {
		probes = append(probes, ci2ProbeEntry{
			ID:                   probe.ID,
			Capability:           probe.Capability,
			Tool:                 probe.Tool,
			Command:              probe.Command,
			Mutating:             probe.Mutating,
			RequiresConfirmation: probe.RequiresConfirmation,
		})
	}
	values := ci2HelmValues{
		Deployment: ci2DeploymentSection{
			Name:          CI2DeploymentName,
			PublicBaseURL: "https://" + CI2Host,
		},
		Ingress: ci2IngressSection{
			Host: CI2Host,
			TLS:  []string{CI2Host, MonitoredHost},
		},
		RecoveryAgent: ci2RecoverySection{
			Enabled:             true,
			Role:                profile.Role,
			CredentialRef:       profile.Target.CredentialRef,
			Monitors:            append([]string{}, profile.Target.Monitors...),
			Triggers:            append([]string{}, profile.Triggers...),
			Tools:               append([]string{}, profile.Tools...),
			MaxRecoveryAttempts: profile.MaxRecoveryAttempts,
			Probes:              probes,
		},
		Watchdog: CI2RecoveryWatchdog(),
		Coder:    ci2CoderSection{FullCoderEnabled: false},
	}

	raw, err := goyaml.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("render ci2 Helm values: %w", err)
	}
	header := "# Generated from backend/nativecoder (ci#658) - do not hand-edit.\n" +
		"# Delta from " + CIHelmValuesFile + " (" + CIDeploymentName + "); see\n" +
		"# DEVELOPMENT.md (section \"Second CI deployment: ci2\") for the recorded delta\n" +
		"# and the provisioning checklist.\n" +
		"# Secret-free: credentialRef is a runtime secret-store reference.\n"
	return header + string(raw), nil
}

// CI2ProvisioningChecklist is the ordered operator checklist for standing up the
// new deployment. Order matters: the watched host may only be handed to the bot
// after the credential exists, and the recovery agent is started BEFORE a
// deployment rolls (TriggerPreDeploy) as well as on demand from CI
// (TriggerMonitoringCrash / TriggerManual).
func CI2ProvisioningChecklist() []string {
	return []string{
		"1. Create the " + CI2DeploymentName + " release from the " + CIDeploymentName + " values with the " + CIDeploymentName + " values file as base (" + CIHelmValuesFile + ").",
		"2. Apply the rendered overlay " + CI2HelmValuesFile + " (RenderCI2HelmValues) so name, host and PUBLIC_BASE_URL point at " + CI2Host + ".",
		"3. Store the SSH/service credential for " + MonitoredHost + " in the runtime secret store under " + DefaultRecoveryAgentProfile().Target.CredentialRef + " and wire only the reference into the deployment env (never an inline value).",
		"4. Confirm the image, storage and resource rows are still pinned to the ci values (no fork of the release flow).",
		"5. Enable the watchdog (" + CI2WatchdogSchedule + " poll of https://" + MonitoredHost + CI2HealthPath + ") and let a failing check dispatch the recovery agent with trigger " + TriggerMonitoringCrash + ".",
		"6. Run the recovery agent once manually from CI (trigger " + TriggerManual + ") against a healthy " + MonitoredHost + " and confirm every probe reports verbatim output.",
		"7. Run it once with trigger " + TriggerPreDeploy + " immediately before the next deployment, so the bot is already attached while the release rolls.",
		"8. Re-check that neither the deployment nor the bot can reach " + ProductionHost + " (ValidateDeploymentDelta + ValidateRecoveryProfile).",
	}
}

// RenderCI2ChecklistMarkdown renders the checklist as the markdown ordered list
// the recorded doc block contains (the steps already carry their own `N.`), so
// DEVELOPMENT.md and the code can not drift.
func RenderCI2ChecklistMarkdown() string {
	var b strings.Builder
	for _, step := range CI2ProvisioningChecklist() {
		b.WriteString(step + "\n")
	}
	return b.String()
}

// RenderDeploymentDeltaTable renders the ci -> ci2 delta as a markdown table, so
// the recorded delta in DEVELOPMENT.md and the code can not drift (the doc test
// asserts the rendered rows are present).
func RenderDeploymentDeltaTable() string {
	var b strings.Builder
	b.WriteString("| values key | kind | ci | ci2 | why |\n")
	b.WriteString("|---|---|---|---|---|\n")
	for _, row := range CIDeploymentDelta() {
		b.WriteString(fmt.Sprintf("| `%s` | %s | %s | %s | %s |\n", row.Key, row.Kind, row.CIValue, row.CI2Value, row.Note))
	}
	return b.String()
}
