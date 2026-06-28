package bots

import (
	"backend/database"
	"encoding/json"
	"net/http"
	"testing"

	"gorm.io/gorm"
)

// ensureCreateBotsPermission makes the create_bots permission explicit for a
// session user (every user already receives it in User.AfterCreate, so this is
// idempotent rather than a second insert), keeping Create reachable no matter
// how the test user was persisted.
func ensureCreateBotsPermission(t *testing.T, DB *gorm.DB, user *database.User) {
	t.Helper()
	permission := database.Permission{UserId: user.ID, Permission: database.PermissionCreateBots}
	if err := DB.Where("user_id = ? AND permission = ?", user.ID, database.PermissionCreateBots).
		FirstOrCreate(&permission).Error; err != nil {
		t.Fatalf("failed to ensure create_bots for %q: %v", user.Name, err)
	}
}

// saveConfigBody is a minimal, structurally valid default_shared_config.
func saveConfigBody(model string) map[string]interface{} {
	return map[string]interface{}{
		"model":         model,
		"backend":       "litellm",
		"endpoint":      "https://litellm.t1m.me/v1",
		"system_prompt": "You are a helpful assistant.",
		"max_tokens":    1024,
		"context":       10,
	}
}

// TestBotWritePathsResolveActingUserForAutomatedSession covers the ci#824
// follow-up: the canonical bot *write* routes (PUT /bots/{id}/config,
// PATCH /bots/{id}, DELETE /bots/{id}) must scope ownership by the acting user,
// not by the raw session principal. An automated session owns no runtime row of
// its own — its fleet belongs to the runtime owner — so scoping by the bot
// itself 404s on a bot that genuinely exists.
func TestBotWritePathsResolveActingUserForAutomatedSession(t *testing.T) {
	DB := setupBotsTestDB(t)
	owner := createUserForBotsTest(t, DB, "owner@example.com", false)

	// The session principal: an automated bot owned by `owner`.
	_, sessionBot := createBotRuntimeForTest(t, DB, owner, "session-bot")
	// The target of the write: a sibling bot in the same fleet.
	target, _ := createBotRuntimeForTest(t, DB, owner, "fleet-bot")

	handler := &BotsHandler{}

	t.Run("SaveConfig", func(t *testing.T) {
		rr := agentDispatchRequest(t, DB, &sessionBot, http.MethodPut, "/api/v1/bots/fleet-bot/config",
			map[string]string{"identifier": "fleet-bot"}, saveConfigBody("updated-model"), nil, nil, handler.SaveConfig)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
		}
		var runtime database.BotRuntimeConfig
		if err := DB.First(&runtime, target.ID).Error; err != nil {
			t.Fatalf("failed to reload bot: %v", err)
		}
		if got := decodeSharedConfig(runtime.DefaultSharedConfig)["model"]; got != "updated-model" {
			t.Fatalf("expected model updated-model, got %v", got)
		}
	})

	t.Run("Update", func(t *testing.T) {
		description := "patched by fleet owner"
		rr := agentDispatchRequest(t, DB, &sessionBot, http.MethodPatch, "/api/v1/bots/fleet-bot",
			map[string]string{"identifier": "fleet-bot"}, map[string]interface{}{"description": description}, nil, nil, handler.Update)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
		}
		var runtime database.BotRuntimeConfig
		if err := DB.First(&runtime, target.ID).Error; err != nil {
			t.Fatalf("failed to reload bot: %v", err)
		}
		if runtime.Description != description {
			t.Fatalf("expected description %q, got %q", description, runtime.Description)
		}
	})

	t.Run("Delete", func(t *testing.T) {
		rr := agentDispatchRequest(t, DB, &sessionBot, http.MethodDelete, "/api/v1/bots/fleet-bot",
			map[string]string{"identifier": "fleet-bot"}, nil, nil, nil, handler.Delete)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
		}
		var runtime database.BotRuntimeConfig
		if err := DB.First(&runtime, target.ID).Error; err != nil {
			t.Fatalf("failed to reload bot: %v", err)
		}
		if runtime.IsActive {
			t.Fatalf("expected bot to be deactivated after delete, got is_active=%v", runtime.IsActive)
		}
	})
}

// TestCreateBotFromAutomatedSessionActsForOwner pins the Create half of the
// follow-up: a bot session creates the new bot for its runtime owner, not for
// itself. The create_bots permission gate stays on the raw session principal.
func TestCreateBotFromAutomatedSessionActsForOwner(t *testing.T) {
	DB := setupBotsTestDB(t)
	owner := createUserForBotsTest(t, DB, "owner@example.com", false)

	_, sessionBot := createBotRuntimeForTest(t, DB, owner, "session-bot")
	ensureCreateBotsPermission(t, DB, &sessionBot)

	rr := agentDispatchRequest(t, DB, &sessionBot, http.MethodPost, "/api/v1/bots",
		nil, createBotRequestPayload("spawned-bot"), nil, nil, (&BotsHandler{}).Create)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var response CreateBotResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if response.Bot.OwnerUserUUID != owner.UUID {
		t.Fatalf("expected owner %s, got %s", owner.UUID, response.Bot.OwnerUserUUID)
	}

	var runtime database.BotRuntimeConfig
	if err := DB.Preload("BotUser").Preload("OwnerUser").Where("uuid = ?", response.Bot.UUID).First(&runtime).Error; err != nil {
		t.Fatalf("failed to reload created bot: %v", err)
	}
	if runtime.OwnerUserId != owner.ID {
		t.Fatalf("expected OwnerUserId %d (acting user), got %d", owner.ID, runtime.OwnerUserId)
	}
	if runtime.BotUserId == sessionBot.ID {
		t.Fatalf("created bot must not be owned by the session bot itself")
	}
}

// TestBotWritePathsKeepHumanSessionsOwnerScoped is the least-privilege
// regression guard: a human session that owns nothing in the fleet must still
// get a 404, on every write route.
func TestBotWritePathsKeepHumanSessionsOwnerScoped(t *testing.T) {
	DB := setupBotsTestDB(t)
	owner := createUserForBotsTest(t, DB, "owner@example.com", false)
	stranger := createUserForBotsTest(t, DB, "stranger@example.com", false)
	createBotRuntimeForTest(t, DB, owner, "fleet-bot")

	saveConfig := agentDispatchRequest(t, DB, stranger, http.MethodPut, "/api/v1/bots/fleet-bot/config",
		map[string]string{"identifier": "fleet-bot"}, saveConfigBody("hijacked-model"), nil, nil, (&BotsHandler{}).SaveConfig)
	if saveConfig.Code != http.StatusNotFound {
		t.Fatalf("SaveConfig: expected 404 for non-owner, got %d: %s", saveConfig.Code, saveConfig.Body.String())
	}

	update := agentDispatchRequest(t, DB, stranger, http.MethodPatch, "/api/v1/bots/fleet-bot",
		map[string]string{"identifier": "fleet-bot"}, map[string]interface{}{"description": "hijacked"}, nil, nil, (&BotsHandler{}).Update)
	if update.Code != http.StatusNotFound {
		t.Fatalf("Update: expected 404 for non-owner, got %d: %s", update.Code, update.Body.String())
	}

	deleteResp := agentDispatchRequest(t, DB, stranger, http.MethodDelete, "/api/v1/bots/fleet-bot",
		map[string]string{"identifier": "fleet-bot"}, nil, nil, nil, (&BotsHandler{}).Delete)
	if deleteResp.Code != http.StatusNotFound {
		t.Fatalf("Delete: expected 404 for non-owner, got %d: %s", deleteResp.Code, deleteResp.Body.String())
	}

	var runtime database.BotRuntimeConfig
	if err := DB.Preload("BotUser").Where("name = ?", "fleet-bot").First(&runtime).Error; err != nil {
		t.Fatalf("failed to reload bot: %v", err)
	}
	if !runtime.IsActive {
		t.Fatalf("non-owner delete must not deactivate the bot")
	}
	if got := decodeSharedConfig(runtime.DefaultSharedConfig)["model"]; got == "hijacked-model" {
		t.Fatalf("non-owner SaveConfig must not modify the config")
	}
}
