package nativecoder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	goyaml "go.yaml.in/yaml/v3"
)

// TestCIDeploymentDeltaIsValidAndCopiesCI asserts the recorded ci -> ci2 delta is
// complete, points at the new deployment's values file and is a copy of ci (not of
// production).
func TestCIDeploymentDeltaIsValidAndCopiesCI(t *testing.T) {
	delta := CIDeploymentDelta()
	if errs := ValidateDeploymentDelta(CI2HelmValuesFile, delta); len(errs) > 0 {
		t.Fatalf("recorded ci -> ci2 delta is invalid: %v", errs)
	}

	byKey := map[string]DeploymentValueDelta{}
	for _, row := range delta {
		byKey[row.Key] = row
	}
	for _, key := range []string{"deployment.name", "ingress.host", "runtime.PUBLIC_BASE_URL", "recoveryAgent.enabled", "watchdog.schedule", "watchdog.dispatchTrigger", "recoveryAgent.credentialRef"} {
		if _, ok := byKey[key]; !ok {
			t.Errorf("delta is missing the %q row", key)
		}
	}
	if got := byKey["deployment.name"].CI2Value; got != CI2DeploymentName {
		t.Errorf("deployment.name ci2 value = %q, want %q", got, CI2DeploymentName)
	}
	if got := byKey["ingress.host"].CI2Value; got != CI2Host {
		t.Errorf("ingress.host ci2 value = %q, want %q", got, CI2Host)
	}
	if got := byKey["ingress.host"].CIValue; got != MonitoredHost {
		t.Errorf("ingress.host ci value = %q, want the watched host %q", got, MonitoredHost)
	}
	// The new deployment is a copy of ci: the pinned rows must be identical and
	// must not smuggle in a divergent (forked) value.
	pinned := 0
	for _, row := range delta {
		if row.Kind == RecoveryDeltaKindPinnedSame {
			pinned++
			if row.CIValue != row.CI2Value {
				t.Errorf("pinned row %q diverges from ci: %q != %q", row.Key, row.CIValue, row.CI2Value)
			}
		}
		if strings.Contains(row.CI2Value, ProductionHost) {
			t.Errorf("row %q points the new deployment at production", row.Key)
		}
	}
	if pinned == 0 {
		t.Error("delta pins no row to the ci values, so it does not describe a copy")
	}
	// The tool subset row must come from the profile, not from a hand-copied list.
	if want := strings.Join(DefaultRecoveryAgentProfile().Tools, ","); byKey["recoveryAgent.tools"].CI2Value != want {
		t.Errorf("recoveryAgent.tools = %q, want the profile subset %q", byKey["recoveryAgent.tools"].CI2Value, want)
	}
}

// TestValidateDeploymentDeltaRejectsBrokenConfig asserts every class of defect a
// hand-edited deployment config could introduce is rejected - in particular any
// attempt to make the new deployment production.
func TestValidateDeploymentDeltaRejectsBrokenConfig(t *testing.T) {
	base := CIDeploymentDelta()
	if errs := ValidateDeploymentDelta(CI2HelmValuesFile, base); len(errs) != 0 {
		t.Fatalf("base delta must be valid, got %v", errs)
	}

	if len(ValidateDeploymentDelta(ProductionHelmValuesFile, base)) == 0 {
		t.Error("validator accepted production as the delta target")
	}
	if len(ValidateDeploymentDelta("development/helm/backend/values.msgmate-io-staging.yaml", base)) == 0 {
		t.Error("validator accepted a target that is not the new deployment")
	}
	if len(ValidateDeploymentDelta("", base)) == 0 {
		t.Error("validator accepted an empty values file")
	}
	if len(ValidateDeploymentDelta(CI2HelmValuesFile, nil)) == 0 {
		t.Error("validator accepted an empty delta (nothing records the difference from ci)")
	}

	duplicate := append(CIDeploymentDelta(), CIDeploymentDelta()[0])
	if len(ValidateDeploymentDelta(CI2HelmValuesFile, duplicate)) == 0 {
		t.Error("validator accepted a delta listing the same key twice")
	}

	noIdentity := []DeploymentValueDelta{}
	for _, row := range base {
		if row.Kind != RecoveryDeltaKindIdentity {
			noIdentity = append(noIdentity, row)
		}
	}
	if len(ValidateDeploymentDelta(CI2HelmValuesFile, noIdentity)) == 0 {
		t.Error("validator accepted a delta without any identity row")
	}

	noWatchdog := []DeploymentValueDelta{}
	for _, row := range base {
		if row.Kind != RecoveryDeltaKindWatchdog {
			noWatchdog = append(noWatchdog, row)
		}
	}
	if len(ValidateDeploymentDelta(CI2HelmValuesFile, noWatchdog)) == 0 {
		t.Error("validator accepted a delta without the crash watchdog")
	}

	noCredential := []DeploymentValueDelta{}
	for _, row := range base {
		if row.Kind != RecoveryDeltaKindCredentialRef {
			noCredential = append(noCredential, row)
		}
	}
	if len(ValidateDeploymentDelta(CI2HelmValuesFile, noCredential)) == 0 {
		t.Error("validator accepted a delta without a credential reference")
	}

	unknownKind := CIDeploymentDelta()
	unknownKind[0].Kind = "guess"
	if len(ValidateDeploymentDelta(CI2HelmValuesFile, unknownKind)) == 0 {
		t.Error("validator accepted an unknown delta kind")
	}

	notDifferent := CIDeploymentDelta()
	notDifferent[0].CI2Value = notDifferent[0].CIValue
	if len(ValidateDeploymentDelta(CI2HelmValuesFile, notDifferent)) == 0 {
		t.Error("validator accepted a difference row that does not differ from ci")
	}

	pinnedDiverges := CIDeploymentDelta()
	for i := range pinnedDiverges {
		if pinnedDiverges[i].Kind == RecoveryDeltaKindPinnedSame {
			pinnedDiverges[i].CI2Value = "a-fork"
			break
		}
	}
	if len(ValidateDeploymentDelta(CI2HelmValuesFile, pinnedDiverges)) == 0 {
		t.Error("validator accepted a pinned row that forks from the ci value")
	}

	secret := CIDeploymentDelta()
	for i := range secret {
		if secret[i].Key == "recoveryAgent.credentialRef" {
			secret[i].CI2Value = "ghp_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		}
	}
	if len(ValidateDeploymentDelta(CI2HelmValuesFile, secret)) == 0 {
		t.Error("validator accepted a credential value where a reference belongs")
	}

	productionNote := CIDeploymentDelta()
	productionNote[1].Note = "also restarts " + ProductionHost
	if len(ValidateDeploymentDelta(CI2HelmValuesFile, productionNote)) == 0 {
		t.Error("validator accepted a row pointing at production")
	}

	stagingLike := CIDeploymentDelta()
	stagingLike[2].CI2Value = "https://" + ProductionHost
	if len(ValidateDeploymentDelta(CI2HelmValuesFile, stagingLike)) == 0 {
		t.Error("validator accepted a ci2 base URL on production")
	}
}

// TestRenderCI2HelmValuesIsCompleteSecretFreeAndStable asserts the rendered
// overlay is a real, parseable values file: same identity as the delta, the
// narrow agent config, the watchdog - and no secret.
func TestRenderCI2HelmValuesIsCompleteSecretFreeAndStable(t *testing.T) {
	rendered, err := RenderCI2HelmValues()
	if err != nil {
		t.Fatalf("render ci2 Helm values: %v", err)
	}
	again, err := RenderCI2HelmValues()
	if err != nil {
		t.Fatalf("render ci2 Helm values twice: %v", err)
	}
	if rendered != again {
		t.Error("rendered Helm values are not deterministic")
	}
	if strings.Contains(rendered, "production") || strings.Contains(rendered, ProductionHost) {
		t.Error("rendered Helm values mention production")
	}
	if looksLikeSecret(rendered) {
		t.Error("rendered Helm values look like they carry a secret")
	}
	if !strings.Contains(rendered, CIHelmValuesFile) {
		t.Error("rendered header does not name the ci values file it is derived from")
	}

	var decoded map[string]any
	if err := goyaml.Unmarshal([]byte(rendered), &decoded); err != nil {
		t.Fatalf("rendered Helm values are not valid YAML: %v", err)
	}
	if len(decoded) == 0 {
		t.Fatal("rendered Helm values decoded to an empty document")
	}

	profile := DefaultRecoveryAgentProfile()
	for _, want := range []string{
		"deployment:", "ingress:", "recoveryAgent:", "watchdog:", "coder:",
		"name: " + CI2DeploymentName,
		"publicBaseURL: https://" + CI2Host,
		"host: " + CI2Host,
		"credentialRef: " + profile.Target.CredentialRef,
		"fullCoderEnabled: false",
		"maxRecoveryAttempts: 2",
		"schedule: '" + CI2WatchdogSchedule + "'",
		"dispatchTrigger: " + TriggerMonitoringCrash,
		"requiresConfirmation: true",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered Helm values are missing %q\n%s", want, rendered)
		}
	}
	for _, probe := range profile.Probes {
		if !strings.Contains(rendered, "id: "+probe.ID) {
			t.Errorf("rendered Helm values are missing probe %q", probe.ID)
		}
	}
	for _, tool := range profile.Tools {
		if !strings.Contains(rendered, tool) {
			t.Errorf("rendered Helm values are missing tool %q", tool)
		}
	}
	if !strings.Contains(rendered, MonitoredHost) {
		t.Errorf("rendered Helm values do not mention the watched host %q", MonitoredHost)
	}
}

// TestCI2WatchdogWiresRecoveryAgent asserts the crash watchdog of the new
// deployment really starts the restricted recovery agent (host, endpoint, role
// and trigger all come from the profile).
func TestCI2WatchdogWiresRecoveryAgent(t *testing.T) {
	watchdog := CI2RecoveryWatchdog()
	profile := DefaultRecoveryAgentProfile()
	if watchdog.Host != MonitoredHost {
		t.Errorf("watchdog polls %q, want the watched host %q", watchdog.Host, MonitoredHost)
	}
	if len(watchdog.Monitors) != 1 || watchdog.Monitors[0] != MonitoredHost {
		t.Errorf("watchdog monitors %v, want exactly [%s]", watchdog.Monitors, MonitoredHost)
	}
	if watchdog.DispatchRole != profile.Role || !containsString(knownRoles, watchdog.DispatchRole) {
		t.Errorf("watchdog dispatches role %q, which is not the recovery role", watchdog.DispatchRole)
	}
	if watchdog.DispatchTrigger != TriggerMonitoringCrash || !containsString(profile.Triggers, watchdog.DispatchTrigger) {
		t.Errorf("watchdog trigger %q is not the monitoring trigger the profile accepts", watchdog.DispatchTrigger)
	}
	if watchdog.Schedule != CI2WatchdogSchedule || !strings.HasPrefix(watchdog.Schedule, "*/") {
		t.Errorf("watchdog schedule %q is not the periodic health check", watchdog.Schedule)
	}
	// The watchdog may only poll the endpoint the profile's health probe uses.
	found := false
	for _, probe := range profile.Probes {
		if strings.Contains(probe.Command, MonitoredHost+CI2HealthPath) {
			found = true
		}
	}
	if !found {
		t.Errorf("no profile probe polls https://%s%s", MonitoredHost, CI2HealthPath)
	}
	if strings.Contains(watchdog.Host, ProductionHost) {
		t.Error("watchdog points at production")
	}
}

// TestCI2ProvisioningChecklistIsOrdered asserts the operator checklist covers the
// whole new deployment in a safe order: credential reference before the watched
// host is handed over, watchdog before the manual/pre-deploy runs, production
// check last.
func TestCI2ProvisioningChecklistIsOrdered(t *testing.T) {
	steps := CI2ProvisioningChecklist()
	if len(steps) < 6 {
		t.Fatalf("checklist has only %d steps: %v", len(steps), steps)
	}
	joined := strings.Join(steps, "\n")
	for _, want := range []string{
		CI2DeploymentName, CI2HelmValuesFile, DefaultRecoveryAgentProfile().Target.CredentialRef,
		MonitoredHost, CI2WatchdogSchedule, TriggerManual, TriggerPreDeploy, ProductionHost,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("checklist does not mention %q", want)
		}
	}
	indexOf := func(needle string) int {
		for i, step := range steps {
			if strings.Contains(step, needle) {
				return i
			}
		}
		return -1
	}
	credential := indexOf(DefaultRecoveryAgentProfile().Target.CredentialRef)
	watchdog := indexOf("watchdog")
	productionCheck := indexOf(ProductionHost)
	if credential < 0 || watchdog < 0 || productionCheck < 0 {
		t.Fatalf("checklist lost a required step: %v", steps)
	}
	if credential >= watchdog {
		t.Error("checklist enables the watchdog before the credential reference exists")
	}
	if productionCheck != len(steps)-1 {
		t.Error("checklist must end with the production-isolation check")
	}
}

// TestCI2HelmValuesFixtureMatchesRender asserts the committed overlay
// (testdata/ci2-helm-values.yaml) is exactly what RenderCI2HelmValues() produces:
// the recorded config of the new deployment can not drift from the code, and the
// reviewer sees the deployment change in the PR diff.
func TestCI2HelmValuesFixtureMatchesRender(t *testing.T) {
	rendered, err := RenderCI2HelmValues()
	if err != nil {
		t.Fatalf("render ci2 Helm values: %v", err)
	}
	path := filepath.Join("testdata", "ci2-helm-values.yaml")
	if os.Getenv("UPDATE_CI2_VALUES") == "1" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("create testdata: %v", err)
		}
		if err := os.WriteFile(path, []byte(rendered), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		t.Logf("regenerated %s", path)
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read committed ci2 overlay %s: %v", CI2HelmValuesFixture, err)
	}
	if string(raw) != rendered {
		t.Errorf("%s does not match RenderCI2HelmValues(); regenerate it with:\n  UPDATE_CI2_VALUES=1 go test ./nativecoder/ -run TestCI2HelmValuesFixtureMatchesRender", CI2HelmValuesFixture)
	}
	if looksLikeSecret(string(raw)) {
		t.Errorf("committed ci2 overlay %s carries what looks like a secret", CI2HelmValuesFixture)
	}
}

// TestCI2DeploymentRecordedInDocs asserts the recorded delta and the checklist in
// DEVELOPMENT.md can not drift from the code: every delta key and checklist step
// must appear there, and the doc must not claim ci2 is production.
func TestCI2DeploymentRecordedInDocs(t *testing.T) {
	path := filepath.Join("..", "..", "DEVELOPMENT.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("doc not available: %v", err)
	}
	doc := string(raw)

	for _, row := range CIDeploymentDelta() {
		if !strings.Contains(doc, row.Key) {
			t.Errorf("docs do not record the delta row %q", row.Key)
		}
		if !strings.Contains(doc, row.CI2Value) {
			t.Errorf("docs do not record the ci2 value %q of row %q", row.CI2Value, row.Key)
		}
	}
	for _, step := range CI2ProvisioningChecklist() {
		if !strings.Contains(doc, strings.TrimSpace(step[strings.Index(step, ".")+1:])) {
			t.Errorf("docs do not record the checklist step %q", step)
		}
	}
	for _, want := range []string{CI2DeploymentName, CI2HelmValuesFile, CIHelmValuesFile, CI2HelmValuesFixture} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs do not name %q", want)
		}
	}
	if !strings.Contains(doc, ProductionHost) {
		t.Errorf("docs do not name production (%s) as forbidden", ProductionHost)
	}
}
