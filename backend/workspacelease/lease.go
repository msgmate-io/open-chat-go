// Package workspacelease implements the shared, advisory workspace-lease
// registry used by the coding integrations (opencode, msgmate_coder) to avoid
// concurrent interactions clobbering the same checkout.
//
// A lease records that an interaction works in a workspace directory. It is
// advisory: Acquire never rejects a caller, it only reports the other live
// holders of the workspace. Callers use that answer to isolate themselves (a
// per-interaction git worktree) or, for deliberately shared workspaces, to
// surface a busy/owner status to the dispatcher and the UI.
package workspacelease

import (
	"path"
	"strings"
	"time"

	"backend/database"

	"gorm.io/gorm"
)

// Lease kinds.
const (
	// KindWorktree marks a lease on an isolated per-interaction git worktree.
	KindWorktree = "worktree"
	// KindShared marks a lease on a workspace a caller deliberately shares with
	// other interactions (advisory only; the holders are reported, not enforced).
	KindShared = "shared"
	// KindSandbox marks a lease on a per-chat temporary sandbox/clone.
	KindSandbox = "sandbox"
)

// DefaultTTL is how long a lease stays live without being refreshed. Coding
// interactions are long-running, so a day is generous; every Acquire refreshes
// it.
const DefaultTTL = 24 * time.Hour

// Manager is the workspace-lease registry bound to a database handle.
type Manager struct {
	db *gorm.DB
}

// New returns a lease manager for db. A nil db yields a manager whose calls are
// inert (no holders, no persisted lease), so a coding integration never fails a
// tool call merely because the registry is unavailable.
func New(db *gorm.DB) *Manager {
	return &Manager{db: db}
}

// CanonicalKey normalizes a workspace directory into a stable lease key so the
// opencode project path and the coder workspace path of the same checkout map
// to the same lease. It uses slash semantics (workspaces can live on remote
// unix hosts), collapses redundant separators and strips a trailing slash.
func CanonicalKey(wsPath string) string {
	trimmed := strings.TrimSpace(wsPath)
	if trimmed == "" {
		return ""
	}
	cleaned := path.Clean(trimmed)
	if cleaned == "." {
		return ""
	}
	return cleaned
}

// AcquireRequest describes a lease acquisition/refresh.
type AcquireRequest struct {
	// Key is the workspace directory (see CanonicalKey).
	Key string
	// OwnerChatUUID identifies the interaction taking the lease.
	OwnerChatUUID string
	// OwnerUserID is the Open-Chat user behind the interaction (informational).
	OwnerUserID uint
	// OwnerLabel is a human-readable owner (e.g. the bot/agent name).
	OwnerLabel string
	// Kind is the lease kind (KindWorktree/KindShared/KindSandbox).
	Kind string
	// Branch / Path describe the isolated workspace the lease points at.
	Branch string
	Path   string
	// TTL overrides DefaultTTL when positive.
	TTL time.Duration
}

// Acquire records (or refreshes) this interaction's lease on req.Key and
// returns the stored lease plus the other live holders of the same workspace
// (excluding this interaction), after purging expired rows. It never fails on
// contention: callers decide whether to isolate or merely report the holders.
func (m *Manager) Acquire(req AcquireRequest) (database.WorkspaceLease, []database.WorkspaceLease, error) {
	key := CanonicalKey(req.Key)
	chat := strings.TrimSpace(req.OwnerChatUUID)
	mine := database.WorkspaceLease{
		WorkspaceKey:  key,
		OwnerChatUUID: chat,
		OwnerUserID:   req.OwnerUserID,
		OwnerLabel:    strings.TrimSpace(req.OwnerLabel),
		Kind:          strings.TrimSpace(req.Kind),
		Branch:        strings.TrimSpace(req.Branch),
		Path:          strings.TrimSpace(req.Path),
		ExpiresAt:     time.Now().UTC().Add(ttl(req.TTL)),
	}
	if key == "" || chat == "" || m == nil || m.db == nil {
		return mine, nil, nil
	}
	if err := m.purgeExpired(key); err != nil {
		return mine, nil, err
	}

	stored, err := m.upsert(mine)
	if err != nil {
		return mine, nil, err
	}
	others, err := m.Holders(key, chat)
	if err != nil {
		return stored, nil, err
	}
	return stored, others, nil
}

// Holder returns the most recent live holder of key that is not excludeChat.
func (m *Manager) Holder(key, excludeChat string) (database.WorkspaceLease, bool, error) {
	others, err := m.Holders(key, excludeChat)
	if err != nil || len(others) == 0 {
		return database.WorkspaceLease{}, false, err
	}
	return others[0], true, nil
}

// Holders returns every live lease on key except the one owned by excludeChat.
// Expired rows are purged first.
func (m *Manager) Holders(key, excludeChat string) ([]database.WorkspaceLease, error) {
	canonical := CanonicalKey(key)
	if canonical == "" || m == nil || m.db == nil {
		return nil, nil
	}
	if err := m.purgeExpired(canonical); err != nil {
		return nil, err
	}
	rows := []database.WorkspaceLease{}
	query := m.db.Where("workspace_key = ?", canonical)
	if exclude := strings.TrimSpace(excludeChat); exclude != "" {
		query = query.Where("owner_chat_uuid <> ?", exclude)
	}
	if err := query.Order("updated_at desc").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// Release removes this interaction's lease on key (if present).
func (m *Manager) Release(key, chatUUID string) error {
	canonical := CanonicalKey(key)
	if canonical == "" || m == nil || m.db == nil {
		return nil
	}
	return m.db.Where("workspace_key = ? AND owner_chat_uuid = ?", canonical, strings.TrimSpace(chatUUID)).
		Delete(&database.WorkspaceLease{}).Error
}

// ReleaseForChat removes every lease owned by an interaction (e.g. when it
// finishes) and reports how many rows were removed.
func (m *Manager) ReleaseForChat(chatUUID string) (int64, error) {
	chat := strings.TrimSpace(chatUUID)
	if chat == "" || m == nil || m.db == nil {
		return 0, nil
	}
	res := m.db.Where("owner_chat_uuid = ?", chat).Delete(&database.WorkspaceLease{})
	return res.RowsAffected, res.Error
}

// Status returns a JSON-friendly description of the workspace lease relative to
// the asking interaction, used to surface a busy/owner state. It reports every
// other live holder under "holders" and the first one ("owner_chat_uuid") for
// convenience.
func (m *Manager) Status(key, chatUUID string) map[string]interface{} {
	canonical := CanonicalKey(key)
	status := map[string]interface{}{
		"workspace_key":   canonical,
		"busy":            false,
		"held_by_other":   false,
		"owner_chat_uuid": "",
		"owner_label":     "",
		"holders":         []interface{}{},
	}
	others, err := m.Holders(canonical, chatUUID)
	if err != nil || len(others) == 0 {
		return status
	}
	holders := make([]interface{}, 0, len(others))
	for _, h := range others {
		holders = append(holders, map[string]interface{}{
			"owner_chat_uuid": h.OwnerChatUUID,
			"owner_user_id":   h.OwnerUserID,
			"owner_label":     h.OwnerLabel,
			"kind":            h.Kind,
			"branch":          h.Branch,
			"path":            h.Path,
			"expires_at_unix": h.ExpiresAt.Unix(),
		})
	}
	status["holders"] = holders
	status["busy"] = true
	status["held_by_other"] = true
	status["owner_chat_uuid"] = others[0].OwnerChatUUID
	status["owner_label"] = others[0].OwnerLabel
	return status
}

func (m *Manager) upsert(mine database.WorkspaceLease) (database.WorkspaceLease, error) {
	existing := database.WorkspaceLease{}
	err := m.db.Where("workspace_key = ? AND owner_chat_uuid = ?", mine.WorkspaceKey, mine.OwnerChatUUID).First(&existing).Error
	switch {
	case err == gorm.ErrRecordNotFound:
		row := mine
		if createErr := m.db.Create(&row).Error; createErr != nil {
			// Lost a race: fall back to updating the row the winner inserted.
			retry := database.WorkspaceLease{}
			if findErr := m.db.Where("workspace_key = ? AND owner_chat_uuid = ?", mine.WorkspaceKey, mine.OwnerChatUUID).First(&retry).Error; findErr != nil {
				return mine, createErr
			}
			existing = retry
			break
		}
		return row, nil
	case err != nil:
		return mine, err
	}

	updates := map[string]interface{}{
		"owner_user_id": mine.OwnerUserID,
		"owner_label":   mine.OwnerLabel,
		"kind":          mine.Kind,
		"branch":        mine.Branch,
		"path":          mine.Path,
		"expires_at":    mine.ExpiresAt,
	}
	if updateErr := m.db.Model(&database.WorkspaceLease{}).Where("id = ?", existing.ID).Updates(updates).Error; updateErr != nil {
		return existing, updateErr
	}
	existing.OwnerUserID = mine.OwnerUserID
	existing.OwnerLabel = mine.OwnerLabel
	existing.Kind = mine.Kind
	existing.Branch = mine.Branch
	existing.Path = mine.Path
	existing.ExpiresAt = mine.ExpiresAt
	return existing, nil
}

func (m *Manager) purgeExpired(key string) error {
	return m.db.Where("workspace_key = ? AND expires_at < ?", key, time.Now().UTC()).Delete(&database.WorkspaceLease{}).Error
}

func ttl(d time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return DefaultTTL
}
