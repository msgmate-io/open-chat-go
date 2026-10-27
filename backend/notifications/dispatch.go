package notifications

import (
	"context"
	"strings"

	"backend/database"
	"backend/logging"
	"backend/runtimecfg"

	"github.com/msgmate-io/go-integration-interface/integrationinterface"
	"gorm.io/gorm"
)

// Core-owned notification slugs. They are registered by the notifications
// integration (see its catalog); dispatch is a no-op when that integration is
// not loaded, so the core stays valid on every integration profile.
const (
	// SlugProviderCreditsExhausted is emitted when an AI provider rejects a
	// turn because the account is out of credits/tokens (e.g. an OpenRouter
	// HTTP 402 "requires more credits").
	SlugProviderCreditsExhausted = "provider_credits_exhausted"
	// SlugInteractionError is emitted when an interaction turn ends on a
	// generation error.
	SlugInteractionError = "interaction_error"
)

// creditsExhaustedMarkers are the provider-side phrasings that mean "top up
// the account". They are matched case-insensitively against the error text so
// the core can distinguish a credits problem from a generic provider fault
// without depending on any provider SDK. OpenRouter's 402 body reads e.g.
// "This request requires more credits, or fewer max_tokens. ... but can only
// afford 3092."
var creditsExhaustedMarkers = []string{
	"more credits",
	"can only afford",
	"out of credits",
	"insufficient credits",
	"insufficient_quota",
	"insufficient quota",
	"exceeded your current quota",
	"credit balance",
	"requires more credits",
	"payment required",
}

// Dispatch forwards one notification to the notifications integration, which
// fans it out to every enabled delivery type (email, Signal, ...). It is a
// no-op when the integration is not loaded.
func Dispatch(ctx context.Context, db *gorm.DB, slug, title, body, receiverUUID string, contextMap map[string]interface{}) error {
	if db == nil || strings.TrimSpace(receiverUUID) == "" || strings.TrimSpace(slug) == "" {
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
	if contextMap == nil {
		contextMap = map[string]interface{}{}
	}
	_, err := fn(ctx, map[string]interface{}{
		"db":                 db,
		"slug":               slug,
		"receiver_user_uuid": strings.TrimSpace(receiverUUID),
		"title":              title,
		"body":               body,
		"source_integration": "core",
		"context":            contextMap,
	})
	return err
}

// matchesCreditsExhausted reports whether a failure detail is the provider
// refusing the request because the account is out of credits/tokens.
func matchesCreditsExhausted(detail string) bool {
	lower := strings.ToLower(detail)
	for _, marker := range creditsExhaustedMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// MaybeDispatchInteractionFailure emits an `interaction_error` notification —
// or `provider_credits_exhausted` when the detail looks like the provider
// rejected the turn for lack of credits — to the interaction's human owner.
// It is best-effort: notification failures are logged and never affect the
// interaction.
//
// The owner is the human participant of the interaction; a pure bot-to-bot
// interaction (no human participant) is skipped.
func MaybeDispatchInteractionFailure(ctx context.Context, db *gorm.DB, chatUUID, detail string) {
	if db == nil || strings.TrimSpace(chatUUID) == "" {
		return
	}
	var chat database.Chat
	if err := db.Preload("User1").Preload("User2").
		Where("uuid = ?", chatUUID).First(&chat).Error; err != nil {
		return
	}
	if !strings.HasPrefix(strings.TrimSpace(chat.ChatType), "interaction") {
		return
	}
	owner, ok := interactionHumanOwner(chat)
	if !ok {
		return
	}

	detail = truncateDetail(detail)
	creditsExhausted := matchesCreditsExhausted(detail)

	slug := SlugInteractionError
	title := "Interaction error"
	if creditsExhausted {
		slug = SlugProviderCreditsExhausted
		title = "AI provider credits exhausted"
	}
	body := buildFailureBody(chat, slug, detail)

	contextMap := map[string]interface{}{
		"chat_uuid": chat.UUID,
	}
	if detail != "" {
		contextMap["error_detail"] = detail
	}
	if creditsExhausted {
		contextMap["remedy"] = "Top up the AI provider account, then retry."
	}

	if err := Dispatch(ctx, db, slug, title, body, owner.UUID, contextMap); err != nil {
		logging.Errorf("interaction-failure notification dispatch failed for chat %s: %v", chat.UUID, err)
	}
}

// interactionHumanOwner returns the non-automated participant of a chat, which
// is the human the failure notification is addressed to.
func interactionHumanOwner(chat database.Chat) (database.User, bool) {
	if !chat.User1.IsAutomated && chat.User1.UUID != "" {
		return chat.User1, true
	}
	if !chat.User2.IsAutomated && chat.User2.UUID != "" {
		return chat.User2, true
	}
	return database.User{}, false
}

// buildFailureBody renders the notification body: the interaction link (when
// the public base URL is configured) plus the underlying error detail and, for
// a credits problem, the remedy.
func buildFailureBody(chat database.Chat, slug, detail string) string {
	lines := []string{}
	if link := interactionURL(chat); link != "" {
		lines = append(lines, link)
	}
	if slug == SlugProviderCreditsExhausted {
		lines = append(lines, "The AI provider rejected the turn because the account is out of credits/tokens.")
	} else {
		lines = append(lines, "The interaction turn ended on an error.")
	}
	if detail != "" {
		lines = append(lines, "Error: "+detail)
	}
	return strings.Join(lines, "\n")
}

// interactionURL builds the public interaction link for the chat, or "" when no
// public base URL is configured.
func interactionURL(chat database.Chat) string {
	base := strings.TrimRight(strings.TrimSpace(runtimecfg.PublicBaseURL()), "/")
	uuid := strings.TrimSpace(chat.UUID)
	if base == "" || uuid == "" {
		return ""
	}
	return base + "/interaction/" + uuid
}

// truncateDetail trims a raw error string for safe inclusion in a notification.
func truncateDetail(detail string) string {
	detail = strings.TrimSpace(detail)
	if len(detail) > 300 {
		detail = detail[:300] + "…"
	}
	return detail
}
