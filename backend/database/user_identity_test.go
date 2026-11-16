package database

import (
	"errors"
	"path/filepath"
	"testing"

	"gorm.io/gorm"
)

func setupIdentityTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	return SetupTestDatabase(t, DBConfig{
		Backend:  "sqlite",
		FilePath: filepath.Join(t.TempDir(), "user_identity_test.db"),
		ResetDB:  true,
	})
}

func mustCreateUser(t *testing.T, db *gorm.DB, username, email string, isAdmin bool) *User {
	t.Helper()
	user := User{Username: username, Email: email, IsAdmin: isAdmin}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user %s: %v", username, err)
	}
	return &user
}

// A password account that later links Google must resolve to the same user id
// through both login paths (ci#1111 acceptance #1). The Google email is an
// alias here: the username from the bootstrap mapping is what matches, and the
// mismatch on email must no longer matter because the link is persisted.
func TestResolveIdentityLinksExistingPasswordUser(t *testing.T) {
	db := setupIdentityTestDB(t)
	user := mustCreateUser(t, db, "open-chat-go-admin", "admin@example.com", true)
	if err := SetUserPassword(db, user.ID, "correct horse battery"); err != nil {
		t.Fatalf("set password: %v", err)
	}

	resolved, created, linked, err := ResolveIdentityAccount(db, IdentityAccountRequest{
		Issuer: "https://accounts.google.com", Subject: "google-sub-1",
		Email: "admin+google@example.com", Username: "open-chat-go-admin", Provision: true,
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if created {
		t.Fatal("expected no new account for the admin bootstrap mapping")
	}
	if !linked {
		t.Fatal("expected the identity to be linked during this call")
	}
	if resolved.ID != user.ID {
		t.Fatalf("expected resolution to land on user %d, got %d", user.ID, resolved.ID)
	}

	// Second login resolves through the persisted link only.
	again, created, linked, err := ResolveIdentityAccount(db, IdentityAccountRequest{
		Issuer: "https://accounts.google.com", Subject: "google-sub-1", Provision: false,
	})
	if err != nil || again == nil || again.ID != user.ID || created || linked {
		t.Fatalf("link-first resolution failed: user=%v created=%v linked=%v err=%v", again, created, linked, err)
	}

	// Password path still works for the same row.
	var reloaded User
	if err := db.First(&reloaded, "id = ?", user.ID).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if err := VerifyUserPassword(&reloaded, "correct horse battery"); err != nil {
		t.Fatalf("password login for linked account failed: %v", err)
	}
}

// An OIDC-provisioned account can attach a password and then log in either way
// (ci#1111 acceptance #2).
func TestOIDCProvisionedUserCanSetPassword(t *testing.T) {
	db := setupIdentityTestDB(t)

	user, created, _, err := ResolveIdentityAccount(db, IdentityAccountRequest{
		Issuer: "https://accounts.google.com", Subject: "google-sub-2",
		Email: "newcomer@example.com", Username: "newcomer", Provision: true,
	})
	if err != nil {
		t.Fatalf("resolve/provision: %v", err)
	}
	if !created || user == nil {
		t.Fatal("expected a newly provisioned account")
	}
	if AccountHasPassword(user) {
		t.Fatal("provisioned account must not carry a password hash")
	}
	if err := VerifyUserPassword(user, "whatever123"); !errors.Is(err, ErrAccountHasNoPassword) {
		t.Fatalf("expected ErrAccountHasNoPassword, got %v", err)
	}

	if err := SetUserPassword(db, user.ID, "brand new password"); err != nil {
		t.Fatalf("set password on provisioned account: %v", err)
	}
	var reloaded User
	if err := db.First(&reloaded, "id = ?", user.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if err := VerifyUserPassword(&reloaded, "brand new password"); err != nil {
		t.Fatalf("password login after set-password failed: %v", err)
	}

	byIdentity, _, _, err := ResolveIdentityAccount(db, IdentityAccountRequest{
		Issuer: "https://accounts.google.com", Subject: "google-sub-2", Provision: false,
	})
	if err != nil || byIdentity == nil || byIdentity.ID != user.ID {
		t.Fatalf("identity login must resolve to the same user: %v %v", byIdentity, err)
	}
}

// A bootstrap mapping may never silently fork a second privileged account
// (ci#1111 acceptance #3 - the ci.msgmate.io pair).
func TestBootstrapAdminDuplicateIsRefused(t *testing.T) {
	db := setupIdentityTestDB(t)
	existing := mustCreateUser(t, db, "open-chat-go-admin", "someone@corp.example", true)

	_, _, _, err := ResolveIdentityAccount(db, IdentityAccountRequest{
		Issuer: "https://accounts.google.com", Subject: "google-sub-3",
		Email: "admin@corp.example", Username: "admin", IsAdmin: true, Provision: true,
	})
	if !errors.Is(err, ErrIdentityLinkRequired) {
		t.Fatalf("expected ErrIdentityLinkRequired, got %v", err)
	}

	var count int64
	if err := db.Model(&User{}).Where("is_admin = ?", true).Count(&count).Error; err != nil {
		t.Fatalf("count admins: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly one admin row, got %d (existing %d left untouched)", count, existing.ID)
	}
}

// Explicit link creation is idempotent per account and rejects re-pointing.
func TestLinkUserIdentityConflict(t *testing.T) {
	db := setupIdentityTestDB(t)
	first := mustCreateUser(t, db, "first", "first@example.com", false)
	second := mustCreateUser(t, db, "second", "second@example.com", false)

	if _, err := LinkUserIdentity(db, first.ID, "iss", "sub", "", "", IdentityLinkSourceAdmin); err != nil {
		t.Fatalf("initial link: %v", err)
	}
	if _, err := LinkUserIdentity(db, first.ID, "iss", "sub", "", "", IdentityLinkSourceAdmin); err != nil {
		t.Fatalf("idempotent re-link must succeed: %v", err)
	}
	if _, err := LinkUserIdentity(db, second.ID, "iss", "sub", "", "", IdentityLinkSourceAdmin); !errors.Is(err, ErrIdentityAlreadyLinked) {
		t.Fatalf("expected ErrIdentityAlreadyLinked, got %v", err)
	}

	links, err := ListUserIdentities(db, first.ID)
	if err != nil || len(links) != 1 {
		t.Fatalf("expected exactly one link, got %d (%v)", len(links), err)
	}
	// Unlinking frees the pair for a deliberate re-link (admin repair).
	if err := UnlinkUserIdentity(db, first.ID, links[0].UUID); err != nil {
		t.Fatalf("unlink: %v", err)
	}
	if _, err := LinkUserIdentity(db, second.ID, "iss", "sub", "", "", IdentityLinkSourceAdmin); err != nil {
		t.Fatalf("re-link after unlink: %v", err)
	}
}
