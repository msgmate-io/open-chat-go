package chats

import (
	"backend/database"
	"backend/logging"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
)

// ci#1101 (part 2): the stop-on-detach half of the ticket makes the server stop
// driving an interaction whose local ssh link is gone. Stopping alone leaves the
// operator with an interaction that silently rests in `idle`/`stopped`, so the
// detach is also reported as a *visible* failure turn that carries a
// `tui_detached` metadata payload. The web UI renders that payload with the
// TuiDetachedEventWidget, which lists the upstream sandboxes the interaction can
// be continued in.
//
// The payload only ever carries identifiers and human labels:
//
//	sandbox uuid, provider name, a display label, a `running` flag, an expiry
//	timestamp and the transport name.
//
// It never carries an attach token, kubeconfig, owner identity, message text or
// any other user data - the widget is rendered in a shared chat too, so the
// payload is treated as public.
const (
	// tuiDetachedMetaKey is the message meta key the widget resolves.
	tuiDetachedMetaKey = "tui_detached"

	// TuiDetachedReason is the payload reason the widget accepts.
	TuiDetachedReason = "tui_detached"

	// tuiDetachedReportMetaKey marks the visible turn this reporter posted, so a
	// second sweep does not post a duplicate report for the same chat.
	tuiDetachedReportMetaKey = "tui_detached_report"

	// tuiDetachedScanLimit is how many recent messages are inspected when
	// checking whether a detach was already reported for a chat.
	tuiDetachedScanLimit = 10
)

// TuiDetachedCandidate is one upstream sandbox the interaction can be continued
// in. It is a *public* projection: identifiers and labels only.
type TuiDetachedCandidate struct {
	SandboxUUID string `json:"sandbox_uuid"`
	Provider    string `json:"provider,omitempty"`
	Label       string `json:"label,omitempty"`
	Running     bool   `json:"running,omitempty"`
	ExpiresAt   string `json:"expires_at,omitempty"`
}

// TuiDetachedEvent is the `tui_detached` metadata payload.
type TuiDetachedEvent struct {
	Reason          string                 `json:"reason"`
	ChatUUID        string                 `json:"chat_uuid,omitempty"`
	InteractionUUID string                 `json:"interaction_uuid,omitempty"`
	SessionID       string                 `json:"session_id,omitempty"`
	SandboxUUID     string                 `json:"sandbox_uuid,omitempty"`
	Transport       string                 `json:"transport,omitempty"`
	DetachedAt      string                 `json:"detached_at,omitempty"`
	DefaultSandbox  string                 `json:"default_sandbox,omitempty"`
	Candidates      []TuiDetachedCandidate `json:"candidates,omitempty"`
}

// TuiDetachedSandboxLister lists the upstream sandboxes a chat may be continued
// in. Consumers that know the sandbox inventory (the coder integration, the
// sandbox session store) register one; without a lister the report still names
// the failure, it simply offers no candidate.
type TuiDetachedSandboxLister func(ctx context.Context, chat database.Chat, owner database.User) []TuiDetachedCandidate

var tuiDetachedListers = struct {
	mu      sync.RWMutex
	lister  TuiDetachedSandboxLister
	enabled bool
}{}

// RegisterTuiDetachedSandboxLister installs the candidate lister. Passing nil
// clears it, which makes reports carry an empty candidate list.
func RegisterTuiDetachedSandboxLister(lister TuiDetachedSandboxLister) {
	tuiDetachedListers.mu.Lock()
	defer tuiDetachedListers.mu.Unlock()
	tuiDetachedListers.lister = lister
	tuiDetachedListers.enabled = lister != nil
}

// currentTuiDetachedLister returns the installed lister (or nil).
func currentTuiDetachedLister() TuiDetachedSandboxLister {
	tuiDetachedListers.mu.RLock()
	defer tuiDetachedListers.mu.RUnlock()
	return tuiDetachedListers.lister
}

// resetTuiDetachedListerForTest clears the lister (tests only).
func resetTuiDetachedListerForTest() { RegisterTuiDetachedSandboxLister(nil) }

// sanitizeTuiDetachedCandidates drops entries without a sandbox uuid, de-dupes
// them and clamps the list so a buggy lister cannot blow up the message meta.
func sanitizeTuiDetachedCandidates(candidates []TuiDetachedCandidate) []TuiDetachedCandidate {
	const maxCandidates = 10
	out := make([]TuiDetachedCandidate, 0, len(candidates))
	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		uuid := strings.TrimSpace(candidate.SandboxUUID)
		if uuid == "" {
			continue
		}
		if _, dup := seen[uuid]; dup {
			continue
		}
		seen[uuid] = struct{}{}
		out = append(out, TuiDetachedCandidate{
			SandboxUUID: uuid,
			Provider:    strings.TrimSpace(candidate.Provider),
			Label:       strings.TrimSpace(candidate.Label),
			Running:     candidate.Running,
			ExpiresAt:   strings.TrimSpace(candidate.ExpiresAt),
		})
		if len(out) == maxCandidates {
			break
		}
	}
	return out
}

// defaultTuiDetachedCandidate mirrors the widget's default: the sandbox the
// interaction ran in when it is still offered, else the first running one, else
// the first candidate.
func defaultTuiDetachedCandidate(event TuiDetachedEvent) string {
	if len(event.Candidates) == 0 {
		return ""
	}
	for _, candidate := range event.Candidates {
		if candidate.SandboxUUID == event.SandboxUUID {
			return candidate.SandboxUUID
		}
	}
	for _, candidate := range event.Candidates {
		if candidate.Running {
			return candidate.SandboxUUID
		}
	}
	return event.Candidates[0].SandboxUUID
}

// tuiDetachedAlreadyReported reports whether the chat already carries a detach
// report among its most recent messages, so a second sweep (or a clean end that
// follows an expiry) does not post a duplicate.
func tuiDetachedAlreadyReported(db *gorm.DB, chatID uint) bool {
	var messages []database.Message
	if err := db.Where("chat_id = ?", chatID).Order("id DESC").Limit(tuiDetachedScanLimit).Find(&messages).Error; err != nil {
		return false
	}
	for _, message := range messages {
		if len(message.MetaData) == 0 {
			continue
		}
		meta := map[string]interface{}{}
		if json.Unmarshal(message.MetaData, &meta) != nil {
			continue
		}
		if reported, ok := meta[tuiDetachedReportMetaKey].(bool); ok && reported {
			return true
		}
	}
	return false
}

// buildTuiDetachedText renders the operator-facing turn. It states what happened
// (the TUI that drove the interaction detached, so the run was stopped) and what
// to do next (continue it in one of the upstream sandboxes the widget lists).
func buildTuiDetachedText(event TuiDetachedEvent) string {
	transport := event.Transport
	if transport == "" {
		transport = "ssh"
	}
	text := "This interaction was stopped because the coder TUI that drove it over a local " +
		transport + " link detached, so nothing kept running upstream on your behalf."
	if len(event.Candidates) == 0 {
		return text + " No upstream sandbox is available to continue it in right now; once one is listed again you can resume the interaction."
	}
	return text + " Pick an upstream sandbox below to continue the interaction there."
}

// ReportTuiDetached posts the visible detach failure turn for a chat. It is
// idempotent per chat: a chat that already carries a detach report is left
// alone, so the sweeper and a clean end can both call it.
//
// The report is best effort: a chat that is not an interaction, has no
// automated participant or cannot be written returns false and the caller
// carries on - the stop itself already happened.
func ReportTuiDetached(db *gorm.DB, chat database.Chat, event TuiDetachedEvent, now time.Time) bool {
	if db == nil || chat.ID == 0 {
		return false
	}
	if !isInteractionChatType(chat.ChatType) {
		return false
	}
	if tuiDetachedAlreadyReported(db, chat.ID) {
		return false
	}

	chat = hydrateChatParticipants(db, chat)
	bot := interactionAutomatedParticipant(chat)
	if bot == nil {
		return false
	}
	owner := chat.User1
	if owner.ID == bot.ID {
		owner = chat.User2
	}
	if owner.ID == 0 {
		return false
	}

	if event.Reason == "" {
		event.Reason = TuiDetachedReason
	}
	event.ChatUUID = chat.UUID
	if event.DetachedAt == "" {
		event.DetachedAt = now.UTC().Format(time.RFC3339)
	}
	if event.Transport == "" {
		event.Transport = "ssh"
	}
	event.Candidates = sanitizeTuiDetachedCandidates(event.Candidates)
	event.DefaultSandbox = defaultTuiDetachedCandidate(event)

	text := buildTuiDetachedText(event)
	metadata := map[string]interface{}{
		"finished":               true,
		"error":                  true,
		"failure_reason":         TuiDetachedReason,
		"stop_reason":            localLinkStopReason,
		tuiDetachedReportMetaKey: true,
		tuiDetachedMetaKey:       event,
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return false
	}

	message := database.Message{
		ChatId:     chat.ID,
		SenderId:   bot.ID,
		ReceiverId: owner.ID,
		Text:       &text,
		DataType:   "text",
		MetaData:   database.JSONRaw(encoded),
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&message).Error; err != nil {
			return err
		}
		return tx.Model(&database.Chat{}).Where("id = ?", chat.ID).
			Update("latest_message_id", message.ID).Error
	}); err != nil {
		logging.Errorf("ci#1101: failed to report the detached TUI of chat %s: %v", chat.UUID, err)
		return false
	}
	logging.Debugf("ci#1101: reported the detached TUI of chat %s (%d candidate sandbox(es))", chat.UUID, len(event.Candidates))
	return true
}

// tuiDetachedEventForChat builds the payload for a chat: the identifier-only
// skeleton plus whatever the registered lister offers. The lister is called with
// the interaction owner so it can scope the sandboxes to them.
func tuiDetachedEventForChat(db *gorm.DB, chat database.Chat, reason string) TuiDetachedEvent {
	event := TuiDetachedEvent{Reason: reason, ChatUUID: chat.UUID, Transport: "ssh"}
	if event.Reason == "" {
		event.Reason = TuiDetachedReason
	}
	lister := currentTuiDetachedLister()
	if lister == nil {
		return event
	}
	owner := chat.User1
	if owner.ID == 0 {
		owner = chat.User2
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			logging.Errorf("ci#1101: sandbox lister panicked for chat %s: %v", chat.UUID, recovered)
		}
	}()
	event.Candidates = sanitizeTuiDetachedCandidates(lister(context.Background(), chat, owner))
	if len(event.Candidates) > 0 {
		event.DefaultSandbox = defaultTuiDetachedCandidate(event)
	}
	return event
}
