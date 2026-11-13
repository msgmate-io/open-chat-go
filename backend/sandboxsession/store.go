package sandboxsession

// Persistence for continuable sandbox sessions (ci#1060).
//
// A row records everything needed to re-attach to a detached-but-alive sandbox
// session from the CLI/TUI: which sandbox it lives in, the interactive session
// id, the owner, the originating chat/interaction, the short lived attach token
// and the session expiry. Detaching writes `detached_at` and leaves the row (and
// therefore the sandbox) intact; destroying is recorded separately in
// `destroyed_at` so audit can tell the two apart.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
)

// ErrSessionNotFound is returned when a persisted session does not exist or is
// not owned by the requesting user.
var ErrSessionNotFound = errors.New("sandboxsession: session not found")

// DefaultStoredSessionTTL is how long a detached session stays re-attachable.
const DefaultStoredSessionTTL = 12 * time.Hour

// SessionRecord is the persisted form of a continuable sandbox session.
type SessionRecord struct {
	UUID            string `gorm:"primaryKey;size:64"`
	Provider        string `gorm:"size:64"`
	SandboxUUID     string `gorm:"size:64;index"`
	SessionID       string `gorm:"size:128;index"`
	OwnerUUID       string `gorm:"size:64;index"`
	OwnerUsername   string `gorm:"size:128"`
	ChatUUID        string `gorm:"size:64;index"`
	InteractionUUID string `gorm:"size:64;index"`
	AttachPath      string `gorm:"size:255"`
	AttachToken     string `gorm:"size:128;index"`
	ExpiresAt       time.Time
	DetachedAt      *time.Time
	DestroyedAt     *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// TableName pins the table name.
func (SessionRecord) TableName() string { return "sandbox_session" }

// Detached reports whether the shell session has been detached.
func (r SessionRecord) Detached() bool { return r.DetachedAt != nil }

// Destroyed reports whether the sandbox was explicitly destroyed.
func (r SessionRecord) Destroyed() bool { return r.DestroyedAt != nil }

// Expired reports whether the record is past its expiry.
func (r SessionRecord) Expired(now time.Time) bool {
	return !r.ExpiresAt.IsZero() && now.After(r.ExpiresAt)
}

// Store persists SessionRecords. The schema is created on first use so consumers
// only need a *gorm.DB.
type Store struct {
	db   *gorm.DB
	once sync.Once
	err  error
}

// NewStore returns a store bound to db.
func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

func (s *Store) schema() error {
	if s == nil || s.db == nil {
		return fmt.Errorf("%w: store has no database", ErrInvalidRequest)
	}
	s.once.Do(func() {
		s.err = s.db.AutoMigrate(&SessionRecord{})
	})
	return s.err
}

// RecordFromSession builds a persistable record for an opened/attached session.
func RecordFromSession(session Session, ownerUUID, ownerUsername, chatUUID, interactionUUID string, now time.Time) SessionRecord {
	expires := session.ExpiresAt
	if expires.IsZero() {
		expires = now.Add(DefaultStoredSessionTTL)
	}
	return SessionRecord{
		UUID:            NewUUID(),
		Provider:        session.Provider,
		SandboxUUID:     session.SandboxUUID,
		SessionID:       session.SessionID,
		OwnerUUID:       ownerUUID,
		OwnerUsername:   ownerUsername,
		ChatUUID:        chatUUID,
		InteractionUUID: interactionUUID,
		AttachPath:      session.AttachPath,
		AttachToken:     session.AttachToken,
		ExpiresAt:       expires,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

// Create inserts rec.
func (s *Store) Create(ctx context.Context, rec SessionRecord) (SessionRecord, error) {
	if err := s.schema(); err != nil {
		return SessionRecord{}, err
	}
	if strings.TrimSpace(rec.UUID) == "" {
		rec.UUID = NewUUID()
	}
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now().UTC()
	}
	rec.UpdatedAt = rec.CreatedAt
	if rec.ExpiresAt.IsZero() {
		rec.ExpiresAt = rec.CreatedAt.Add(DefaultStoredSessionTTL)
	}
	if err := s.db.WithContext(ctx).Create(&rec).Error; err != nil {
		return SessionRecord{}, fmt.Errorf("sandboxsession: create session record: %w", err)
	}
	return rec, nil
}

// Get returns the record by uuid.
func (s *Store) Get(ctx context.Context, uuid string) (SessionRecord, error) {
	if err := s.schema(); err != nil {
		return SessionRecord{}, err
	}
	var rec SessionRecord
	err := s.db.WithContext(ctx).Where("uuid = ?", strings.TrimSpace(uuid)).First(&rec).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return SessionRecord{}, ErrSessionNotFound
	}
	if err != nil {
		return SessionRecord{}, fmt.Errorf("sandboxsession: load session record: %w", err)
	}
	return rec, nil
}

// GetForOwner returns the record by uuid, scoped to its owner. A record owned by
// somebody else is reported as not found so endpoints cannot be used to probe.
func (s *Store) GetForOwner(ctx context.Context, uuid, ownerUUID string) (SessionRecord, error) {
	rec, err := s.Get(ctx, uuid)
	if err != nil {
		return SessionRecord{}, err
	}
	if strings.TrimSpace(ownerUUID) == "" || rec.OwnerUUID != strings.TrimSpace(ownerUUID) {
		return SessionRecord{}, ErrSessionNotFound
	}
	return rec, nil
}

// MarkDetached records a detach. It is idempotent and never deletes the row: the
// sandbox stays alive and re-attachable.
func (s *Store) MarkDetached(ctx context.Context, uuid, ownerUUID string) (SessionRecord, error) {
	rec, err := s.GetForOwner(ctx, uuid, ownerUUID)
	if err != nil {
		return SessionRecord{}, err
	}
	if rec.Destroyed() {
		return SessionRecord{}, fmt.Errorf("%w: session was destroyed", ErrInvalidRequest)
	}
	now := time.Now().UTC()
	rec.DetachedAt = &now
	rec.UpdatedAt = now
	if err := s.db.WithContext(ctx).Save(&rec).Error; err != nil {
		return SessionRecord{}, fmt.Errorf("sandboxsession: detach session record: %w", err)
	}
	return rec, nil
}

// MarkAttached clears the detach marker and refreshes the attach token/expiry so
// a subsequent AttachSandbox hands out a fresh credential.
func (s *Store) MarkAttached(ctx context.Context, uuid, ownerUUID string, session Session) (SessionRecord, error) {
	rec, err := s.GetForOwner(ctx, uuid, ownerUUID)
	if err != nil {
		return SessionRecord{}, err
	}
	if rec.Destroyed() {
		return SessionRecord{}, fmt.Errorf("%w: session was destroyed", ErrInvalidRequest)
	}
	now := time.Now().UTC()
	rec.DetachedAt = nil
	rec.UpdatedAt = now
	if session.SessionID != "" {
		rec.SessionID = session.SessionID
	}
	if session.AttachToken != "" {
		rec.AttachToken = session.AttachToken
	}
	if session.AttachPath != "" {
		rec.AttachPath = session.AttachPath
	}
	if !session.ExpiresAt.IsZero() {
		rec.ExpiresAt = session.ExpiresAt
	}
	if err := s.db.WithContext(ctx).Save(&rec).Error; err != nil {
		return SessionRecord{}, fmt.Errorf("sandboxsession: attach session record: %w", err)
	}
	return rec, nil
}

// MarkDestroyed records the explicit destruction of the sandbox. This is the only
// store operation that marks a sandbox gone; detach never does.
func (s *Store) MarkDestroyed(ctx context.Context, uuid, ownerUUID string) (SessionRecord, error) {
	rec, err := s.GetForOwner(ctx, uuid, ownerUUID)
	if err != nil {
		return SessionRecord{}, err
	}
	if rec.Destroyed() {
		return rec, nil
	}
	now := time.Now().UTC()
	rec.DestroyedAt = &now
	rec.UpdatedAt = now
	if err := s.db.WithContext(ctx).Save(&rec).Error; err != nil {
		return SessionRecord{}, fmt.Errorf("sandboxsession: destroy session record: %w", err)
	}
	return rec, nil
}

// ListForOwner returns the owner's live (not destroyed) sessions, newest first.
func (s *Store) ListForOwner(ctx context.Context, ownerUUID string) ([]SessionRecord, error) {
	if err := s.schema(); err != nil {
		return nil, err
	}
	var recs []SessionRecord
	err := s.db.WithContext(ctx).
		Where("owner_uuid = ? AND destroyed_at IS NULL", strings.TrimSpace(ownerUUID)).
		Order("created_at DESC").
		Find(&recs).Error
	if err != nil {
		return nil, fmt.Errorf("sandboxsession: list session records: %w", err)
	}
	return recs, nil
}

// NewUUID returns a random RFC4122 v4 style identifier.
func NewUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing is unrecoverable; fall back to a timestamp so the
		// caller still gets a unique-ish identifier instead of panicking on a
		// request path.
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}
