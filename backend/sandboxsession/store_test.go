package sandboxsession

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"backend/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newSessionStoreTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "sessions.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	database.CloseTestDB(t, db)
	return db
}

func TestStoreCreateAndGetForOwner(t *testing.T) {
	store := NewStore(newSessionStoreTestDB(t))
	ctx := context.Background()

	session := Session{Provider: "local", SandboxUUID: "sbx-1", SessionID: "sess-1", AttachPath: "/terminal/sess-1", AttachToken: "tok"}
	rec, err := store.Create(ctx, RecordFromSession(session, "user-1", "alice", "chat-1", "int-1", time.Now().UTC()))
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if rec.UUID == "" {
		t.Fatal("Create() returned empty uuid")
	}

	got, err := store.GetForOwner(ctx, rec.UUID, "user-1")
	if err != nil {
		t.Fatalf("GetForOwner() error = %v", err)
	}
	if got.SandboxUUID != "sbx-1" || got.SessionID != "sess-1" || got.AttachToken != "tok" {
		t.Fatalf("unexpected record: %+v", got)
	}

	if _, err := store.GetForOwner(ctx, rec.UUID, "user-2"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("GetForOwner() for other owner error = %v, want ErrSessionNotFound", err)
	}
	if _, err := store.Get(ctx, "does-not-exist"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("Get() for unknown uuid error = %v, want ErrSessionNotFound", err)
	}
}

func TestStoreRecordDefaultsExpiry(t *testing.T) {
	store := NewStore(newSessionStoreTestDB(t))
	now := time.Now().UTC()

	rec, err := store.Create(context.Background(), RecordFromSession(Session{Provider: "local", SandboxUUID: "sbx-1"}, "user-1", "alice", "", "", now))
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if !rec.ExpiresAt.Equal(now.Add(DefaultStoredSessionTTL)) {
		t.Fatalf("ExpiresAt = %v, want %v", rec.ExpiresAt, now.Add(DefaultStoredSessionTTL))
	}
}

func TestStoreDetachKeepsSessionAndDestroyIsExplicit(t *testing.T) {
	store := NewStore(newSessionStoreTestDB(t))
	ctx := context.Background()

	rec, err := store.Create(ctx, RecordFromSession(Session{Provider: "local", SandboxUUID: "sbx-1", SessionID: "sess-1"}, "user-1", "alice", "", "", time.Now().UTC()))
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	detached, err := store.MarkDetached(ctx, rec.UUID, "user-1")
	if err != nil {
		t.Fatalf("MarkDetached() error = %v", err)
	}
	if !detached.Detached() {
		t.Fatal("MarkDetached() did not set DetachedAt")
	}
	if detached.Destroyed() {
		t.Fatal("MarkDetached() must not destroy the sandbox")
	}

	// A detached session is still listed and still re-attachable.
	live, err := store.ListForOwner(ctx, "user-1")
	if err != nil {
		t.Fatalf("ListForOwner() error = %v", err)
	}
	if len(live) != 1 {
		t.Fatalf("ListForOwner() returned %d records, want 1", len(live))
	}

	reattached, err := store.MarkAttached(ctx, rec.UUID, "user-1", Session{SessionID: "sess-2", AttachToken: "tok-2", AttachPath: "/terminal/sess-2", ExpiresAt: time.Now().UTC().Add(2 * time.Hour)})
	if err != nil {
		t.Fatalf("MarkAttached() error = %v", err)
	}
	if reattached.Detached() {
		t.Fatal("MarkAttached() must clear DetachedAt")
	}
	if reattached.SessionID != "sess-2" || reattached.AttachToken != "tok-2" || reattached.AttachPath != "/terminal/sess-2" {
		t.Fatalf("MarkAttached() did not refresh attach credentials: %+v", reattached)
	}

	destroyed, err := store.MarkDestroyed(ctx, rec.UUID, "user-1")
	if err != nil {
		t.Fatalf("MarkDestroyed() error = %v", err)
	}
	if !destroyed.Destroyed() {
		t.Fatal("MarkDestroyed() did not set DestroyedAt")
	}

	live, err = store.ListForOwner(ctx, "user-1")
	if err != nil {
		t.Fatalf("ListForOwner() error = %v", err)
	}
	if len(live) != 0 {
		t.Fatalf("destroyed session still listed: %+v", live)
	}

	if _, err := store.MarkDetached(ctx, rec.UUID, "user-1"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("MarkDetached() on destroyed session error = %v, want ErrInvalidRequest", err)
	}
}

func TestStoreOperationsAreOwnerScoped(t *testing.T) {
	store := NewStore(newSessionStoreTestDB(t))
	ctx := context.Background()

	rec, err := store.Create(ctx, RecordFromSession(Session{Provider: "local", SandboxUUID: "sbx-1", SessionID: "sess-1"}, "user-1", "alice", "", "", time.Now().UTC()))
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if _, err := store.MarkDetached(ctx, rec.UUID, "user-2"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("MarkDetached() for other owner error = %v, want ErrSessionNotFound", err)
	}
	if _, err := store.MarkAttached(ctx, rec.UUID, "user-2", Session{SessionID: "x"}); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("MarkAttached() for other owner error = %v, want ErrSessionNotFound", err)
	}
	if _, err := store.MarkDestroyed(ctx, rec.UUID, "user-2"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("MarkDestroyed() for other owner error = %v, want ErrSessionNotFound", err)
	}

	other, err := store.ListForOwner(ctx, "user-2")
	if err != nil {
		t.Fatalf("ListForOwner() error = %v", err)
	}
	if len(other) != 0 {
		t.Fatalf("ListForOwner() leaked %d records to another owner", len(other))
	}

	// The record is untouched by the rejected calls.
	got, err := store.GetForOwner(ctx, rec.UUID, "user-1")
	if err != nil {
		t.Fatalf("GetForOwner() error = %v", err)
	}
	if got.Detached() || got.Destroyed() {
		t.Fatalf("record mutated by another owner: %+v", got)
	}
}

func TestStoreRequiresDatabase(t *testing.T) {
	store := NewStore(nil)
	if _, err := store.Create(context.Background(), SessionRecord{}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Create() without db error = %v, want ErrInvalidRequest", err)
	}
}

func TestNewUUIDIsUnique(t *testing.T) {
	seen := make(map[string]struct{}, 128)
	for i := 0; i < 128; i++ {
		id := NewUUID()
		if len(id) != 36 {
			t.Fatalf("NewUUID() = %q, want 36 chars", id)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("NewUUID() returned duplicate %q", id)
		}
		seen[id] = struct{}{}
	}
}
