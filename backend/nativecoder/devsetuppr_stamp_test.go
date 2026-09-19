package nativecoder

import (
	"strings"
	"testing"
)

// TestDevSetupVerificationStampPromise guards the zero-diff verification stamp
// PR for ci#756 (Optimize / Improve Open-Chat-Go Development set-up): the
// development set-up audit surface (per-repo matrix + per-bot fleet) must stay
// wired so the operator report can keep failing a config publish on coverage
// gaps. It is the in-tree promise, on main, that the ticket's implementation is
// resident and verifiable.
func TestDevSetupVerificationStampPromise(t *testing.T) {
	if DevSetupConfigKey == "" {
		t.Fatalf("DevSetupConfigKey must be set (coder_dev_setup)")
	}
	if len(DefaultDevSetupProfiles) < 4 {
		t.Fatalf("expected the per-repo dev-setup profile catalogue, got %d entries", len(DefaultDevSetupProfiles))
	}
	known := map[string]bool{}
	for _, entry := range DefaultDevSetupProfiles {
		known[entry.Profile] = true
	}
	for _, profile := range []string{DevSetupProfileDefault, DevSetupProfileFallback, "full-android", "core-only"} {
		if !known[profile] {
			t.Fatalf("missing known profile %q in the dev-setup catalogue", profile)
		}
	}
	fragment := RenderDevSetupConfigFragment()
	for _, want := range []string{DevSetupConfigKey + ":", "submodules: true", "msgmate-io/open-chat-go"} {
		if !strings.Contains(fragment, want) {
			t.Fatalf("config fragment lacks %q", want)
		}
	}
	if gaps := DevSetupFleetGaps(DevSetupFleetPlans(CoderDevSetupFleet)); len(gaps) != 0 {
		t.Fatalf("verification stamp requires zero fleet coverage gaps, got: %v", gaps)
	}
}
