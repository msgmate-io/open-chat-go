package publicdocs

// Coverage guard for ci#1052 (Test Coverage For Identity Provider).
//
// The generic OIDC identity-provider integration
// (msgmate-io/open-chat-go-identity-provider-integration) ships two artefacts
// into this repository:
//
//   - a private documentation page (visibility: integration), and
//   - a throw-away Keycloak sandbox stack plus a scripted login check that
//     exercises the authorization-code + PKCE flow end to end without any LLM
//     call.
//
// Both are load-bearing: the docs are the only place where the runtime
// configuration is written down and the sandbox is what the no-LLM e2e check
// runs. This file keeps them honest (and keeps the misspelled legacy repository
// name from creeping back in).

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	identitySandboxDir   = "development/sandbox/identity_provider"
	identityRealmFile    = "realm/open-chat-realm.json"
	identityComposeFile  = "docker-compose.yaml"
	identityVerifyScript = "verify_login.py"
	identityDocsPage     = "frontend/pages/docs/content/identity-provider.mdx"
	// identityCallbackPath is the callback route the integration registers and
	// the identity provider has to allow-list.
	identityCallbackPath = "/api/v1/integrations/identity_provider/login/callback"
	// legacyIdentityRepoNames are the misspelled repository/module spellings
	// that must not reappear anywhere in this tree.
	legacyIdentityRepoNames = "intentity"
)

type identityRealm struct {
	Realm   string `json:"realm"`
	SSL     string `json:"sslRequired"`
	Clients []struct {
		ClientID     string            `json:"clientId"`
		PublicClient bool              `json:"publicClient"`
		Secret       string            `json:"secret"`
		StandardFlow bool              `json:"standardFlowEnabled"`
		RedirectURIs []string          `json:"redirectUris"`
		Attributes   map[string]string `json:"attributes"`
	} `json:"clients"`
	Users []struct {
		Username string `json:"username"`
		Enabled  bool   `json:"enabled"`
	} `json:"users"`
}

func readIdentityRealm(t *testing.T) identityRealm {
	t.Helper()

	path := filepath.Join(repoRoot(t), identitySandboxDir, identityRealmFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read sandbox realm: %v", err)
	}

	var realm identityRealm
	if err := json.Unmarshal(raw, &realm); err != nil {
		t.Fatalf("sandbox realm is not valid JSON: %v", err)
	}
	return realm
}

// TestIdentityProviderSandboxStack checks that the Keycloak sandbox stack that
// the no-LLM e2e login check drives is complete and self-consistent.
func TestIdentityProviderSandboxStack(t *testing.T) {
	root := repoRoot(t)
	sandbox := filepath.Join(root, identitySandboxDir)

	for _, rel := range []string{identityComposeFile, identityVerifyScript, identityRealmFile} {
		if _, err := os.Stat(filepath.Join(sandbox, rel)); err != nil {
			t.Fatalf("%s/%s is missing: %v", identitySandboxDir, rel, err)
		}
	}

	compose, err := os.ReadFile(filepath.Join(sandbox, identityComposeFile))
	if err != nil {
		t.Fatalf("read sandbox compose file: %v", err)
	}
	composeText := string(compose)
	for _, want := range []string{
		"quay.io/keycloak/keycloak:",
		"--import-realm",
		"healthcheck:",
		"realm:/opt/keycloak/data/import:ro",
	} {
		if !strings.Contains(composeText, want) {
			t.Errorf("sandbox compose file must contain %q", want)
		}
	}

	realm := readIdentityRealm(t)
	if realm.Realm != "open-chat" {
		t.Errorf("sandbox realm name = %q, want %q", realm.Realm, "open-chat")
	}
	if realm.SSL != "none" {
		t.Errorf("sandbox realm sslRequired = %q, want %q (plain http sandbox)", realm.SSL, "none")
	}

	var client *struct {
		ClientID     string            `json:"clientId"`
		PublicClient bool              `json:"publicClient"`
		Secret       string            `json:"secret"`
		StandardFlow bool              `json:"standardFlowEnabled"`
		RedirectURIs []string          `json:"redirectUris"`
		Attributes   map[string]string `json:"attributes"`
	}
	for i := range realm.Clients {
		if realm.Clients[i].ClientID == "open-chat" {
			client = &realm.Clients[i]
			break
		}
	}
	if client == nil {
		t.Fatal("sandbox realm must define the 'open-chat' OIDC client")
	}
	if client.PublicClient {
		t.Error("the 'open-chat' client must be confidential (client secret + code exchange)")
	}
	if client.Secret == "" {
		t.Error("the confidential sandbox client needs a secret")
	}
	if !client.StandardFlow {
		t.Error("the authorization-code flow must be enabled")
	}
	if got := client.Attributes["pkce.code.challenge.method"]; got != "S256" {
		t.Errorf("pkce.code.challenge.method = %q, want S256", got)
	}

	callbackRegistered := false
	for _, uri := range client.RedirectURIs {
		if strings.HasSuffix(uri, identityCallbackPath) {
			callbackRegistered = true
			break
		}
	}
	if !callbackRegistered {
		t.Errorf("sandbox realm must register the integration callback %q, got %v", identityCallbackPath, client.RedirectURIs)
	}

	seeded := false
	for _, user := range realm.Users {
		if user.Username == "sandbox-user" && user.Enabled {
			seeded = true
			break
		}
	}
	if !seeded {
		t.Error("sandbox realm must seed an enabled 'sandbox-user' to log in with")
	}
}

// TestIdentityProviderDocsPage checks that the private documentation page is
// gated to the integration and documents the runtime configuration and a
// concrete setup example.
func TestIdentityProviderDocsPage(t *testing.T) {
	root := repoRoot(t)

	raw, err := os.ReadFile(filepath.Join(root, identityDocsPage))
	if err != nil {
		t.Fatalf("read identity-provider docs page: %v", err)
	}
	page := string(raw)

	frontmatter, body, found := strings.Cut(page, "\n---\n")
	if !found {
		t.Fatalf("%s must start with a frontmatter block", identityDocsPage)
	}
	for _, want := range []string{
		"visibility: integration",
		"integration: identity_provider",
	} {
		if !strings.Contains(frontmatter, want) {
			t.Errorf("docs frontmatter must contain %q", want)
		}
	}

	for _, want := range []string{
		"OCI_IDENTITY_PROVIDER_ENABLED",
		"OCI_IDENTITY_PROVIDER_ISSUER",
		"OCI_IDENTITY_PROVIDER_CLIENT_ID",
		"OCI_IDENTITY_PROVIDER_CLIENT_SECRET",
		"OCI_IDENTITY_PROVIDER_REDIRECT_URL",
		"OCI_IDENTITY_PROVIDER_PRESET",
		"OCI_IDENTITY_PROVIDER_BOOTSTRAP_JSON",
		identityCallbackPath,
		"open-chat-go-identity-provider-integration",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("docs body must document %q", want)
		}
	}

	// A setup example for at least one concrete identity provider plus the
	// sandbox instructions that keep the example reproducible.
	for _, want := range []string{
		"Keycloak", "Authentik", identityVerifyScript,
		"integrations.private.yaml", "migrate-identity-provider-integration.sh",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("docs body must mention %q", want)
		}
	}
}

// TestNoLegacyIdentityProviderRepoReferences fails when the misspelled legacy
// repository name is still referenced somewhere in this tree (ci#1052 moved the
// integration to `open-chat-go-identity-provider-integration`).
func TestNoLegacyIdentityProviderRepoReferences(t *testing.T) {
	root := repoRoot(t)

	skipDirs := map[string]bool{
		".git": true, "node_modules": true, "clients": true, "dist": true,
		"frontend_assets": true, ".integrations": true, "vendor": true,
	}

	// generatedPaths are not repository content: `openchat-integrations sync`
	// materializes them from the (private) integration manifests at build time,
	// so they are gitignored here and are not ours to police. The rename of the
	// integration *there* - the private `development/ci` manifest key still
	// spells the legacy name - happens in the ci repository (see
	// development/scripts/migrate-identity-provider-integration.sh). Scanning
	// those paths would only report artefacts of the machine the test happens
	// to run on, which is why the guard used to fail in CI while passing on a
	// tree where the integration had never been synced.
	generatedPaths := []string{
		filepath.Join("backend", "go.work"),
		filepath.Join("backend", "go.work.sum"),
		filepath.Join("backend", "integrations", "externalintegrations", "imports_gen.go"),
		filepath.Join("backend", "integrations", "externalintegrations", "sources_gen.go"),
		filepath.Join("development", "ci"),
		filepath.Join("development", "helm"),
	}

	isGenerated := func(rel string) bool {
		for _, generated := range generatedPaths {
			if rel == generated || strings.HasPrefix(rel, generated+string(filepath.Separator)) {
				return true
			}
		}
		return false
	}

	// These two files are the detectors themselves: they have to spell the
	// legacy name out in order to look for it.
	guardFiles := map[string]bool{
		"identity_provider_sandbox_test.go": true,
		identityVerifyScript:                true,
	}

	var offenders []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		if d.IsDir() {
			if path == root {
				return nil
			}
			name := d.Name()
			if skipDirs[name] || (strings.HasPrefix(name, ".") && name != ".github") || isGenerated(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if isGenerated(rel) || guardFiles[d.Name()] {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > 2<<20 {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if strings.Contains(string(content), legacyIdentityRepoNames) {
			offenders = append(offenders, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk repository: %v", err)
	}

	if len(offenders) > 0 {
		t.Errorf("misspelled legacy identity-provider repo name %q still referenced in: %s",
			legacyIdentityRepoNames, strings.Join(offenders, ", "))
	}
}
