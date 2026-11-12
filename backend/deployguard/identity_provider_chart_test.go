package deployguard

// Guard for ci#1061 (connect a real IdP deployment to https://msgmate.io).
//
// The chart itself lives in the private repository
// msgmate-io/open-chat-go-helm-chart; this repository carries the chart change
// as a patch plus a README (the ci#667 precedent, see
// development/helm-postgresql/). Nothing here can run `helm template`, so these
// tests pin the invariants that make the change safe to ship before the IdP
// exists:
//
//   - the base values keep `identityProvider.enabled: false`, and the template
//     only renders `OCI_IDENTITY_PROVIDER_*` env behind that switch, so a default
//     render is unchanged and the integration keeps failing closed (404);
//   - the deployment overlays enable it with a complete, explicit configuration
//     (an enabled-but-incomplete configuration answers 503);
//   - no credential is ever committed: the client secret and the
//     identity-to-user map come from a Secret via `secretKeyRef`;
//   - the env names the chart renders are exactly the ones the integration
//     documents, and the callback URL is the route the integration registers.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

const (
	identityPatchPath  = "development/helm-identity-provider/ci-1061-identity-provider.patch"
	identityReadmePath = "development/helm-identity-provider/README.md"

	chartValuesPath        = "backend/values.yaml"
	chartTemplatePath      = "backend/templates/backend.yaml"
	chartProductionOverlay = "backend/values.msgmate-io-production.yaml"
	chartStagingOverlay    = "backend/values.msgmate-io-staging.yaml"

	identityProviderDocsPage = "frontend/pages/docs/content/identity-provider.mdx"

	// identityProviderCallbackPath is the callback the integration registers and
	// therefore the URL every IdP client has to allow-list.
	identityProviderCallbackPath = "/api/v1/integrations/identity_provider/login/callback"

	identityProviderEnvPrefix = "OCI_IDENTITY_PROVIDER_"
	identityProviderKey       = "identityProvider:"
)

// identityProviderValues mirrors the `identityProvider` block the patch adds to
// the chart values; only the keys these assertions care about are decoded.
type identityProviderValues struct {
	IdentityProvider struct {
		Enabled          bool   `yaml:"enabled"`
		Preset           string `yaml:"preset"`
		Issuer           string `yaml:"issuer"`
		ClientID         string `yaml:"clientID"`
		RedirectURL      string `yaml:"redirectURL"`
		Scopes           string `yaml:"scopes"`
		DefaultRole      string `yaml:"defaultRole"`
		AllowUnmapped    bool   `yaml:"allowUnmapped"`
		ExistingSecret   string `yaml:"existingSecret"`
		ClientSecretKey  string `yaml:"clientSecretKey"`
		BootstrapJSONKey string `yaml:"bootstrapJSONKey"`
	} `yaml:"identityProvider"`
}

func deployRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "DEVELOPMENT.md")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "backend")); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no repository root above %s", dir)
		}
		dir = parent
	}
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(deployRepoRoot(t), rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// patchSections splits a unified diff into per-file hunks, keyed by the b-side
// path. Added and context lines are kept, dropped lines discarded, so each
// section reads like the patched file for the regions the diff covers.
func patchSections(t *testing.T, patch string) map[string]string {
	t.Helper()
	sections := map[string]string{}
	current := ""
	var b strings.Builder
	flush := func() {
		if current != "" {
			sections[current] += b.String()
		}
		b.Reset()
	}
	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flush()
			current = ""
		case strings.HasPrefix(line, "+++ "):
			path := strings.TrimPrefix(line, "+++ b/")
			path = strings.TrimPrefix(path, "+++ ")
			current = strings.TrimSpace(path)
		case current == "":
			// header/context before the first file header
		case strings.HasPrefix(line, "+"):
			b.WriteString(line[1:])
			b.WriteString("\n")
		case strings.HasPrefix(line, " "):
			b.WriteString(line[1:])
			b.WriteString("\n")
		}
	}
	flush()
	if len(sections) == 0 {
		t.Fatalf("%s carries no hunks", identityPatchPath)
	}
	return sections
}

// topLevelBlock returns the mapping that starts at the given top-level key of a
// patch section. The chart change appends the block at the end of each values
// file, so the block runs from its key to the end of the section; leading
// partial context of the enclosing document is dropped so the result parses as
// standalone YAML.
func topLevelBlock(t *testing.T, name, section, key string) string {
	t.Helper()
	lines := strings.Split(section, "\n")
	start := -1
	for i, line := range lines {
		if line == key {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("%s does not add a top-level %q block (%s)", identityPatchPath, key, name)
	}
	return strings.Join(lines[start:], "\n")
}

func decodeIdentityProviderValues(t *testing.T, name, section string) identityProviderValues {
	t.Helper()
	var out identityProviderValues
	if err := yaml.Unmarshal([]byte(topLevelBlock(t, name, section, identityProviderKey)), &out); err != nil {
		t.Fatalf("%s: parse added %s block as YAML: %v", name, identityProviderKey, err)
	}
	return out
}

func identityProviderSections(t *testing.T) map[string]string {
	t.Helper()
	return patchSections(t, readRepoFile(t, identityPatchPath))
}

// TestIdentityProviderChartValuesAreAdditiveAndFailClosed pins the base values:
// disabled by default, no issuer/client/secret, and a documented secret layout.
func TestIdentityProviderChartValuesAreAdditiveAndFailClosed(t *testing.T) {
	sections := identityProviderSections(t)

	section, ok := sections[chartValuesPath]
	if !ok {
		t.Fatalf("%s does not add the identityProvider block to %s", identityPatchPath, chartValuesPath)
	}
	base := decodeIdentityProviderValues(t, chartValuesPath, section).IdentityProvider

	if base.Enabled {
		t.Error("identityProvider.enabled must default to false: a default render has to stay free of OCI_IDENTITY_PROVIDER_* env so the integration keeps answering 404")
	}
	if base.Issuer != "" || base.ClientID != "" || base.RedirectURL != "" {
		t.Errorf("identityProvider defaults must be empty, got issuer=%q clientID=%q redirectURL=%q", base.Issuer, base.ClientID, base.RedirectURL)
	}
	if base.ExistingSecret != "" {
		t.Errorf("identityProvider.existingSecret must default to empty, got %q", base.ExistingSecret)
	}
	if base.AllowUnmapped {
		t.Error("identityProvider.allowUnmapped must default to false: an unmapped identity must never get an account")
	}
	if base.Preset == "" {
		t.Error("identityProvider.preset needs a documented default so an operator only has to set issuer/clientID/secret")
	}
	if !strings.Contains(base.Scopes, "openid") {
		t.Errorf("identityProvider.scopes must include `openid`, got %q", base.Scopes)
	}
	if base.ClientSecretKey == "" || base.BootstrapJSONKey == "" {
		t.Errorf("identityProvider must document the Secret keys it reads, got clientSecretKey=%q bootstrapJSONKey=%q", base.ClientSecretKey, base.BootstrapJSONKey)
	}
	if base.ClientSecretKey == base.BootstrapJSONKey {
		t.Errorf("clientSecretKey and bootstrapJSONKey must differ, both are %q", base.ClientSecretKey)
	}
}

// TestIdentityProviderOverlaysAreComplete checks that the deployment profiles
// ship a complete configuration: an enabled-but-incomplete integration answers
// 503, which is exactly the half-configured state this ticket must avoid.
func TestIdentityProviderOverlaysAreComplete(t *testing.T) {
	sections := identityProviderSections(t)

	cases := []struct {
		path string
		host string
	}{
		{chartProductionOverlay, "msgmate.io"},
		{chartStagingOverlay, "stage.msgmate.io"},
	}
	for _, tc := range cases {
		t.Run(tc.host, func(t *testing.T) {
			section, ok := sections[tc.path]
			if !ok {
				t.Fatalf("%s does not add an identityProvider block to %s", identityPatchPath, tc.path)
			}
			idp := decodeIdentityProviderValues(t, tc.path, section).IdentityProvider

			if !idp.Enabled {
				t.Fatalf("%s must set identityProvider.enabled: true", tc.path)
			}
			if idp.Preset != "keycloak" {
				t.Errorf("%s: preset = %q, want the provisioned keycloak preset", tc.path, idp.Preset)
			}
			if !strings.HasPrefix(idp.Issuer, "https://") || !strings.Contains(idp.Issuer, "/realms/") {
				t.Errorf("%s: issuer = %q, want an https Keycloak realm URL", tc.path, idp.Issuer)
			}
			if idp.ClientID == "" {
				t.Errorf("%s: clientID must name the confidential OIDC client", tc.path)
			}
			wantRedirect := "https://" + tc.host + identityProviderCallbackPath
			if idp.RedirectURL != wantRedirect {
				t.Errorf("%s: redirectURL = %q, want %q (an inexact value is the usual cause of redirect_uri_mismatch)", tc.path, idp.RedirectURL, wantRedirect)
			}
			if idp.ExistingSecret == "" {
				t.Errorf("%s: existingSecret must name the Secret holding the client secret and the identity map", tc.path)
			}
			if idp.AllowUnmapped {
				t.Errorf("%s: allowUnmapped must stay false in a managed deployment", tc.path)
			}
		})
	}
}

// TestIdentityProviderTemplateRendersEnvBehindSwitch is the core fail-closed
// guard: the OCI_IDENTITY_PROVIDER_* env may only appear inside the
// `if $idpEnabled` branch, and the render must fail when the configuration is
// incomplete instead of emitting a half-configured release.
func TestIdentityProviderTemplateRendersEnvBehindSwitch(t *testing.T) {
	sections := identityProviderSections(t)

	template, ok := sections[chartTemplatePath]
	if !ok {
		t.Fatalf("%s does not touch %s", identityPatchPath, chartTemplatePath)
	}

	firstEnv := strings.Index(template, "- name: "+identityProviderEnvPrefix)
	if firstEnv < 0 {
		t.Fatalf("%s renders no %s* env", chartTemplatePath, identityProviderEnvPrefix)
	}

	// Render guards: the completeness checks sit between the switch that
	// declares $idpEnabled and the env entries.
	guardStart := strings.Index(template, "{{- if $idpEnabled }}")
	if guardStart < 0 {
		t.Fatalf("%s must gate the identity-provider env on $idpEnabled", chartTemplatePath)
	}
	guardBlock := template[guardStart:firstEnv]
	if got := strings.Count(guardBlock, `fail "identityProvider.enabled requires`); got < 3 {
		t.Errorf("%s has %d identityProvider render guards, want at least 3 (issuer, clientID, existingSecret); got:\n%s", chartTemplatePath, got, guardBlock)
	}
	if !strings.Contains(readRepoFile(t, identityPatchPath), "$idpEnabled := ") {
		t.Errorf("%s must derive $idpEnabled from .Values.identityProvider.enabled", chartTemplatePath)
	}

	// Every OCI_* reference has to live inside the guarded env list, i.e. after
	// the switch that opens it and before the envFrom that closes the list.
	envSwitchAt := strings.LastIndex(template[:firstEnv], "{{- if $idpEnabled }}")
	if envSwitchAt < 0 {
		t.Fatalf("%s renders %s* without an $idpEnabled guard, so it leaks into a default render", chartTemplatePath, identityProviderEnvPrefix)
	}
	envFromRel := strings.Index(template[firstEnv:], "envFrom:")
	if envFromRel < 0 {
		t.Fatalf("%s: could not delimit the env block that follows the identity-provider branch", chartTemplatePath)
	}
	envBlock := template[envSwitchAt : firstEnv+envFromRel]

	if got, want := strings.Count(template, identityProviderEnvPrefix), strings.Count(envBlock, identityProviderEnvPrefix); got != want {
		t.Errorf("%s references %s* %d times but only %d inside the guarded env block; every reference must be switched so a default render stays unchanged", chartTemplatePath, identityProviderEnvPrefix, got, want)
	}
	if !strings.Contains(envBlock, "{{- end }}") {
		t.Errorf("%s must close the identity-provider env branch before envFrom, got:\n%s", chartTemplatePath, envBlock)
	}

	// No credential may be rendered from a values key: both secret-bearing
	// variables have to come from the Secret.
	for _, secretEnv := range []string{
		identityProviderEnvPrefix + "CLIENT_SECRET",
		identityProviderEnvPrefix + "BOOTSTRAP_JSON",
	} {
		idx := strings.Index(envBlock, secretEnv)
		if idx < 0 {
			t.Errorf("%s does not render %s", chartTemplatePath, secretEnv)
			continue
		}
		tail := envBlock[idx:]
		if end := strings.Index(tail, "- name: "); end > 0 {
			tail = tail[:end]
		}
		if !strings.Contains(tail, "secretKeyRef") {
			t.Errorf("%s renders %s without secretKeyRef; the value must come from a Secret, got:\n%s", chartTemplatePath, secretEnv, tail)
		}
		if strings.Contains(tail, "value:") {
			t.Errorf("%s renders %s with an inline value; no credential may live in the chart, got:\n%s", chartTemplatePath, secretEnv, tail)
		}
	}

	// The non-secret settings are plain values, but the boolean has to be
	// stringified so Kubernetes does not reject the env entry.
	allowUnmappedIdx := strings.Index(envBlock, identityProviderEnvPrefix+"ALLOW_UNMAPPED")
	if allowUnmappedIdx < 0 {
		t.Errorf("%s does not render %sALLOW_UNMAPPED", chartTemplatePath, identityProviderEnvPrefix)
	} else {
		tail := envBlock[allowUnmappedIdx:]
		if end := strings.Index(tail, "- name: "); end > 0 {
			tail = tail[:end]
		}
		if strings.Contains(tail, "value: true") && !strings.Contains(tail, "toString") {
			t.Errorf("%s must derive ALLOW_UNMAPPED from values with toString, got:\n%s", chartTemplatePath, tail)
		}
	}

	if !strings.Contains(envBlock, "optional: true") {
		t.Error("the bootstrap-json key must be optional so a release can start before the identity map is known")
	}
}

// TestIdentityProviderEnvMatchesDocumentedContract pins the chart's env names to
// the integration's documented configuration: a typo here would leave the
// integration enabled-but-incomplete (503) instead of failing the render.
func TestIdentityProviderEnvMatchesDocumentedContract(t *testing.T) {
	sections := identityProviderSections(t)
	template := sections[chartTemplatePath]

	envNameRe := regexp.MustCompile(identityProviderEnvPrefix + `[A-Z_]+`)
	rendered := map[string]bool{}
	for _, name := range envNameRe.FindAllString(template, -1) {
		if strings.HasSuffix(name, "_") {
			continue
		}
		rendered[name] = true
	}
	if len(rendered) == 0 {
		t.Fatalf("%s renders no %s* env", chartTemplatePath, identityProviderEnvPrefix)
	}

	docs := readRepoFile(t, identityProviderDocsPage)
	documented := map[string]bool{}
	for _, name := range envNameRe.FindAllString(docs, -1) {
		if strings.HasSuffix(name, "_") {
			// prose mention of the `OCI_IDENTITY_PROVIDER_*` prefix
			continue
		}
		documented[name] = true
	}
	if len(documented) == 0 {
		t.Fatalf("%s documents no %s* variables", identityProviderDocsPage, identityProviderEnvPrefix)
	}

	for name := range rendered {
		if !documented[name] {
			t.Errorf("the chart renders %s but %s does not document it", name, identityProviderDocsPage)
		}
	}
	for name := range documented {
		if !rendered[name] {
			t.Errorf("%s documents %s but the chart never renders it, so an enabled release would stay incomplete", identityProviderDocsPage, name)
		}
	}

	if !strings.Contains(docs, identityProviderCallbackPath) {
		t.Errorf("%s does not name the callback %s the overlays register", identityProviderDocsPage, identityProviderCallbackPath)
	}
}

// TestIdentityProviderChartCarriesNoCredentials makes sure the committed patch
// and README never carry a secret value.
func TestIdentityProviderChartCarriesNoCredentials(t *testing.T) {
	patch := readRepoFile(t, identityPatchPath)
	readme := readRepoFile(t, identityReadmePath)

	// A credential would show up as a values key holding a non-empty scalar.
	secretKeys := regexp.MustCompile(`(?m)^(?:clientSecret|client_secret|clientKey|password|token|secret):\s*\S+`)
	for _, section := range patchSections(t, patch) {
		if m := secretKeys.FindString(section); m != "" {
			t.Errorf("%s carries a credential value: %q", identityPatchPath, strings.TrimSpace(m))
		}
	}

	for _, want := range []string{"secretKeyRef", "enabled: false", "Rollback"} {
		if !strings.Contains(readme, want) && !strings.Contains(patch, want) {
			t.Errorf("neither the README nor the patch documents %q", want)
		}
	}
	if !strings.Contains(readme, "open-chat-identity-provider") {
		t.Error("the README must name the Secret the overlays reference so the operator knows what to create")
	}
}
