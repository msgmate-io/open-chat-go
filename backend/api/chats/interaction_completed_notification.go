package chats

import (
	"context"
	"encoding/json"
	"log"
	"net/url"
	"strings"

	"backend/database"
	"backend/runtimecfg"

	"github.com/msgmate-io/go-integration-interface/integrationinterface"
	"gorm.io/gorm"
)

// Interaction notification slugs emitted by the core interaction lifecycle.
// The slugs are registered by the notifications integration; dispatch is a
// no-op when that integration is not loaded, so the core stays valid on every
// profile.
const (
	// notificationSlugInteractionCompleted is emitted for every completed
	// interaction (the automated agent's final reply is ready).
	notificationSlugInteractionCompleted = "interaction_completed"
	// notificationSlugInteractionConfirmed is emitted when the owner approves
	// a pending interaction confirmation so its deferred reply can start.
	notificationSlugInteractionConfirmed = "interaction_confirmed"
	// notificationSlugInteractionActionTaken is emitted when a confirmable
	// action inside an interaction has been executed.
	notificationSlugInteractionActionTaken = "interaction_action_taken"
)

// maybeDispatchInteractionCompletedNotification emits the
// `interaction_completed` notification after a final automated reply is
// persisted for an interaction chat. This is the core completion choke point:
// every chat backend (the core HAL agent, opencode and its derived backends,
// matrix/signal forwarding, ...) persists its final message through
// `POST /api/v1/chats/{chat_uuid}/messages/send`, so emitting here covers all
// interactions instead of only opencode's post-interaction hook.
//
// It is best-effort: notification failures are logged and never affect message
// delivery. Intermediate markers that still need the user (permission prompts,
// relay questions, confirmable actions, ...) are excluded because the
// interaction is not actually completed yet.
func maybeDispatchInteractionCompletedNotification(ctx context.Context, db *gorm.DB, chat database.Chat, sender, receiver database.User, message database.Message) {
	if db == nil {
		return
	}
	if !sender.IsAutomated || receiver.IsAutomated || receiver.ID == 0 {
		return
	}
	if !strings.HasPrefix(strings.TrimSpace(chat.ChatType), "interaction") {
		return
	}
	meta, ok := decodeMessageMetadata(message.MetaData)
	if !ok || !isInteractionCompletionMetadata(message, meta) {
		return
	}
	if len(pendingActionsForMessage(message)) > 0 {
		return
	}
	ownerUUID := strings.TrimSpace(receiver.UUID)
	if ownerUUID == "" {
		return
	}

	interactionURL := interactionCompletedURL(db, chat, receiver)
	tags := completedInteractionTags(chat)
	interactionType := interactionCompletedType(chat)
	body := buildInteractionCompletedBody(interactionURL, interactionType, tags)

	if err := dispatchInteractionNotification(ctx, db, notificationSlugInteractionCompleted, "Interaction completed", body, ownerUUID, interactionCompletedContext(db, chat, interactionURL, tags)); err != nil {
		log.Printf("interaction-completed notification dispatch failed for chat %s: %v", chat.UUID, err)
	}
}

// maybeDispatchInteractionConfirmedNotification emits the
// `interaction_confirmed` notification once the owner approves a pending
// interaction confirmation. The approve handler is the smallest correct hook:
// it is the single point where the interaction leaves the pending gate and its
// deferred reply is enqueued, and it carries the interaction chat + owner.
//
// Best-effort: notification failures are logged and never affect the approval.
func maybeDispatchInteractionConfirmedNotification(ctx context.Context, db *gorm.DB, chat database.Chat, owner database.User) {
	if db == nil {
		return
	}
	if !strings.HasPrefix(strings.TrimSpace(chat.ChatType), "interaction") {
		return
	}
	ownerUUID := strings.TrimSpace(owner.UUID)
	if ownerUUID == "" {
		return
	}

	interactionURL := interactionCompletedURL(db, chat, owner)
	tags := completedInteractionTags(chat)
	interactionType := interactionCompletedType(chat)
	body := buildInteractionConfirmedBody(interactionURL, interactionType, tags)

	if err := dispatchInteractionNotification(ctx, db, notificationSlugInteractionConfirmed, "Interaction confirmed", body, ownerUUID, interactionCompletedContext(db, chat, interactionURL, tags)); err != nil {
		log.Printf("interaction-confirmed notification dispatch failed for chat %s: %v", chat.UUID, err)
	}
}

// MaybeDispatchInteractionActionTakenNotification emits the
// `interaction_action_taken` notification once a confirmable action has been
// executed. The confirmable-action executor is the smallest correct hook: the
// user's decision to execute the action (and its success/failure) is recorded
// there, and the interaction chat + action are in scope.
//
// `status` is the terminal action status ("executed" or "failed"); actions that
// still wait for an SSH-key unlock stay pending and must not be announced.
// Best-effort: notification failures are logged and never affect execution.
func MaybeDispatchInteractionActionTakenNotification(ctx context.Context, db *gorm.DB, chat database.Chat, actor database.User, toolName, status string) {
	if db == nil {
		return
	}
	if !strings.HasPrefix(strings.TrimSpace(chat.ChatType), "interaction") {
		return
	}
	receiverUUID := strings.TrimSpace(actor.UUID)
	if receiverUUID == "" {
		return
	}

	interactionURL := interactionCompletedURL(db, chat, actor)
	tags := completedInteractionTags(chat)
	body := buildInteractionActionTakenBody(interactionURL, toolName, status)
	contextMap := interactionCompletedContext(db, chat, interactionURL, tags)
	if action := strings.TrimSpace(toolName); action != "" {
		contextMap["action"] = action
	}
	if result := strings.TrimSpace(status); result != "" {
		contextMap["action_status"] = result
	}

	if err := dispatchInteractionNotification(ctx, db, notificationSlugInteractionActionTaken, "Interaction action taken", body, receiverUUID, contextMap); err != nil {
		log.Printf("interaction-action-taken notification dispatch failed for chat %s: %v", chat.UUID, err)
	}
}

// dispatchInteractionNotification resolves the notifications integration and
// forwards one interaction notification. It is a no-op when the integration is
// not loaded, so the core remains valid on every integration profile.
func dispatchInteractionNotification(ctx context.Context, db *gorm.DB, slug, title, body, receiverUUID string, contextMap map[string]interface{}) error {
	if db == nil || strings.TrimSpace(receiverUUID) == "" {
		return nil
	}
	def, ok := integrationinterface.Get("notifications")
	if !ok {
		return nil
	}
	fn, ok := def.Functions["dispatch_notification"]
	if !ok || fn == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	_, err := fn(ctx, map[string]interface{}{
		"db":                 db,
		"slug":               slug,
		"receiver_user_uuid": receiverUUID,
		"title":              title,
		"body":               body,
		"source_integration": "core",
		"context":            contextMap,
	})
	return err
}

func decodeMessageMetadata(raw database.JSONRaw) (map[string]interface{}, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	meta := map[string]interface{}{}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, false
	}
	return meta, true
}

// isInteractionCompletionMetadata reports whether a persisted message is a
// successful completion of an interaction. It mirrors the terminal-state logic
// in resolveInteractionStatus: `finished` without `error` and without an
// in-flight event payload (e.g. provider_retry, which also carries
// `finished: true`).
func isInteractionCompletionMetadata(message database.Message, meta map[string]interface{}) bool {
	if meta == nil {
		return false
	}
	finished, _ := meta["finished"].(bool)
	if !finished {
		return false
	}
	if errFlag, _ := meta["error"].(bool); errFlag {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(message.DataType), "event") {
		return false
	}
	if eventType, _ := meta["event_type"].(string); strings.TrimSpace(eventType) != "" {
		return false
	}
	return true
}

// interactionCompletedURL builds the public interaction link, preferring the
// owner's share UUID (stable public link) and falling back to the raw chat
// UUID. It mirrors the git integration's triggerInteractionURL helper.
func interactionCompletedURL(db *gorm.DB, chat database.Chat, owner database.User) string {
	shareUUID := ""
	share := database.SharedChatInstance{}
	if err := db.Where("chat_id = ? AND owning_user_id = ?", chat.ID, owner.ID).First(&share).Error; err == nil {
		shareUUID = strings.TrimSpace(share.ChatShareUUID)
	}
	if shareUUID == "" {
		shareUUID = strings.TrimSpace(chat.UUID)
	}
	if shareUUID == "" {
		return ""
	}
	base := strings.TrimRight(strings.TrimSpace(runtimecfg.PublicBaseURL()), "/")
	if base == "" {
		return ""
	}
	return base + "/interaction/" + url.PathEscape(shareUUID)
}

// completedInteractionTags returns the chat's free-form tags, which carry the
// dispatch context (e.g. `repo:open-chat-go`, `via:assign`).
func completedInteractionTags(chat database.Chat) []string {
	tags := make([]string, 0, len(chat.Tags))
	for _, tag := range chat.Tags {
		if tag = strings.TrimSpace(tag); tag != "" {
			tags = append(tags, tag)
		}
	}
	return tags
}

// interactionCompletedType derives a short interaction type for the notification
// body. It prefers the dispatch event encoded in the `via:<event>` tag (set by
// the git trigger dispatch), then the chat backend name, then a generic label.
func interactionCompletedType(chat database.Chat) string {
	for _, tag := range chat.Tags {
		if strings.HasPrefix(tag, "via:") {
			if value := strings.TrimSpace(strings.TrimPrefix(tag, "via:")); value != "" {
				return value
			}
		}
	}
	if chat.SharedConfig != nil && len(chat.SharedConfig.ConfigData) > 0 {
		config := map[string]interface{}{}
		if err := json.Unmarshal(chat.SharedConfig.ConfigData, &config); err == nil {
			if backend, _ := config["chat_backend"].(string); strings.TrimSpace(backend) != "" {
				return strings.TrimSpace(backend)
			}
		}
	}
	return "interaction"
}

// buildInteractionCompletedBody renders the compact completion notification
// body: the direct interaction link plus the type and tags. It intentionally
// omits any "Interaction completed" preamble because the Signal renderer puts
// the title (monospace + bold) on its own first line, so the body starts
// directly with the link.
func buildInteractionCompletedBody(interactionURL, interactionType string, tags []string) string {
	return interactionLinkLine(interactionURL, interactionTypeDetails(interactionType, tags))
}

// buildInteractionConfirmedBody renders the confirmation notification body: the
// direct interaction link plus the type and tags, mirroring the completion body.
func buildInteractionConfirmedBody(interactionURL, interactionType string, tags []string) string {
	return interactionLinkLine(interactionURL, interactionTypeDetails(interactionType, tags))
}

// buildInteractionActionTakenBody renders the confirmable-action notification
// body: the direct interaction link plus the executed tool and result status.
func buildInteractionActionTakenBody(interactionURL, toolName, status string) string {
	details := "Action: " + strings.TrimSpace(toolName)
	if result := strings.TrimSpace(status); result != "" {
		details += " (" + result + ")"
	}
	return interactionLinkLine(interactionURL, details)
}

// interactionTypeDetails renders the shared "Type: <type>, Tags: <...>" detail
// suffix used by the interaction lifecycle bodies.
func interactionTypeDetails(interactionType string, tags []string) string {
	details := "Type: " + strings.TrimSpace(interactionType)
	if len(tags) > 0 {
		details += ", Tags: " + strings.Join(tags, ", ")
	}
	return details
}

// interactionLinkLine joins the direct interaction link and a short detail
// suffix on a single line (two spaces between them), e.g.
// "https://ci.msgmate.io/interaction/<uuid>  Type: assign, Tags: git".
func interactionLinkLine(interactionURL, details string) string {
	interactionURL = strings.TrimSpace(interactionURL)
	details = strings.TrimSpace(details)
	switch {
	case interactionURL == "":
		return details
	case details == "":
		return interactionURL
	default:
		return interactionURL + "  " + details
	}
}

// interactionCompletedContext adds the repo/PR context from the interaction's
// shared config (`post_action`/`post_actions`) when available, plus the
// interaction link and tags.
func interactionCompletedContext(db *gorm.DB, chat database.Chat, interactionURL string, tags []string) map[string]interface{} {
	contextMap := map[string]interface{}{}
	// The in-app notification opens the finished interaction chat directly;
	// the notifications integration derives a `chat` action from `chat_uuid`.
	if chatUUID := strings.TrimSpace(chat.UUID); chatUUID != "" {
		contextMap["chat_uuid"] = chatUUID
	}
	if interactionURL != "" {
		contextMap["interaction_url"] = interactionURL
	}
	if len(tags) > 0 {
		contextMap["tags"] = strings.Join(tags, ", ")
	}
	repo, number, subjectURL := interactionRepoContext(db, chat)
	if repo != "" {
		contextMap["repo"] = repo
	}
	if number != 0 {
		contextMap["number"] = number
	}
	if subjectURL != "" {
		contextMap["url"] = subjectURL
	}
	return contextMap
}

// interactionRepoContext extracts the triggering repository, number and URL from
// the interaction's shared config post-actions (the git integration stores them
// there via `withPlanAutoImplement`). Falls back to the `repo:<slug>` dispatch
// tag when no post-action is present.
func interactionRepoContext(db *gorm.DB, chat database.Chat) (string, int, string) {
	configData := []byte(nil)
	if chat.SharedConfig != nil && len(chat.SharedConfig.ConfigData) > 0 {
		configData = chat.SharedConfig.ConfigData
	} else if db != nil {
		shared := database.SharedChatConfig{}
		if err := db.Where("chat_id = ?", chat.ID).First(&shared).Error; err == nil {
			configData = shared.ConfigData
		}
	}
	if len(configData) > 0 {
		config := map[string]interface{}{}
		if err := json.Unmarshal(configData, &config); err == nil {
			entries := []map[string]interface{}{}
			if raw, ok := config["post_action"].(map[string]interface{}); ok {
				entries = append(entries, raw)
			}
			if rawList, ok := config["post_actions"].([]interface{}); ok {
				for _, item := range rawList {
					if m, ok := item.(map[string]interface{}); ok {
						entries = append(entries, m)
					}
				}
			}
			for _, entry := range entries {
				repo, _ := entry["repo"].(string)
				subjectURL, _ := entry["issue_url"].(string)
				number := 0
				switch typed := entry["number"].(type) {
				case float64:
					number = int(typed)
				case int:
					number = typed
				}
				if strings.TrimSpace(repo) != "" || strings.TrimSpace(subjectURL) != "" {
					return strings.TrimSpace(repo), number, strings.TrimSpace(subjectURL)
				}
			}
		}
	}
	for _, tag := range chat.Tags {
		if strings.HasPrefix(tag, "repo:") {
			if slug := strings.TrimSpace(strings.TrimPrefix(tag, "repo:")); slug != "" {
				return slug, 0, ""
			}
		}
	}
	return "", 0, ""
}
