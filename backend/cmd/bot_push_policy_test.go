package cmd

import (
	"strings"
	"testing"
)

// decodePolicyTestConfig decodes a raw bot config file the way
// ADD_BOT_FROM_CONFIG does: strict JSON, optional top-level `policy` block.
func decodePolicyTestConfig(t *testing.T, raw string) (botBootstrapConfig, error) {
	t.Helper()
	return decodeBotBootstrapConfig([]byte(raw), "test.json")
}

// TestBotBootstrapConfigPushPolicy is the ci#1138 table test: a merge/update-refs
// bot config parses, keeps its identity, inherits the default protected-branch
// policy, and an integration-supplied config may narrow (add protections) but
// never widen (drop protections) what its bot may push.
func TestBotBootstrapConfigPushPolicy(t *testing.T) {
	mergeUpdateBot := `{
		"primary_owner": "admin",
		"bot": {
			"username": "git_repo_merge_update_bot",
			"password": "BotPass1!",
			"name": "git_repo_merge_update_bot"
		},
		"policy": {
			"protected_branches": ["  release "],
			"push_denylist": ["refs/heads/release"]
		},
		"default_shared_config": {
			"description": "Merges pull requests and refreshes sub-repo refs; may never push main.",
			"model": "qwen3-4b-instruct-2507_vllm"
		}
	}`

	inheritsDefaults := `{
		"primary_owner": "admin",
		"bot": {
			"username": "git_repo_migration_reconcile_bot",
			"password": "BotPass1!",
			"name": "git_repo_migration_reconcile_bot"
		},
		"default_shared_config": {
			"description": "Reconciles conflicting Django migrations on a PR branch.",
			"model": "qwen3-4b-instruct-2507_vllm"
		}
	}`

	widenedEmptyProtected := `{
		"primary_owner": "admin",
		"bot": {
			"username": "git_repo_merge_update_bot",
			"password": "BotPass1!",
			"name": "git_repo_merge_update_bot"
		},
		"policy": { "protected_branches": [] },
		"default_shared_config": { "model": "qwen3-4b-instruct-2507_vllm" }
	}`

	widenedEmptyDenylist := `{
		"primary_owner": "admin",
		"bot": {
			"username": "git_repo_merge_update_bot",
			"password": "BotPass1!",
			"name": "git_repo_merge_update_bot"
		},
		"policy": { "push_denylist": [] },
		"default_shared_config": { "model": "qwen3-4b-instruct-2507_vllm" }
	}`

	widenedMissingProtected := `{
		"primary_owner": "admin",
		"bot": {
			"username": "git_repo_merge_update_bot",
			"password": "BotPass1!",
			"name": "git_repo_merge_update_bot"
		},
		"policy": { "protected_branches": ["release"] },
		"default_shared_config": { "model": "qwen3-4b-instruct-2507_vllm" }
	}`

	widenedMissingDenylist := `{
		"primary_owner": "admin",
		"bot": {
			"username": "git_repo_merge_update_bot",
			"password": "BotPass1!",
			"name": "git_repo_merge_update_bot"
		},
		"policy": { "push_denylist": ["refs/heads/release"] },
		"default_shared_config": { "model": "qwen3-4b-instruct-2507_vllm" }
	}`

	bothLocations := `{
		"primary_owner": "admin",
		"bot": {
			"username": "git_repo_merge_update_bot",
			"password": "BotPass1!",
			"name": "git_repo_merge_update_bot"
		},
		"policy": { "protected_branches": ["main"] },
		"default_shared_config": {
			"model": "qwen3-4b-instruct-2507_vllm",
			"policy": { "protected_branches": ["main"] }
		}
	}`

	tests := []struct {
		name string
		raw  string
		// wantDecodeErr is a substring of the decode error, "" means no error.
		wantDecodeErr string
		// wantIntegrationErr is a substring of validateIntegrationPushPolicy's
		// error, "" means an integration may supply this config.
		wantIntegrationErr string
		// wantProtected / wantDenylist are asserted against the effective policy
		// when wantDecodeErr is empty.
		wantProtected []string
		wantDenylist  []string
	}{
		{
			name:          "merge_update config parses and inherits the default policy",
			raw:           mergeUpdateBot,
			wantProtected: []string{"main", "master", "dev", "release"},
			wantDenylist:  []string{"refs/heads/main", "refs/heads/master", "refs/heads/dev", "refs/heads/release"},
		},
		{
			name:          "config without a policy block keeps the defaults untouched",
			raw:           inheritsDefaults,
			wantProtected: []string{"main", "master", "dev"},
			wantDenylist:  []string{"refs/heads/main", "refs/heads/master", "refs/heads/dev"},
		},
		{
			name:               "integration config may narrow with extra protections",
			raw:                mergeUpdateBot,
			wantProtected:      []string{"main", "master", "dev", "release"},
			wantDenylist:       []string{"refs/heads/main", "refs/heads/master", "refs/heads/dev", "refs/heads/release"},
			wantIntegrationErr: "",
		},
		{
			name:               "integration config rejecting an empty protected_branches list",
			raw:                widenedEmptyProtected,
			wantDecodeErr:      "must name at least one branch",
			wantIntegrationErr: "must name at least one branch",
		},
		{
			name:               "integration config rejecting an empty push_denylist",
			raw:                widenedEmptyDenylist,
			wantDecodeErr:      "must name at least one ref",
			wantIntegrationErr: "must name at least one ref",
		},
		{
			name:               "integration config may not drop the default protected branches",
			raw:                widenedMissingProtected,
			wantProtected:      []string{"main", "master", "dev", "release"},
			wantDenylist:       []string{"refs/heads/main", "refs/heads/master", "refs/heads/dev"},
			wantIntegrationErr: "may only narrow what the bot may push",
		},
		{
			name:               "integration config may not drop the default denylisted refs",
			raw:                widenedMissingDenylist,
			wantProtected:      []string{"main", "master", "dev"},
			wantDenylist:       []string{"refs/heads/main", "refs/heads/master", "refs/heads/dev", "refs/heads/release"},
			wantIntegrationErr: "may only narrow what the bot may push",
		},
		{
			name:          "policy declared twice is rejected",
			raw:           bothLocations,
			wantDecodeErr: "not both",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := decodePolicyTestConfig(t, tc.raw)
			if tc.wantDecodeErr != "" {
				if err == nil {
					t.Fatalf("expected decode error containing %q, got nil", tc.wantDecodeErr)
				}
				if !strings.Contains(err.Error(), tc.wantDecodeErr) {
					t.Fatalf("expected decode error containing %q, got %v", tc.wantDecodeErr, err)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected decode error: %v", err)
				}
				// The identity a catalog listing renders must stay usable: a bot
				// without a name or a description cannot be shown or dispatched.
				if strings.TrimSpace(cfg.Bot.Name) == "" {
					t.Fatalf("expected a non-empty bot.name")
				}
				description, _ := cfg.DefaultSharedConfig["description"].(string)
				if strings.TrimSpace(description) == "" {
					t.Fatalf("expected a non-empty default_shared_config.description")
				}
			}

			integrationErr := validateIntegrationPushPolicy(cfg, "test.json")
			if tc.wantIntegrationErr != "" {
				if integrationErr == nil {
					t.Fatalf("expected the integration policy check to reject the config")
				}
				if !strings.Contains(integrationErr.Error(), tc.wantIntegrationErr) {
					t.Fatalf("expected error containing %q, got %v", tc.wantIntegrationErr, integrationErr)
				}
				return
			}
			if integrationErr != nil {
				t.Fatalf("expected the integration policy check to accept the config, got %v", integrationErr)
			}

			declared, err := declaredPushPolicy(cfg)
			if err != nil {
				t.Fatalf("declaredPushPolicy failed: %v", err)
			}
			// What the bot actually gets: defaults plus whatever it declared,
			// a declaration can only add.
			effective := EffectivePushPolicy(declared)
			assertStringSet(t, "protected_branches", effective.ProtectedBranches, tc.wantProtected)
			assertStringSet(t, "push_denylist", effective.PushDenylist, tc.wantDenylist)

			// When the config declared a policy it must end up merged into
			// default_shared_config, so every dispatch inherits the full set.
			if _, declaredInline := cfg.DefaultSharedConfig[PolicySharedConfigKey]; declaredInline {
				merged, err := withEffectivePushPolicy(cfg, "test.json")
				if err != nil {
					t.Fatalf("withEffectivePushPolicy failed: %v", err)
				}
				mergedPolicy, err := declaredPushPolicy(merged)
				if err != nil {
					t.Fatalf("declaredPushPolicy on the merged config failed: %v", err)
				}
				assertStringSet(t, "merged protected_branches", mergedPolicy.ProtectedBranches, tc.wantProtected)
				assertStringSet(t, "merged push_denylist", mergedPolicy.PushDenylist, tc.wantDenylist)
			}
		})
	}
}

// TestEffectivePushPolicyMergesDefaults pins the merge semantics the bots rely
// on: defaults can be extended, never replaced.
func TestEffectivePushPolicyMergesDefaults(t *testing.T) {
	effective := EffectivePushPolicy(BotPushPolicy{})
	assertStringSet(t, "protected_branches", effective.ProtectedBranches, DefaultProtectedBranches())
	assertStringSet(t, "push_denylist", effective.PushDenylist, DefaultPushDenylist())

	// Entries are trimmed and de-duplicated instead of duplicating a default.
	extended := EffectivePushPolicy(BotPushPolicy{ProtectedBranches: []string{" main ", " release ", "release"}})
	assertStringSet(t, "protected_branches", extended.ProtectedBranches, []string{"main", "master", "dev", "release"})

	// A config can never remove a default from the effective policy.
	if got := EffectivePushPolicy(BotPushPolicy{ProtectedBranches: []string{"release"}}); len(got.ProtectedBranches) != 4 {
		t.Fatalf("expected the defaults to be preserved, got %v", got.ProtectedBranches)
	}
}

func assertStringSet(t *testing.T, label string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: expected %v, got %v", label, want, got)
	}
	set := make(map[string]struct{}, len(got))
	for _, value := range got {
		set[value] = struct{}{}
	}
	for _, value := range want {
		if _, ok := set[value]; !ok {
			t.Fatalf("%s: expected %v, got %v (missing %q)", label, want, got, value)
		}
	}
}
