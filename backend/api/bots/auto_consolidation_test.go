package bots

import (
	"encoding/json"
	"testing"

	"backend/api/chats"
	"backend/database"
	"backend/nativecoder"
)

// TestAutoConsolidationDefaultAppliedToCoderBotConfig guards the default-ON
// wiring of ci#653: a bot config that declares a native coder agent gets the
// standard `auto_consolidation` block merged in through the shared
// bot-config defaults path, and a non-coder config does not.
func TestAutoConsolidationDefaultAppliedToCoderBotConfig(t *testing.T) {
	t.Setenv(nativecoder.AutoConsolidationEnvKey, "")

	coder := map[string]interface{}{
		"model":             "some-model",
		"backend":           "litellm",
		"coder_interaction": "autonomous",
	}
	applied, err := applyIntegrationDefaultsForUser(nil, nil, coder)
	if err != nil {
		t.Fatalf("defaults failed: %v", err)
	}
	raw, ok := applied[nativecoder.AutoConsolidationConfigKey].(map[string]interface{})
	if !ok {
		t.Fatalf("expected auto_consolidation on a coder bot config, got %#v", applied[nativecoder.AutoConsolidationConfigKey])
	}
	if raw["enabled"] != true {
		t.Fatalf("expected enabled=true, got %#v", raw["enabled"])
	}

	plain := map[string]interface{}{
		"model":   "some-model",
		"backend": "deepinfra",
		"tools":   []interface{}{"get_weather"},
	}
	appliedPlain, err := applyIntegrationDefaultsForUser(nil, nil, plain)
	if err != nil {
		t.Fatalf("defaults failed: %v", err)
	}
	if _, present := appliedPlain[nativecoder.AutoConsolidationConfigKey]; present {
		t.Fatalf("a non-coder bot config must not get auto_consolidation: %#v", appliedPlain)
	}
}

// TestAutoConsolidationDefaultRespectsPerAgentToggle guards that an explicit
// per-agent block wins over the wiring default.
func TestAutoConsolidationDefaultRespectsPerAgentToggle(t *testing.T) {
	t.Setenv(nativecoder.AutoConsolidationEnvKey, "")

	coder := map[string]interface{}{
		"coder_interaction": "autonomous",
		nativecoder.AutoConsolidationConfigKey: map[string]interface{}{
			"enabled": false,
		},
	}
	applied, err := applyIntegrationDefaultsForUser(nil, nil, coder)
	if err != nil {
		t.Fatalf("defaults failed: %v", err)
	}
	raw, ok := applied[nativecoder.AutoConsolidationConfigKey].(map[string]interface{})
	if !ok || raw["enabled"] != false {
		t.Fatalf("explicit enabled=false must be preserved, got %#v", applied[nativecoder.AutoConsolidationConfigKey])
	}
}

// TestRunAutoConsolidationSkipsNonCoderChats guards that the completion hook is
// inert for a chat that is not a native coder agent.
func TestRunAutoConsolidationSkipsNonCoderChats(t *testing.T) {
	ResetAutoConsolidationCooldownForTest()
	calls := 0
	previous := nativecoder.ConsolidationClientFactory
	nativecoder.ConsolidationClientFactory = func(repo string) (nativecoder.ConsolidationClient, error) {
		calls++
		return nil, nil
	}
	t.Cleanup(func() { nativecoder.ConsolidationClientFactory = previous })

	chat := database.Chat{Model: database.Model{ID: 1}, ChatType: "interaction", SharedConfig: sharedConfigForTest(t, map[string]interface{}{
		"model":   "some-model",
		"backend": "deepinfra",
		"tools":   []interface{}{"get_weather"},
	})}
	runAutoConsolidation(nil, chat)
	if calls != 0 {
		t.Fatalf("expected no client construction for a non-coder chat, got %d", calls)
	}
}

// TestRunAutoConsolidationRunsOnceForAReadyCoderChat covers the wired pass for a
// native coder chat: the configured policy reaches the runner, the repository is
// resolved from the shared config, and a second pass within the cooldown does
// not run again.
func TestRunAutoConsolidationRunsOnceForAReadyCoderChat(t *testing.T) {
	ResetAutoConsolidationCooldownForTest()
	t.Setenv(nativecoder.AutoConsolidationEnvKey, "")

	var repos []string
	previous := nativecoder.ConsolidationClientFactory
	nativecoder.ConsolidationClientFactory = func(repo string) (nativecoder.ConsolidationClient, error) {
		repos = append(repos, repo)
		return nil, nil // no-op client: the run is skipped, not acted on
	}
	t.Cleanup(func() { nativecoder.ConsolidationClientFactory = previous })

	chat := database.Chat{Model: database.Model{ID: 1}, ChatType: "interaction", SharedConfig: sharedConfigForTest(t, map[string]interface{}{
		"model":             "some-model",
		"backend":           "litellm",
		"coder_interaction": "autonomous",
		nativecoder.AutoConsolidationConfigKey: map[string]interface{}{
			"enabled": true,
		},
		autoConsolidationRepoConfigKey: "msgmate-io/open-chat-go",
	})}

	runAutoConsolidation(nil, chat)
	runAutoConsolidation(nil, chat)

	if len(repos) != 1 {
		t.Fatalf("expected exactly one pass, got %d (%v)", len(repos), repos)
	}
	if repos[0] != "msgmate-io/open-chat-go" {
		t.Fatalf("expected the configured repo, got %q", repos[0])
	}
}

// TestAutoConsolidationRepoPrefersConfigThenEnv guards the repository resolution.
func TestAutoConsolidationRepoPrefersConfigThenEnv(t *testing.T) {
	t.Setenv(nativecoder.GitHubRepoEnv, "msgmate-io/from-env")
	if got := autoConsolidationRepo(map[string]interface{}{}); got != "msgmate-io/from-env" {
		t.Fatalf("repo = %q, want the deployment env value", got)
	}
	if got := autoConsolidationRepo(map[string]interface{}{autoConsolidationRepoConfigKey: "msgmate-io/from-config"}); got != "msgmate-io/from-config" {
		t.Fatalf("repo = %q, want the per-agent value", got)
	}
	t.Setenv(nativecoder.GitHubRepoEnv, "")
	if got := autoConsolidationRepo(map[string]interface{}{}); got != "" {
		t.Fatalf("repo = %q, want empty when nothing is configured", got)
	}
}

// TestAutoConsolidationRunnerIsRegistered guards that the choke point reaches an
// executor (an unregistered hook would silently disable the feature).
func TestAutoConsolidationRunnerIsRegistered(t *testing.T) {
	if !chats.AutoConsolidationRunnerRegistered() {
		t.Fatal("expected the auto-consolidation executor to be registered at init")
	}
}

func sharedConfigForTest(t *testing.T, config map[string]interface{}) *database.SharedChatConfig {
	t.Helper()
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("failed to encode shared config: %v", err)
	}
	return &database.SharedChatConfig{ConfigData: encoded}
}

// TestAutoConsolidationRunnerRespectsDeploymentKillSwitch guards the fleet-wide
// rollback: with the kill switch set, a coder chat is left alone even though its
// own block is absent.
func TestAutoConsolidationRunnerRespectsDeploymentKillSwitch(t *testing.T) {
	ResetAutoConsolidationCooldownForTest()
	t.Setenv(nativecoder.AutoConsolidationEnvKey, "off")

	calls := 0
	previous := nativecoder.ConsolidationClientFactory
	nativecoder.ConsolidationClientFactory = func(repo string) (nativecoder.ConsolidationClient, error) {
		calls++
		return nil, nil
	}
	t.Cleanup(func() { nativecoder.ConsolidationClientFactory = previous })

	chat := database.Chat{Model: database.Model{ID: 1}, ChatType: "interaction", SharedConfig: sharedConfigForTest(t, map[string]interface{}{
		"coder_interaction":            "autonomous",
		autoConsolidationRepoConfigKey: "msgmate-io/open-chat-go",
	})}
	runAutoConsolidation(nil, chat)
	if calls != 0 {
		t.Fatalf("the deployment kill switch must suppress the pass, got %d client(s)", calls)
	}

	// Sanity: the same chat does run once the switch is unset.
	t.Setenv(nativecoder.AutoConsolidationEnvKey, "")
	runAutoConsolidation(nil, chat)
	if calls != 1 {
		t.Fatalf("expected the pass to run once the switch is unset, got %d client(s)", calls)
	}
}
