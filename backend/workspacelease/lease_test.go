package workspacelease

import (
	"path/filepath"
	"testing"
	"time"

	"backend/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newLeaseTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "leases.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	database.CloseTestDB(t, db)
	if err := db.AutoMigrate(&database.WorkspaceLease{}); err != nil {
		t.Fatalf("failed to migrate workspace lease: %v", err)
	}
	return db
}

func TestCanonicalKeyNormalizesPaths(t *testing.T) {
	cases := map[string]string{
		"/workspace/open-chat-go/": "/workspace/open-chat-go",
		"/workspace//open-chat-go": "/workspace/open-chat-go",
		"  /srv/repo  ":            "/srv/repo",
		"/srv/a/../b":              "/srv/b",
		"":                         "",
		"   ":                      "",
		".":                        "",
	}
	for in, want := range cases {
		if got := CanonicalKey(in); got != want {
			t.Fatalf("CanonicalKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAcquireReportsOtherHolders(t *testing.T) {
	m := New(newLeaseTestDB(t))
	key := "/workspace/open-chat-go"

	lease, others, err := m.Acquire(AcquireRequest{Key: key, OwnerChatUUID: "chat-a", OwnerUserID: 7, Kind: KindShared})
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if lease.OwnerChatUUID != "chat-a" {
		t.Fatalf("expected chat-a owner, got %q", lease.OwnerChatUUID)
	}
	if len(others) != 0 {
		t.Fatalf("first holder should see no others, got %+v", others)
	}

	// A second interaction on the same workspace must see the first.
	_, others, err = m.Acquire(AcquireRequest{Key: key, OwnerChatUUID: "chat-b", Kind: KindWorktree, Path: "/tmp/wt/b"})
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	if len(others) != 1 || others[0].OwnerChatUUID != "chat-a" {
		t.Fatalf("expected chat-b to see chat-a as holder, got %+v", others)
	}

	status := m.Status(key, "chat-b")
	if busy, _ := status["busy"].(bool); !busy {
		t.Fatalf("expected status busy=true, got %+v", status)
	}
	if heldByOther, _ := status["held_by_other"].(bool); !heldByOther {
		t.Fatalf("expected status held_by_other=true, got %+v", status)
	}
	if got, _ := status["owner_chat_uuid"].(string); got != "chat-a" {
		t.Fatalf("expected owner chat-a, got %q", got)
	}

	// A refresh by chat-a must not report itself as a holder.
	_, selfOthers, err := m.Acquire(AcquireRequest{Key: key, OwnerChatUUID: "chat-a", Kind: KindShared})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if len(selfOthers) != 1 || selfOthers[0].OwnerChatUUID != "chat-b" {
		t.Fatalf("expected chat-a to see only chat-b, got %+v", selfOthers)
	}
}

func TestAcquireTreatsExpiredLeaseAsFree(t *testing.T) {
	m := New(newLeaseTestDB(t))
	key := "/srv/repo"

	if _, _, err := m.Acquire(AcquireRequest{Key: key, OwnerChatUUID: "old", TTL: time.Nanosecond}); err != nil {
		t.Fatalf("seed acquire: %v", err)
	}
	time.Sleep(2 * time.Millisecond)

	_, others, err := m.Acquire(AcquireRequest{Key: key, OwnerChatUUID: "new", Kind: KindWorktree})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if len(others) != 0 {
		t.Fatalf("expected expired lease to be invisible, got %+v", others)
	}
}

func TestReleaseFreesWorkspace(t *testing.T) {
	m := New(newLeaseTestDB(t))
	key := "/srv/repo"
	if _, _, err := m.Acquire(AcquireRequest{Key: key, OwnerChatUUID: "chat-a", Kind: KindShared}); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := m.Release(key, "chat-b"); err != nil {
		t.Fatalf("release by non-owner should be a no-op, got %v", err)
	}
	if _, ok, _ := m.Holder(key, "chat-b"); !ok {
		t.Fatalf("non-owner release must not remove the lease")
	}
	if err := m.Release(key, "chat-a"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, ok, _ := m.Holder(key, "chat-b"); ok {
		t.Fatalf("expected lease removed after owner release")
	}
}

func TestReleaseForChatRemovesAllLeases(t *testing.T) {
	m := New(newLeaseTestDB(t))
	for _, key := range []string{"/srv/a", "/srv/b"} {
		if _, _, err := m.Acquire(AcquireRequest{Key: key, OwnerChatUUID: "chat-a", Kind: KindWorktree}); err != nil {
			t.Fatalf("acquire %s: %v", key, err)
		}
	}
	n, err := m.ReleaseForChat("chat-a")
	if err != nil {
		t.Fatalf("release for chat: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 leases removed, got %d", n)
	}
}

func TestNilManagerIsInert(t *testing.T) {
	var m *Manager
	lease, others, err := m.Acquire(AcquireRequest{Key: "/srv/repo", OwnerChatUUID: "chat-a"})
	if err != nil || len(others) != 0 {
		t.Fatalf("nil manager acquire: others=%+v err=%v", others, err)
	}
	if lease.WorkspaceKey != "/srv/repo" {
		t.Fatalf("expected canonical key, got %q", lease.WorkspaceKey)
	}
	if _, ok, _ := m.Holder("/srv/repo", "chat-b"); ok {
		t.Fatalf("nil manager holder must report free")
	}
}
