package integrations

// coreOnlyIntegrationNames is the set of integrations that ship in the public
// `core-only` profile declared in integrations.yaml. Integrations in this set
// are surfaced with the "core" label in the /integrations overview; every
// other compiled integration is labelled "full".
//
// Keep this in sync with integrations.yaml. A test
// (profile_labels_test.go) parses the manifest and fails when the set drifts.
var coreOnlyIntegrationNames = map[string]struct{}{
	"mcp":           {},
	"rest_api_tool": {},
	"go_client":     {},
}
