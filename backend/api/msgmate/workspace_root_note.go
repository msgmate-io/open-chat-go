package msgmate

import (
	"context"
	"encoding/json"
	"strings"

	"backend/agentdefaults"
	"backend/database"
	"backend/logging"
)

// Producer side of the ci#1120 dead workspace pin (ci#1168 item 1).
//
// The tool-error classifier below sees a provisioning failure whose message
// names the workspace root that was never materialised (`/workspace/coder-<uuid>`).
// The dispatch path cannot see that failure, so the observation has to be
// stamped onto the chat's stored `coder_select_workspace` selector for the next
// turn's ReconcileTaskRepo to consume; agentdefaults.NoteSandboxWorkspaceMissingRoot
// does the marking and ReconcileTaskRepo drops the dead pin.
//
// Everything here is best effort: a note that cannot be persisted must never
// mask the tool failure it describes, so every failure path logs and returns
// nil.

// NoteSandboxWorkspaceMissingRootHook builds the per-turn hook that records a
// missing workspace root on the chat's stored workspace selector. It reads the
// chat uuid off the bot-reply turn context and captures the process database
// handle, returning nil when there is no turn context to attribute the
// observation to.
func NoteSandboxWorkspaceMissingRootHook(ctx context.Context) func(path string) error {
	turn, ok := agentTurnFromContext(ctx)
	if !ok {
		return nil
	}
	chatUUID := strings.TrimSpace(turn.message.Content.ChatUUID)
	if chatUUID == "" {
		return nil
	}
	return func(path string) error {
		return persistSandboxWorkspaceMissingRoot(chatUUID, path)
	}
}

// noteSandboxWorkspaceMissingRoot records on the chat's stored workspace
// selector that the provisioned workspace root reported as missing by path does
// not exist, so the next turn re-selects a workspace instead of replaying a pin
// that cannot work. It is a no-op when the turn carries no note hook (no turn
// context, or a caller that owns its own persistence) and on any persistence
// failure: it must never mask the original tool failure.
func noteSandboxWorkspaceMissingRoot(ctx context.Context, path string) error {
	spec, ok := agentTurnSpecFromContext(ctx)
	if !ok || spec.NoteWorkspaceMissingRoot == nil {
		return nil
	}
	if err := spec.NoteWorkspaceMissingRoot(path); err != nil {
		logging.Warnf(
			"could not record missing sandbox workspace root %q on the stored selector (ci#1168): %v",
			strings.TrimSpace(path), err,
		)
	}
	return nil
}

// persistSandboxWorkspaceMissingRoot marks the workspace selector stored in the
// chat's shared config and writes it back. A chat without a shared config, one
// whose selector names no provisioned workspace, and a selector that already
// carries the marker are all left untouched.
func persistSandboxWorkspaceMissingRoot(chatUUID, path string) error {
	db := database.GetGlobalDB()
	if db == nil {
		return nil
	}
	var chat database.Chat
	if err := db.Preload("SharedConfig").Where("uuid = ?", chatUUID).First(&chat).Error; err != nil {
		return nil
	}
	if chat.SharedConfig == nil || len(chat.SharedConfig.ConfigData) == 0 {
		return nil
	}
	config := map[string]interface{}{}
	if err := json.Unmarshal(chat.SharedConfig.ConfigData, &config); err != nil {
		return nil
	}
	toolInit, _ := config[agentdefaults.ToolInitConfigKey].(map[string]interface{})
	if toolInit == nil {
		return nil
	}
	selector, _ := toolInit[agentdefaults.WorkspaceSelectorTool].(map[string]interface{})
	if len(selector) == 0 {
		return nil
	}
	marked := agentdefaults.NoteSandboxWorkspaceMissingRoot(selector, path)
	// The helper returns its input unchanged when the selector names no
	// provisioned workspace; it adds exactly one key otherwise.
	if len(marked) == len(selector) {
		return nil
	}
	toolInit[agentdefaults.WorkspaceSelectorTool] = marked
	config[agentdefaults.ToolInitConfigKey] = toolInit
	encoded, err := json.Marshal(config)
	if err != nil {
		return nil
	}
	if err := db.Model(&database.SharedChatConfig{}).
		Where("id = ?", chat.SharedConfig.ID).
		Update("config_data", database.JSONRaw(encoded)).Error; err != nil {
		return err
	}
	logging.Warnf(
		"recorded missing sandbox workspace root %q on the stored workspace selector of chat %s (ci#1168)",
		strings.TrimSpace(path), chatUUID,
	)
	return nil
}
