package bots

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"time"

	"backend/api/chats"
	"backend/database"
	"backend/nativecoder"

	"gorm.io/gorm"
)

// This file wires the native coder's auto-consolidation default (ci#653) into
// the shared interaction-completion choke point.
//
// A native `msgmate_coder` agent is defaulted to `auto_consolidation` ON (see
// nativecoder.ApplyAutoConsolidationDefault and the integration's
// SharedConfigDefaults contribution). At the completion choke point used by the
// auto-continuation loop (ci#537) and the post-interaction hook (ci#649) the
// core therefore runs one consolidation pass for such an agent: it detects the
// ready open PRs (green, non-draft, mergeable, targeting `main`, authored by the
// agent), opens/refreshes exactly ONE `chore/consolidate-<date>` PR that folds
// them in, applies the standard label set, and merges it through the existing
// auto-approve path (ci#538) once every required check is green — rolling later
// ready PRs into the NEXT consolidation (ci#652).
//
// It is deliberately conservative and a no-op unless the deployment configured a
// GitHub token and repository (`OPEN_CHAT_GITHUB_POST_TOKEN` / `GITHUB_TOKEN` /
// `GH_TOKEN` plus `OPEN_CHAT_GITHUB_REPO`), so a stock deployment behaves
// exactly as before.

func init() {
	chats.RegisterAutoConsolidationRunner(runAutoConsolidation)
}

// autoConsolidationCooldown is the minimum interval between two consolidation
// passes of the same chat. A single interaction may finish several turns in a
// row (auto-continue); the cooldown keeps the loop from re-running the pass on
// every one of them while the previous run is still settling.
const autoConsolidationCooldown = 60 * time.Second

var (
	autoConsolidationMu   sync.Mutex
	autoConsolidationLast = map[uint]time.Time{}
)

// runAutoConsolidation runs one auto-consolidation pass for a completed
// automated turn. It is best-effort: every failure is logged and never affects
// message delivery. A chat whose config does not declare a native coder agent,
// or whose policy is switched off, is left untouched.
func runAutoConsolidation(db *gorm.DB, chat database.Chat) {
	if chat.ID == 0 {
		return
	}
	config := autoConsolidationChatConfig(db, chat)
	if !nativecoder.IsCoderAgentConfig(config) {
		return
	}
	policy := nativecoder.ResolvePolicyFromConfig(config)
	if !policy.Enabled {
		return
	}
	repo := autoConsolidationRepo(config)
	if repo == "" {
		return
	}
	if !autoConsolidationDue(chat.ID) {
		return
	}
	// Latch the cooldown before attempting the pass: an auto-continued
	// interaction finishes several turns in a row and must not re-run the pass
	// (or re-hit GitHub for a client that is not configured) on every one.
	autoConsolidationMarkRun(chat.ID)

	client, err := nativecoder.ConsolidationClientFactory(repo)
	if err != nil {
		log.Printf("auto consolidation: client for %s failed: %v", repo, err)
		return
	}
	if client == nil {
		// No GitHub token/repository configured: the feature is a no-op.
		return
	}

	runner := nativecoder.ConsolidationRunner{Client: client}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	result, runErr := runner.Run(ctx, policy, repo)
	if runErr != nil {
		log.Printf("auto consolidation: pass for %s failed: %v", repo, runErr)
		return
	}
	if !result.Ran {
		return
	}
	log.Printf(
		"auto consolidation: repo=%s pr=%d created=%v merged=%v: %s",
		repo, result.PullRequest, result.Created, result.Merged, result.Plan.Reason,
	)
}

// autoConsolidationRepoConfigKey is the optional shared-config key naming the
// `owner/name` repository an agent consolidates (`scope: repo`). It defaults to
// the deployment repository env (`OPEN_CHAT_GITHUB_REPO`) so a native coder
// deployment needs no extra per-agent key.
const autoConsolidationRepoConfigKey = "auto_consolidation_repo"

// autoConsolidationRepo resolves the repository to consolidate: the explicit
// shared-config key first, then the deployment environment. An empty value
// disables the pass (there is nothing to consolidate).
func autoConsolidationRepo(config map[string]interface{}) string {
	if value, ok := config[autoConsolidationRepoConfigKey].(string); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return strings.TrimSpace(nativecoder.GitHubRepoFromEnv())
}

// autoConsolidationChatConfig decodes a chat's effective shared config.
func autoConsolidationChatConfig(db *gorm.DB, chat database.Chat) map[string]interface{} {
	configData := []byte(nil)
	if chat.SharedConfig != nil && len(chat.SharedConfig.ConfigData) > 0 {
		configData = chat.SharedConfig.ConfigData
	} else if db != nil && chat.SharedConfigId != nil && *chat.SharedConfigId != 0 {
		var shared database.SharedChatConfig
		if err := db.First(&shared, "id = ?", *chat.SharedConfigId).Error; err != nil || len(shared.ConfigData) == 0 {
			return nil
		}
		configData = shared.ConfigData
	}
	if len(configData) == 0 {
		return nil
	}
	config := map[string]interface{}{}
	if err := json.Unmarshal(configData, &config); err != nil {
		return nil
	}
	return config
}

// autoConsolidationDue reports whether the chat's cooldown has elapsed.
func autoConsolidationDue(chatID uint) bool {
	autoConsolidationMu.Lock()
	defer autoConsolidationMu.Unlock()
	last, seen := autoConsolidationLast[chatID]
	return !seen || time.Since(last) >= autoConsolidationCooldown
}

// autoConsolidationMarkRun records the start of a pass for the cooldown.
func autoConsolidationMarkRun(chatID uint) {
	autoConsolidationMu.Lock()
	defer autoConsolidationMu.Unlock()
	autoConsolidationLast[chatID] = time.Now()
}

// ResetAutoConsolidationCooldownForTest clears the per-chat cooldown state so a
// test can run several passes in a row.
func ResetAutoConsolidationCooldownForTest() {
	autoConsolidationMu.Lock()
	defer autoConsolidationMu.Unlock()
	autoConsolidationLast = map[uint]time.Time{}
}
