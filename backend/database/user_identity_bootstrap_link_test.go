package database

import (
	"errors"
	"strings"
	"testing"

	"gorm.io/gorm"
)

// countUsers is the blunt invariant these tests care about: ci#1111 is a *row
// count* bug (two admins for one human), so every assertion below checks that no
// extra user row appears.
func countUsers(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var count int64
	if err := db.Model(&User{}).Count(&count).Error; err != nil {
		t.Fatalf("count users: %v", err)
	}
	return count
}

// Google login for the email the bootstrap admin was pinned to must resolve to
// that very user id, without provisioning a second row (ci#1111 acceptance #1,
// cause (c): the two provisioning paths must converge on one row).
func TestOIDCLoginBindsToBootstrapAdminUser(t *testing.T) {
	db := setupIdentityTestDB(t)
	admin := mustCreateUser(t, db, "open-chat-go-admin", "admin@legacy.local", true)

	linked, err := EnsureBootstrapAdminIdentity(db, admin.ID, "Admin@Example.com")
	if err != nil {
		t.Fatalf("pin bootstrap identity: %v", err)
	}
	if !linked {
		t.Fatal("expected the bootstrap identity to be linked on first run")
	}

	resolved, created, wasLinked, err := ResolveIdentityAccount(db, IdentityAccountRequest{
		Issuer:    "https://accounts.google.com",
		Subject:   "google-sub-1111",
		Email:     "admin@example.com",
		Username:  "admin",
		IsAdmin:   true,
		Provision: true,
	})
	if err != nil {
		t.Fatalf("resolve identity: %v", err)
	}
	if created {
		t.Fatal("expected the Google login to bind to the bootstrap admin, not provision a new user")
	}
	if !wasLinked {
		t.Fatal("expected the Google identity to be linked to the existing account")
	}
	if resolved == nil || resolved.ID != admin.ID {
		t.Fatalf("expected user id %d, got %#v", admin.ID, resolved)
	}
	if got := countUsers(t, db); got != 1 {
		t.Fatalf("expected exactly 1 user row after the Google login, got %d", got)
	}
}

// An identity nobody has ever seen is still provisioned: the binding path must
// not turn into "reject every login".
func TestOIDCLoginUnknownEmailStillProvisions(t *testing.T) {
	db := setupIdentityTestDB(t)
	admin := mustCreateUser(t, db, "open-chat-go-admin", "admin@example.com", true)
	if _, err := EnsureBootstrapAdminIdentity(db, admin.ID, "admin@example.com"); err != nil {
		t.Fatalf("pin bootstrap identity: %v", err)
	}

	resolved, created, _, err := ResolveIdentityAccount(db, IdentityAccountRequest{
		Issuer:    "https://accounts.google.com",
		Subject:   "stranger-sub",
		Email:     "stranger@example.com",
		Username:  "stranger",
		Provision: true,
	})
	if err != nil {
		t.Fatalf("resolve identity: %v", err)
	}
	if !created {
		t.Fatal("expected an unknown identity to be provisioned")
	}
	if resolved == nil || resolved.ID == admin.ID {
		t.Fatalf("expected a new user, got %#v", resolved)
	}
	if got := countUsers(t, db); got != 2 {
		t.Fatalf("expected 2 user rows (admin + stranger), got %d", got)
	}
}

// Re-login must be stable: the same external identity keeps resolving to the same
// row and the binding is idempotent on both paths (no second link row, no
// re-provisioning, no email churn).
func TestOIDCLoginAndBootstrapPinAreIdempotent(t *testing.T) {
	db := setupIdentityTestDB(t)
	admin := mustCreateUser(t, db, "open-chat-go-admin", "admin@legacy.local", true)

	if _, err := EnsureBootstrapAdminIdentity(db, admin.ID, "admin@example.com"); err != nil {
		t.Fatalf("pin bootstrap identity: %v", err)
	}

	req := IdentityAccountRequest{
		Issuer:    "https://accounts.google.com",
		Subject:   "google-sub-1111",
		Email:     "admin@example.com",
		Username:  "admin",
		IsAdmin:   true,
		Provision: true,
	}
	first, _, _, err := ResolveIdentityAccount(db, req)
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	second, created, _, err := ResolveIdentityAccount(db, req)
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if created {
		t.Fatal("re-login must not provision another user")
	}
	if first == nil || second == nil || first.ID != second.ID || first.ID != admin.ID {
		t.Fatalf("expected a stable user id %d, got first=%#v second=%#v", admin.ID, first, second)
	}

	links, err := ListUserIdentities(db, admin.ID)
	if err != nil {
		t.Fatalf("list identities: %v", err)
	}
	if len(links) != 2 {
		t.Fatalf("expected exactly 2 links (bootstrap + google), got %d", len(links))
	}

	// A later start re-runs the pin against the value the IdP asserts.
	linkedAgain, err := EnsureBootstrapAdminIdentity(db, admin.ID, "admin@example.com")
	if err != nil {
		t.Fatalf("re-pin bootstrap identity: %v", err)
	}
	if linkedAgain {
		t.Fatal("expected the second bootstrap pin to be a no-op")
	}

	var refreshed User
	if err := db.First(&refreshed, "id = ?", admin.ID).Error; err != nil {
		t.Fatalf("reload admin: %v", err)
	}
	if refreshed.Email != "admin@example.com" {
		t.Fatalf("expected the pinned email to survive, got %q", refreshed.Email)
	}
	if got := countUsers(t, db); got != 1 {
		t.Fatalf("expected exactly 1 user row, got %d", got)
	}
}

// A deployment that already forked into two admins must be *flagged*, never
// merged: no row is deleted, no email is stolen from the other account and both
// rows keep their data.
func TestDuplicateAdminAccountsAreFlaggedNotMerged(t *testing.T) {
	db := setupIdentityTestDB(t)
	bootstrapAdmin := mustCreateUser(t, db, "open-chat-go-admin", "admin@example.com", true)
	loginAdmin := mustCreateUser(t, db, "admin", "admin@legacy.local", true)

	duplicates, err := FindDuplicateAdminAccounts(db)
	if err != nil {
		t.Fatalf("find duplicate admins: %v", err)
	}
	if len(duplicates) != 2 {
		t.Fatalf("expected the fork to be reported as 2 admin rows, got %d", len(duplicates))
	}

	// The configured admin email is already owned by the *other* row: the pin
	// must refuse instead of re-pointing the email or the identity.
	linked, err := EnsureBootstrapAdminIdentity(db, loginAdmin.ID, "admin@example.com")
	if err == nil {
		t.Fatal("expected a duplicate report instead of silently claiming the email")
	}
	if !errors.Is(err, ErrIdentityLinkRequired) {
		t.Fatalf("expected ErrIdentityLinkRequired, got %v", err)
	}
	if linked {
		t.Fatal("expected no link row to be written by a refused pin")
	}
	if !strings.Contains(err.Error(), bootstrapAdmin.UUID) {
		t.Fatalf("expected the duplicate report to name the other account, got %v", err)
	}

	var reloaded User
	if err := db.First(&reloaded, "id = ?", loginAdmin.ID).Error; err != nil {
		t.Fatalf("reload login admin: %v", err)
	}
	if reloaded.Email != "admin@legacy.local" {
		t.Fatalf("expected the forked account's email to be left alone, got %q", reloaded.Email)
	}
	if got := countUsers(t, db); got != 2 {
		t.Fatalf("expected both admin rows to be preserved, got %d", got)
	}
	if links, err := ListUserIdentities(db, loginAdmin.ID); err != nil || len(links) != 0 {
		t.Fatalf("expected no identity written for the refused pin, got %d (%v)", len(links), err)
	}

	// The already-linked half of the pair still resolves to itself, so the fork
	// stays addressable instead of being folded into one account.
	resolved, created, _, err := ResolveIdentityAccount(db, IdentityAccountRequest{
		Issuer:    "https://accounts.google.com",
		Subject:   "google-sub-1111",
		Email:     "admin@example.com",
		Username:  "open-chat-go-admin",
		IsAdmin:   true,
		Provision: true,
	})
	if err != nil {
		t.Fatalf("resolve identity for the forked pair: %v", err)
	}
	if created {
		t.Fatal("expected the existing bootstrap admin to be reused")
	}
	if resolved == nil || resolved.ID != bootstrapAdmin.ID {
		t.Fatalf("expected user id %d, got %#v", bootstrapAdmin.ID, resolved)
	}
	if got := countUsers(t, db); got != 2 {
		t.Fatalf("expected no third row, got %d", got)
	}
}
