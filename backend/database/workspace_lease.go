package database

import "time"

// WorkspaceLease is an advisory, process-wide occupancy record of which
// interaction currently works in a coding workspace (a checkout directory on a
// host). It is deliberately shared by every coding integration (opencode,
// msgmate_coder, ...) so concurrent agents bound to the same path can see who
// else is working there before they clobber each other.
//
// A lease is advisory: it never blocks a caller. A coding integration reads the
// other live holders, decides whether to isolate itself (e.g. via a
// per-interaction git worktree) and records its own lease. Expired leases are
// treated as free. Multiple interactions may hold a lease on the same workspace
// key at once (deliberate sharing), which is exactly what the status surface
// reports.
//
// It intentionally does not embed Model: leases are hard-deleted (a soft-deleted
// row would keep occupying the unique index).
type WorkspaceLease struct {
	ID            uint      `gorm:"primarykey" json:"-"`
	WorkspaceKey  string    `gorm:"size:1024;uniqueIndex:idx_workspace_lease_key_chat" json:"workspace_key"`
	OwnerChatUUID string    `gorm:"size:64;uniqueIndex:idx_workspace_lease_key_chat" json:"owner_chat_uuid"`
	OwnerUserID   uint      `gorm:"index" json:"owner_user_id"`
	OwnerLabel    string    `gorm:"size:255" json:"owner_label,omitempty"`
	Kind          string    `gorm:"size:32" json:"kind"`
	Branch        string    `gorm:"size:255" json:"branch,omitempty"`
	Path          string    `gorm:"size:1024" json:"path,omitempty"`
	ExpiresAt     time.Time `gorm:"index" json:"expires_at"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}
