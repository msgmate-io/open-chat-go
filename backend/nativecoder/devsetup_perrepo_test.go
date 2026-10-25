package nativecoder

import (
	"strings"
	"testing"
)

// monorepoRepoPrefix is the broad fallback of DefaultDevSetupProfiles. Any
// repository that is not the monorepo itself must not resolve to it, otherwise
// it inherits the batteries-included set-up (Helm chart, mobile client, LLM
// coding context) even though its development process needs something else.
const monorepoRepoPrefix = "msgmate-io/open-chat-go"

// TestDevSetupPaletteTriggersHaveRepoProfile (ci#756) keeps the ci.msgmate.io
// configuration complete on a per-repo basis: every repository the sandbox
// palette maps a git-repo trigger to has to resolve to a development set-up
// entry of its own. Before this, the VNC/Odoo/frontend/integration repositories
// only matched the broad monorepo entry and therefore pulled the monorepo's
// batteries-included profile instead of the set-up they actually need.
func TestDevSetupPaletteTriggersHaveRepoProfile(t *testing.T) {
	palette := SandboxPaletteSnapshot()
	if len(palette.Triggers) == 0 {
		t.Fatal("sandbox palette declares no git-repo triggers")
	}
	for _, trigger := range palette.Triggers {
		repo := NormalizeRepo(trigger.Repo)
		if repo == "" {
			t.Errorf("palette trigger %q normalizes to an empty repository", trigger.Repo)
			continue
		}
		entry, ok := DevSetupRepoEntryFor(repo)
		if !ok {
			t.Errorf("%s: no development set-up default, the sandbox would stay half-set-up (ci#756)", repo)
			continue
		}
		if !strings.HasPrefix(repo, entry.RepoPrefix) {
			t.Errorf("%s: matched entry %q is not a prefix of the repository", repo, entry.RepoPrefix)
		}
		if entry.Profile == "" {
			t.Errorf("%s: matched entry %q declares no profile", repo, entry.RepoPrefix)
		}
		if repo != monorepoRepoPrefix && entry.RepoPrefix == monorepoRepoPrefix {
			t.Errorf("%s: only the broad monorepo default matches, declare a per-repo profile for this trigger (ci#756)", repo)
		}
		spec := DevSetupSpecForRepo(repo)
		if !spec.Enabled {
			t.Errorf("%s: resolved development set-up is disabled", repo)
			continue
		}
		if repo != monorepoRepoPrefix && spec.Profile == DevSetupProfileDefault {
			t.Errorf("%s: inherits the monorepo default profile %q", repo, DevSetupProfileDefault)
		}
	}
}
