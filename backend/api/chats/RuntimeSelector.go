package chats

import (
	"backend/database"
	"backend/logging"
	"backend/server/util"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"gorm.io/gorm"
)

// runtimeSelectorsMetaKey is the message meta key that carries the runtime
// selection widget state. It holds either an array of selector objects or a
// single selector object (tolerated for older emitters).
const runtimeSelectorsMetaKey = "runtime_selectors"

// Selector meta keys that carry the enriched trigger envelope through the
// selector -> worker interaction hand-off (ci#1131).
const (
	// runtimeSelectorTaskMessageKey is the full, enriched trigger text that
	// must reach the spawned worker interaction verbatim (for a GitHub mention:
	// the "GitHub mention detected on <repo>#<n> / Title: / URL: /
	// Trigger reason: / Comment by @<author>: <body>" envelope). It is optional
	// so selectors emitted before ci#1131 keep working.
	runtimeSelectorTaskMessageKey = "task_message"

	// runtimeSelectorTriggerContextKey is the optional structured companion of
	// task_message ({kind, repo, number, title, url, trigger_reason,
	// comment_author, comment_body}) so tests and tools can assert fields
	// instead of substring-matching prose.
	runtimeSelectorTriggerContextKey = "trigger_context"

	// runtimeSelectorForwardedMessageKey records the text the widget actually
	// posted as the new interaction's first message.
	runtimeSelectorForwardedMessageKey = "forwarded_message"

	// runtimeSelectorContextForwardedKey records whether that text still carried
	// the selector's trigger envelope. Legacy selectors without a task_message
	// resolve with false (nothing was forwarded to validate).
	runtimeSelectorContextForwardedKey = "context_forwarded"
)

// Selector meta keys that back the reset affordance (ci#1135).
const (
	// runtimeSelectorAllowResetKey is the optional per-selector knob the
	// proposing tool (git_propose_interaction_runtime) sets to false to forbid
	// resetting the selector. Absent means allowed, so selectors persisted
	// before ci#1135 keep the reset affordance.
	runtimeSelectorAllowResetKey = "allow_reset"

	// runtimeSelectorResetHistoryKey archives the runs a reset abandoned so the
	// interaction identifiers of an already started run are not lost.
	runtimeSelectorResetHistoryKey = "reset_history"

	// runtimeSelectorResetCountKey counts how often a selector was reset. It is
	// observability only: resets are not capped.
	runtimeSelectorResetCountKey = "reset_count"
)

// RuntimeSelectorRequireForwardedMessageEnvKey gates the fail-closed
// validation added in ci#1131. It is OFF by default so the core-side rule and
// the trigger-owner emitter (the git integration, which must actually emit
// task_message) can land independently: with the flag off the resolve handler
// warns and records context_forwarded:false instead of rejecting.
const RuntimeSelectorRequireForwardedMessageEnvKey = "OPEN_CHAT_RUNTIME_SELECTOR_REQUIRE_FORWARDED_MESSAGE"

// RuntimeSelectorForwardedMessageRequired reports whether a selector carrying a
// task_message refuses to be confirmed without a forwarded_message that still
// contains that envelope.
func RuntimeSelectorForwardedMessageRequired() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(RuntimeSelectorRequireForwardedMessageEnvKey))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// Runtime selector status values persisted back into the message meta.
const (
	RuntimeSelectorPending   = "pending"
	RuntimeSelectorConfirmed = "confirmed"
	RuntimeSelectorCancelled = "cancelled"
)

// resolveRuntimeSelectorRequest is the body accepted by the resolve endpoint.
// The widget creates the interaction client-side and then records the decision
// (with the resulting interaction identifiers) so a reload shows the resolved
// state instead of the pending widget.
//
// Because the interaction is created client-side, the endpoint supports a
// two-phase "started" flow: the widget first claims the selector (decision
// "started" without a started chat uuid, carrying a client-generated claim_id),
// creates the interaction only if the claim succeeded, and then records the
// resulting interaction identifiers with the same claim_id. This makes a second
// widget instance (or a second browser tab) unable to start the same selector
// twice: its claim is rejected with 409 before it can create anything.
type resolveRuntimeSelectorRequest struct {
	SelectorID            string `json:"selector_id"`
	Decision              string `json:"decision"`
	ClaimID               string `json:"claim_id,omitempty"`
	StartedChatUUID       string `json:"started_chat_uuid,omitempty"`
	StartedInteractionURL string `json:"started_interaction_url,omitempty"`
	// ForwardedMessage is the text the widget actually posted as the new
	// interaction's first message (ci#1131). It is optional so widgets that
	// predate the field keep working; when the selector carries a
	// task_message it is what core validates against (fail closed once
	// OPEN_CHAT_RUNTIME_SELECTOR_REQUIRE_FORWARDED_MESSAGE is on).
	ForwardedMessage string `json:"forwarded_message,omitempty"`
}

// ResolveRuntimeSelectorResponse is returned by the resolve handler.
type ResolveRuntimeSelectorResponse struct {
	Success  bool                   `json:"success"`
	Status   string                 `json:"status"`
	Selector map[string]interface{} `json:"selector"`
}

// parseRuntimeSelectorsFromMeta extracts the selector objects from a message
// meta map. Both the array form and the single-object form are accepted.
func parseRuntimeSelectorsFromMeta(meta map[string]interface{}) []map[string]interface{} {
	if meta == nil {
		return nil
	}
	raw, ok := meta[runtimeSelectorsMetaKey]
	if !ok {
		return nil
	}
	selectors := []map[string]interface{}{}
	switch typed := raw.(type) {
	case []interface{}:
		for _, entry := range typed {
			if selector, ok := entry.(map[string]interface{}); ok {
				normalizeRuntimeSelectorTriggerFields(selector)
				selectors = append(selectors, selector)
			}
		}
	case map[string]interface{}:
		normalizeRuntimeSelectorTriggerFields(typed)
		selectors = append(selectors, typed)
	}
	return selectors
}

// normalizeRuntimeSelectorTriggerFields normalises the ci#1131 trigger
// envelope fields carried on a selector. It never invents keys: a selector
// emitted before ci#1131 keeps exactly the keys it shipped with (so legacy
// payloads stay byte-for-byte permissive), while a present task_message is
// trimmed and a trigger_context is guaranteed to be a decoded object (a JSON
// string is decoded, an unusable value is dropped).
func normalizeRuntimeSelectorTriggerFields(selector map[string]interface{}) {
	if selector == nil {
		return
	}
	if raw, ok := selector[runtimeSelectorTaskMessageKey]; ok {
		taskMessage, _ := raw.(string)
		if normalized := strings.TrimSpace(taskMessage); normalized != "" {
			selector[runtimeSelectorTaskMessageKey] = normalized
		} else {
			delete(selector, runtimeSelectorTaskMessageKey)
		}
	}
	raw, ok := selector[runtimeSelectorTriggerContextKey]
	if !ok {
		return
	}
	switch typed := raw.(type) {
	case map[string]interface{}:
		if len(typed) == 0 {
			delete(selector, runtimeSelectorTriggerContextKey)
		}
	case string:
		decoded := map[string]interface{}{}
		if err := json.Unmarshal([]byte(typed), &decoded); err == nil && len(decoded) > 0 {
			selector[runtimeSelectorTriggerContextKey] = decoded
			return
		}
		delete(selector, runtimeSelectorTriggerContextKey)
	default:
		delete(selector, runtimeSelectorTriggerContextKey)
	}
}

// runtimeSelectorTaskMessage returns the trimmed trigger envelope carried by a
// selector, or "" when the selector predates ci#1131.
func runtimeSelectorTaskMessage(selector map[string]interface{}) string {
	return actionTaskStringField(selector, runtimeSelectorTaskMessageKey)
}

// runtimeSelectorTriggerContext returns the structured companion of
// task_message, or nil when the selector carries none.
func runtimeSelectorTriggerContext(selector map[string]interface{}) map[string]interface{} {
	if selector == nil {
		return nil
	}
	context, _ := selector[runtimeSelectorTriggerContextKey].(map[string]interface{})
	return context
}

// findRuntimeSelector picks the selector a decision targets. An explicit id
// wins; without one (eg a selector persisted before ids existed) the first
// still-pending entry is used.
func findRuntimeSelector(selectors []map[string]interface{}, selectorID string) map[string]interface{} {
	if selectorID != "" {
		for _, selector := range selectors {
			if actionTaskStringField(selector, "id") == selectorID {
				return selector
			}
		}
	}
	for _, selector := range selectors {
		status, _ := selector["status"].(string)
		if status == "" || status == RuntimeSelectorPending {
			return selector
		}
	}
	return nil
}

// persistRuntimeSelectorMeta writes the mutated selector list back into the
// message meta while preserving every unrelated meta key.
func persistRuntimeSelectorMeta(DB *gorm.DB, message database.Message, meta map[string]interface{}, selectors []map[string]interface{}) error {
	encodedSelectors := make([]interface{}, 0, len(selectors))
	for _, selector := range selectors {
		encodedSelectors = append(encodedSelectors, selector)
	}
	meta[runtimeSelectorsMetaKey] = encodedSelectors
	encoded, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return DB.Model(&database.Message{}).Where("id = ?", message.ID).Update("meta_data", database.JSONRaw(encoded)).Error
}

// runtimeSelectorForwardingState classifies whether the text the widget posted
// as the new interaction's first message still carries the selector's trigger
// envelope (ci#1131).
const (
	// runtimeSelectorForwardingNotApplicable means the selector carries no
	// task_message (a payload emitted before ci#1131); the legacy permissive
	// behaviour applies and context_forwarded is recorded as false.
	runtimeSelectorForwardingNotApplicable = "not_applicable"
	// runtimeSelectorForwardingEnriched means the forwarded text still carries
	// the selector's trigger envelope.
	runtimeSelectorForwardingEnriched = "enriched"
	// runtimeSelectorForwardingMissing means no forwarded_message was submitted
	// at all, so nothing proves the envelope reached the worker interaction.
	runtimeSelectorForwardingMissing = "missing"
	// runtimeSelectorForwardingLossy means a forwarded_message was submitted but
	// it dropped the envelope (typically the bare comment body, which is the
	// exact regression reported in ci#1131).
	runtimeSelectorForwardingLossy = "lossy"
)

// runtimeSelectorEnvelopePrefixMaxLen bounds the expected_prefix hint returned
// with a rejection so a long envelope does not echo into the error payload.
const runtimeSelectorEnvelopePrefixMaxLen = 160

// evaluateRuntimeSelectorForwarding classifies a submitted forwarded_message
// against the selector's task_message.
func evaluateRuntimeSelectorForwarding(selector map[string]interface{}, forwardedMessage string) string {
	if runtimeSelectorTaskMessage(selector) == "" {
		return runtimeSelectorForwardingNotApplicable
	}
	forwarded := strings.TrimSpace(forwardedMessage)
	if forwarded == "" {
		return runtimeSelectorForwardingMissing
	}
	if runtimeSelectorForwardingCarriesEnvelope(selector, forwarded) {
		return runtimeSelectorForwardingEnriched
	}
	return runtimeSelectorForwardingLossy
}

// runtimeSelectorForwardingCarriesEnvelope reports whether the forwarded text
// still contains the selector's trigger envelope. The envelope is compared
// whitespace-insensitively because a widget may re-wrap it; when it is not
// contained verbatim the structured trigger_context is used as a fallback so a
// rendered-but-whole envelope is still accepted.
func runtimeSelectorForwardingCarriesEnvelope(selector map[string]interface{}, forwardedMessage string) bool {
	taskMessage := runtimeSelectorTaskMessage(selector)
	if taskMessage == "" {
		return false
	}
	forwarded := collapseSelectorWhitespace(forwardedMessage)
	if forwarded == "" {
		return false
	}
	// The reported defect: the worker received byte-for-byte the comment body
	// instead of the envelope. Treat that (and any whitespace variant of it) as
	// lossy even if the body happens to contain a structured token.
	if commentBody := actionTaskStringField(runtimeSelectorTriggerContext(selector), "comment_body"); commentBody != "" {
		if collapseSelectorWhitespace(commentBody) == forwarded {
			return false
		}
	}
	if strings.Contains(forwarded, collapseSelectorWhitespace(taskMessage)) {
		return true
	}
	// Fallback: every structured token the trigger_context carries must be
	// present (url, repo#number, repo), and at least two of them must exist so a
	// lone token cannot vouch for the whole envelope.
	triggerContext := runtimeSelectorTriggerContext(selector)
	if len(triggerContext) == 0 {
		return false
	}
	required := []string{}
	if url := actionTaskStringField(triggerContext, "url"); url != "" {
		required = append(required, url)
	}
	repo := actionTaskStringField(triggerContext, "repo")
	number := actionTaskStringField(triggerContext, "number")
	if repo != "" && number != "" {
		required = append(required, repo+"#"+number)
	}
	if repo != "" {
		required = append(required, repo)
	}
	if len(required) < 2 {
		return false
	}
	for _, token := range required {
		if !strings.Contains(forwarded, collapseSelectorWhitespace(token)) {
			return false
		}
	}
	return true
}

// collapseSelectorWhitespace lowercases-free whitespace normalisation used to
// compare envelope/body text across formatting differences.
func collapseSelectorWhitespace(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

// runtimeSelectorEnvelopePrefix returns the first non-empty line of the trigger
// envelope, used as the expected_prefix hint of a rejection so a caller can see
// what must be forwarded.
func runtimeSelectorEnvelopePrefix(taskMessage string) string {
	for _, line := range strings.Split(taskMessage, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if len(trimmed) > runtimeSelectorEnvelopePrefixMaxLen {
			return trimmed[:runtimeSelectorEnvelopePrefixMaxLen]
		}
		return trimmed
	}
	return ""
}

// recordRuntimeSelectorForwarding stores the forwarded text and the
// context_forwarded flag on the selector. A call that omits forwarded_message
// keeps whatever an earlier phase of the same flow already recorded.
func recordRuntimeSelectorForwarding(selector map[string]interface{}, forwardedMessage string) {
	forwarded := strings.TrimSpace(forwardedMessage)
	if forwarded == "" {
		forwarded, _ = selector[runtimeSelectorForwardedMessageKey].(string)
		forwarded = strings.TrimSpace(forwarded)
	}
	if forwarded != "" {
		selector[runtimeSelectorForwardedMessageKey] = forwarded
	}
	selector[runtimeSelectorContextForwardedKey] = runtimeSelectorForwardingCarriesEnvelope(selector, forwarded)
}

// writeRuntimeSelectorForwardingRejected emits the fail-closed response for a
// hand-off that dropped the trigger envelope. The selector is left untouched
// (still pending) so nothing was confirmed and no orphan interaction survives.
func writeRuntimeSelectorForwardingRejected(w http.ResponseWriter, selector map[string]interface{}, state string) {
	status := http.StatusBadRequest
	reason := "forwarded_message missing: the confirmed worker turn must carry the full trigger envelope"
	if state == runtimeSelectorForwardingLossy {
		status = http.StatusConflict
		reason = "forwarded_message does not carry the selector's trigger envelope: refusing to confirm a lossy hand-off"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":         false,
		"reason":          state,
		"error":           reason,
		"expected_prefix": runtimeSelectorEnvelopePrefix(runtimeSelectorTaskMessage(selector)),
	})
}

// ResolveRuntimeSelector records the user's runtime choice on the message that
// carries the selector, so the pending action clears and a reload renders the
// resolved state.
//
//	@Summary      Resolve runtime selector
//	@Description  Record the user's decision (started or cancelled) for a runtime selector carried by a message.
//	@Tags         messages
//	@Accept       json
//	@Produce      json
//	@Security     SessionAuth
//	@Param        chat_uuid path string true "Chat UUID"
//	@Param        message_uuid path string true "Selector message UUID"
//	@Success      200 {object} chats.ResolveRuntimeSelectorResponse
//	@Failure      400 {string} string "Invalid request"
//	@Failure      403 {string} string "Forbidden"
//	@Failure      404 {string} string "Not found"
//	@Failure      409 {string} string "Conflict"
//	@Router       /api/v1/chats/{chat_uuid}/messages/{message_uuid}/runtime-selector/resolve [post]
func (h *ChatsHandler) ResolveRuntimeSelector(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	chatUUID, chatUUIDErr := parseChatUUID(r)
	messageUUID := strings.TrimSpace(r.PathValue("message_uuid"))
	if chatUUIDErr != nil || messageUUID == "" {
		http.Error(w, "Invalid chat/message UUID", http.StatusBadRequest)
		return
	}

	chat, err := findAccessibleChat(DB, user, chatUUID)
	if err != nil {
		http.Error(w, "Chat not found", http.StatusNotFound)
		return
	}
	if !enforceBrowserTokenInteractionChat(w, r, chat.ChatType) {
		return
	}

	var message database.Message
	if err := DB.Where("uuid = ? AND chat_id = ?", messageUUID, chat.ID).First(&message).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			http.Error(w, "Message not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Failed to load message", http.StatusInternalServerError)
		return
	}

	meta := map[string]interface{}{}
	if len(message.MetaData) > 0 {
		if err := json.Unmarshal(message.MetaData, &meta); err != nil {
			http.Error(w, "Message is not a runtime selector", http.StatusConflict)
			return
		}
	}
	selectors := parseRuntimeSelectorsFromMeta(meta)
	if len(selectors) == 0 {
		http.Error(w, "Message has no runtime selector", http.StatusConflict)
		return
	}

	var req resolveRuntimeSelectorRequest
	if err := decodeOptionalJSONBody(r, &req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	req.SelectorID = strings.TrimSpace(req.SelectorID)
	decision := strings.ToLower(strings.TrimSpace(req.Decision))
	if decision != "started" && decision != "cancelled" && decision != "reset" {
		http.Error(w, "decision must be started, cancelled or reset", http.StatusBadRequest)
		return
	}

	selector := findRuntimeSelector(selectors, req.SelectorID)
	if selector == nil {
		http.Error(w, "Runtime selector not found", http.StatusNotFound)
		return
	}

	claimID := strings.TrimSpace(req.ClaimID)
	statusValue, _ := selector["status"].(string)
	pending := statusValue == "" || statusValue == RuntimeSelectorPending
	existingClaim, _ := selector["claim_id"].(string)
	startedChatUUID := strings.TrimSpace(req.StartedChatUUID)
	startedURL := strings.TrimSpace(req.StartedInteractionURL)
	forwardedMessage := strings.TrimSpace(req.ForwardedMessage)

	if startedChatUUID != "" && !isResolvableStartedInteraction(DB, user, startedChatUUID) {
		http.Error(w, "Started interaction chat not found", http.StatusConflict)
		return
	}

	// ci#1131: the trigger envelope (task_message) must survive the selector ->
	// worker interaction hand-off. The widget posts the enriched task text to
	// the new interaction and reports it back as forwarded_message; when the
	// selector carries a task_message but the forwarded text is missing or has
	// dropped the envelope we fail closed: the selector stays pending and no
	// interaction is confirmed, so the turn is retried with the full context
	// instead of silently running the worker on the bare comment body. The
	// gate is off by default so core and the trigger owner can land
	// independently; with it off the loss is only warned about and recorded.
	if decision == "started" {
		forwardingState := evaluateRuntimeSelectorForwarding(selector, forwardedMessage)
		if forwardingState == runtimeSelectorForwardingMissing || forwardingState == runtimeSelectorForwardingLossy {
			if RuntimeSelectorForwardedMessageRequired() {
				logging.Warnf("runtime selector %s: refusing %s hand-off (chat=%s message=%s)",
					req.SelectorID, forwardingState, chat.UUID, message.UUID)
				writeRuntimeSelectorForwardingRejected(w, selector, forwardingState)
				return
			}
			logging.Warnf("runtime selector %s: %s trigger envelope on hand-off, allowed because %s is off (chat=%s message=%s)",
				req.SelectorID, forwardingState, RuntimeSelectorRequireForwardedMessageEnvKey, chat.UUID, message.UUID)
		}
	}

	if decision == "reset" {
		resolveRuntimeSelectorReset(w, DB, user, message, meta, selectors, selector, statusValue)
		return
	}

	if decision == "cancelled" {
		if !pending {
			http.Error(w, "Runtime selector has already been resolved", http.StatusConflict)
			return
		}
		selector["status"] = RuntimeSelectorCancelled
		selector["decided_by"] = user.UUID
		selector["decided_at"] = time.Now().UTC().Format(time.RFC3339)
		if err := persistRuntimeSelectorMeta(DB, message, meta, selectors); err != nil {
			http.Error(w, "Failed to update runtime selector", http.StatusInternalServerError)
			return
		}
		writeRuntimeSelectorResolved(w, selector)
		return
	}

	// decision == "started"
	if pending {
		// First phase: claim the selector. Callers that already created the
		// interaction may record its identifiers in the same call.
		selector["status"] = RuntimeSelectorConfirmed
		selector["decided_by"] = user.UUID
		selector["decided_at"] = time.Now().UTC().Format(time.RFC3339)
		if claimID != "" {
			selector["claim_id"] = claimID
		}
		if startedChatUUID != "" {
			selector["started_chat_uuid"] = startedChatUUID
		}
		if startedURL != "" {
			selector["started_interaction_url"] = startedURL
		}
		recordRuntimeSelectorForwarding(selector, forwardedMessage)
		if err := persistRuntimeSelectorMeta(DB, message, meta, selectors); err != nil {
			http.Error(w, "Failed to update runtime selector", http.StatusInternalServerError)
			return
		}
		writeRuntimeSelectorResolved(w, selector)
		return
	}

	// Already resolved. Let the claim owner complete the second phase (record
	// the started interaction) or retry a claim that did not create anything
	// yet. Any other claim (a second widget instance or browser tab) is
	// rejected with 409 before it can create a duplicate interaction.
	recorded, _ := selector["started_chat_uuid"].(string)
	if statusValue == RuntimeSelectorConfirmed &&
		claimID != "" &&
		claimID == strings.TrimSpace(existingClaim) &&
		strings.TrimSpace(recorded) == "" {
		if startedChatUUID != "" {
			selector["started_chat_uuid"] = startedChatUUID
		}
		if startedURL != "" {
			selector["started_interaction_url"] = startedURL
		}
		recordRuntimeSelectorForwarding(selector, forwardedMessage)
		if err := persistRuntimeSelectorMeta(DB, message, meta, selectors); err != nil {
			http.Error(w, "Failed to update runtime selector", http.StatusInternalServerError)
			return
		}
		writeRuntimeSelectorResolved(w, selector)
		return
	}

	http.Error(w, "Runtime selector has already been resolved", http.StatusConflict)
}

// isResolvableStartedInteraction reports whether a client-supplied started chat
// uuid names a chat the user may access and that is an interaction.
func isResolvableStartedInteraction(DB *gorm.DB, user *database.User, chatUUID string) bool {
	startedChat, err := findAccessibleChat(DB, user, chatUUID)
	if err != nil {
		return false
	}
	return isInteractionChatType(startedChat.ChatType)
}

// runtimeSelectorAllowsReset reports whether a selector may be reset. The
// proposing tool sets allow_reset:false to forbid it; a missing key means
// allowed so every selector persisted before ci#1135 keeps the affordance.
func runtimeSelectorAllowsReset(selector map[string]interface{}) bool {
	if selector == nil {
		return true
	}
	switch typed := selector[runtimeSelectorAllowResetKey].(type) {
	case bool:
		return typed
	case string:
		// Toleration for emitters that send the flag as a string.
		return !strings.EqualFold(strings.TrimSpace(typed), "false")
	default:
		return true
	}
}

// runtimeSelectorResetCount returns how often a selector was already reset.
func runtimeSelectorResetCount(selector map[string]interface{}) int {
	return actionTaskIntField(selector, runtimeSelectorResetCountKey)
}

// resolveRuntimeSelectorReset moves a confirmed selector back to pending so the
// user can pick a runtime again (ci#1135). The previous run is archived instead
// of dropped, and clearing claim_id invalidates any tab still holding the
// pre-reset claim: its late second phase can no longer record an interaction.
func resolveRuntimeSelectorReset(w http.ResponseWriter, DB *gorm.DB, user *database.User, message database.Message, meta map[string]interface{}, selectors []map[string]interface{}, selector map[string]interface{}, statusValue string) {
	// Only a confirmed selector has a run to abandon. Pending selectors have
	// nothing to reset (the widget should just cancel) and cancelled stays
	// terminal, so both are conflicts instead of a silent no-op.
	if statusValue != RuntimeSelectorConfirmed {
		http.Error(w, "Runtime selector is not confirmed", http.StatusConflict)
		return
	}
	if !runtimeSelectorAllowsReset(selector) {
		http.Error(w, "Reset is not allowed for this selector", http.StatusConflict)
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	startedChatUUID := actionTaskStringField(selector, "started_chat_uuid")
	startedURL := actionTaskStringField(selector, "started_interaction_url")
	if startedChatUUID != "" || startedURL != "" {
		history := []interface{}{}
		if existing, ok := selector[runtimeSelectorResetHistoryKey].([]interface{}); ok {
			history = existing
		}
		entry := map[string]interface{}{
			"chat_uuid": startedChatUUID,
			"reset_by":  user.UUID,
			"reset_at":  now,
		}
		if startedURL != "" {
			entry["interaction_url"] = startedURL
		}
		selector[runtimeSelectorResetHistoryKey] = append(history, entry)
	}
	delete(selector, "started_chat_uuid")
	delete(selector, "started_interaction_url")
	delete(selector, "claim_id")
	delete(selector, "decided_by")
	delete(selector, "decided_at")
	selector["status"] = RuntimeSelectorPending
	selector[runtimeSelectorResetCountKey] = runtimeSelectorResetCount(selector) + 1
	if err := persistRuntimeSelectorMeta(DB, message, meta, selectors); err != nil {
		logging.Warnf("runtime selector reset failed to persist (chat=%s message=%s): %v",
			message.UUID, message.UUID, err)
		http.Error(w, "Failed to update runtime selector", http.StatusInternalServerError)
		return
	}
	writeRuntimeSelectorResolved(w, selector)
}

// writeRuntimeSelectorResolved emits the successful resolve response for a
// selector whose status was just recorded.
func writeRuntimeSelectorResolved(w http.ResponseWriter, selector map[string]interface{}) {
	status, _ := selector["status"].(string)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ResolveRuntimeSelectorResponse{
		Success:  true,
		Status:   status,
		Selector: selector,
	})
}
