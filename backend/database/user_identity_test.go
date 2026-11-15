package database

import (
	"errors"
	"path/filepath"
	"testing"

	"gorm.io/gorm"
)

// userIdentityTestDB opens a throwaway SQLite database with every migration
// applied, so the `user_identity` table and its (issuer, subject) unique index
// are really created instead of being simulated by the model tags (ci#1111).
func userIdentityTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	return SetupTestDatabase(t, DBConfig{
		Backend:  "sqlite",
		FilePath: filepath.Join(t.TempDir(), "user_identity_test.db"),
		ResetDB:  true,
	})
}

func createIdentityTestUser(t *testing.T, DB *gorm.DB, username, email string, isAdmin bool) *User {
	t.Helper()
	user := &User{
		Name:         username,
		Username:     username,
		Email:        email,
		PasswordHash: "x",
		ContactToken: username + "-token",
		IsAdmin:      isAdmin,
	}
	if err := DB.Create(user).Error; err != nil {
		t.Fatalf("failed to create user %q: %v", username, err)
	}
	return user
}

// TestResolveUserIdentityCreatesAndReusesLink is the core slice-1 promise: the
// first login resolves through the bootstrap map and *persists* the link, so the
// second login resolves through the link itself instead of re-deriving the user
// from username/email strings.
func TestResolveUserIdentityCreatesAndReusesLink(t *testing.T) {
	DB := userIdentityTestDB(t)

	existing := createIdentityTestUser(t, DB, "alice", "alice@example.com", false)

	bootstrapCalls := 0
	bootstrap := func(tx *gorm.DB) (IdentityBootstrapResult, error) {
		bootstrapCalls++
		// The bootstrap map always reports alice, but after the link exists it
		// must never be consulted again.
		return IdentityBootstrapResult{User: existing, Created: false}, nil
	}

	req := UserIdentityRequest{Issuer: "https://id.example.com/realms/main", Subject: "sub-alice", Email: "alice@example.com", EmailVerified: true}

	first, err := ResolveUserIdentity(DB, req, IdentityResolveOptions{Bootstrap: bootstrap})
	if err != nil {
		t.Fatalf("first resolution failed: %v", err)
	}
	if first.User == nil || first.User.ID != existing.ID {
		t.Fatalf("first resolution did not resolve the bootstrap user")
	}
	if first.Source != IdentitySourceBootstrapMatched {
		t.Fatalf("expected source %q, got %q", IdentitySourceBootstrapMatched, first.Source)
	}
	if first.Identity == nil || first.Identity.ID == 0 {
		t.Fatalf("first resolution did not persist the identity link")
	}

	var linkCount int64
	if err := DB.Model(&UserIdentity{}).Count(&linkCount).Error; err != nil {
		t.Fatalf("failed to count links: %v", err)
	}
	if linkCount != 1 {
		t.Fatalf("expected exactly one persisted link, got %d", linkCount)
	}

	second, err := ResolveUserIdentity(DB, req, IdentityResolveOptions{Bootstrap: bootstrap})
	if err != nil {
		t.Fatalf("second resolution failed: %v", err)
	}
	if second.User == nil || second.User.ID != existing.ID {
		t.Fatalf("second resolution resolved a different user (id %v), account forked", second.User)
	}
	if second.Source != IdentitySourceLinked {
		t.Fatalf("expected second resolution to come from the link, got source %q", second.Source)
	}
	if bootstrapCalls != 1 {
		t.Fatalf("bootstrap map was consulted %d times, expected 1 (the link must short-circuit)", bootstrapCalls)
	}
	if err := DB.Model(&UserIdentity{}).Count(&linkCount).Error; err != nil {
		t.Fatalf("failed to count links: %v", err)
	}
	if linkCount != 1 {
		t.Fatalf("expected the link to be reused, got %d rows", linkCount)
	}
}

// TestUserIdentityIsUniquePerIssuerSubject pins the unique index: the same
// subject under two issuers is two identities, the same pair cannot be bound
// twice, and a second bind to a different user fails closed.
func TestUserIdentityIsUniquePerIssuerSubject(t *testing.T) {
	DB := userIdentityTestDB(t)

	alice := createIdentityTestUser(t, DB, "alice", "alice@example.com", false)
	bob := createIdentityTestUser(t, DB, "bob", "bob@example.com", false)

	req := UserIdentityRequest{Issuer: "https://issuer-a", Subject: "sub-1", Email: "alice@example.com", EmailVerified: true}
	if _, err := LinkUserIdentity(DB, alice, req, false); err != nil {
		t.Fatalf("linking alice failed: %v", err)
	}
	// Same subject under a different issuer is a distinct identity.
	other := UserIdentityRequest{Issuer: "https://issuer-b", Subject: "sub-1", Email: "bob@example.com", EmailVerified: true}
	if _, err := LinkUserIdentity(DB, bob, other, false); err != nil {
		t.Fatalf("linking bob under a different issuer failed: %v", err)
	}
	// Rebinding the same pair to another user is refused, never silently moved.
	if _, err := LinkUserIdentity(DB, bob, req, true); err == nil {
		t.Fatalf("expected rebinding an existing identity to a second user to fail")
	} else {
		var elsewhere *IdentityLinkedElsewhereError
		if !errors.As(err, &elsewhere) {
			t.Fatalf("expected IdentityLinkedElsewhereError, got %T: %v", err, err)
		}
	}

	user, identity, found, err := FindUserByIdentity(DB, "https://issuer-a", "sub-1")
	if err != nil || !found {
		t.Fatalf("expected to find the link, found=%v err=%v", found, err)
	}
	if user.ID != alice.ID || identity.UserId != alice.ID {
		t.Fatalf("link resolved the wrong user")
	}
}

// TestResolveUserIdentityRefusesSecondAdmin is the ci#1111 duplicate-admin stop:
// a bootstrap mapping that mints a new account while an is_admin account already
// exists is refused loudly, and the refused mint is rolled back so no twin admin
// is left behind.
func TestResolveUserIdentityRefusesSecondAdmin(t *testing.T) {
	DB := userIdentityTestDB(t)

	canonical := createIdentityTestUser(t, DB, "admin", "admin@iem.thm.de", true)

	bootstrap := func(tx *gorm.DB) (IdentityBootstrapResult, error) {
		// The old behaviour: mint a fresh admin because the bootstrap username
		// does not match the existing admin row.
		minted := &User{
			Name:         "open-chat-go-admin",
			Username:     "open-chat-go-admin",
			Email:        "open-chat-go-admin",
			PasswordHash: "x",
			ContactToken: "minted-token",
			IsAdmin:      true,
		}
		if err := tx.Create(minted).Error; err != nil {
			return IdentityBootstrapResult{}, err
		}
		return IdentityBootstrapResult{User: minted, Created: true}, nil
	}

	req := UserIdentityRequest{Issuer: "https://id.example.com/realms/main", Subject: "sub-admin", Email: "admin@iem.thm.de", EmailVerified: true}
	resolution, err := ResolveUserIdentity(DB, req, IdentityResolveOptions{Bootstrap: bootstrap})
	var conflict *AdminConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected AdminConflictError, got %v (resolution %+v)", err, resolution)
	}
	if !conflict.MintedAdmin {
		t.Fatalf("expected the conflict to be reported as a refused admin mint")
	}
	if conflict.ExistingUserId != canonical.ID || conflict.ExistingUsername != "admin" {
		t.Fatalf("conflict did not name the canonical admin row: %+v", conflict)
	}

	// The refused mint must be rolled back, not merely reported.
	var admins []User
	if err := DB.Where("is_admin = ?", true).Find(&admins).Error; err != nil {
		t.Fatalf("failed to read admins: %v", err)
	}
	if len(admins) != 1 || admins[0].ID != canonical.ID {
		t.Fatalf("expected exactly the canonical admin row, got %d admins", len(admins))
	}
	var links int64
	if err := DB.Model(&UserIdentity{}).Count(&links).Error; err != nil {
		t.Fatalf("failed to count links: %v", err)
	}
	if links != 0 {
		t.Fatalf("expected no link to be persisted for a refused mint, got %d", links)
	}
}

// TestResolveUserIdentityRefusesAdminAutoLink pins the slice-1 policy: an
// identity may be auto-linked only to a non-admin account; any link to
// is_admin = true needs the explicit operator action (slice 2).
func TestResolveUserIdentityRefusesAdminAutoLink(t *testing.T) {
	DB := userIdentityTestDB(t)

	admin := createIdentityTestUser(t, DB, "admin", "admin@iem.thm.de", true)
	bootstrap := func(tx *gorm.DB) (IdentityBootstrapResult, error) {
		return IdentityBootstrapResult{User: admin, Created: false}, nil
	}

	req := UserIdentityRequest{Issuer: "https://id.example.com/realms/main", Subject: "sub-admin", Email: "admin@iem.thm.de", EmailVerified: true}
	_, err := ResolveUserIdentity(DB, req, IdentityResolveOptions{Bootstrap: bootstrap})
	var refused *AdminLinkRefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("expected AdminLinkRefusedError for an auto-link to an admin, got %v", err)
	}

	// The verified-email fallback is held to the same rule.
	bootstrapMiss := func(tx *gorm.DB) (IdentityBootstrapResult, error) {
		return IdentityBootstrapResult{}, nil
	}
	_, err = ResolveUserIdentity(DB, req, IdentityResolveOptions{Bootstrap: bootstrapMiss})
	if !errors.As(err, &refused) {
		t.Fatalf("expected AdminLinkRefusedError for an email-match link to an admin, got %v", err)
	}

	// An explicit operator link (slice 2) is what is allowed to bind an admin.
	if _, err := LinkUserIdentity(DB, admin, req, true); err != nil {
		t.Fatalf("explicit admin link should be allowed: %v", err)
	}
}

// TestResolveUserIdentityEmailFallbackNeverCreates pins the third resolution step:
// a verified email attaches to an existing account and is persisted, while an
// unverified email never resolves at all.
func TestResolveUserIdentityEmailFallbackNeverCreates(t *testing.T) {
	DB := userIdentityTestDB(t)

	user := createIdentityTestUser(t, DB, "carol", "Carol@Example.com", false)
	noBootstrap := func(tx *gorm.DB) (IdentityBootstrapResult, error) { return IdentityBootstrapResult{}, nil }

	unverified := UserIdentityRequest{Issuer: "https://issuer", Subject: "sub-carol", Email: "carol@example.com", EmailVerified: false}
	resolution, err := ResolveUserIdentity(DB, unverified, IdentityResolveOptions{Bootstrap: noBootstrap})
	if err != nil {
		t.Fatalf("unverified resolution failed: %v", err)
	}
	if resolution.User != nil {
		t.Fatalf("an unverified email must not resolve an account")
	}

	verified := unverified
	verified.EmailVerified = true
	resolution, err = ResolveUserIdentity(DB, verified, IdentityResolveOptions{Bootstrap: noBootstrap})
	if err != nil {
		t.Fatalf("verified resolution failed: %v", err)
	}
	if resolution.User == nil || resolution.User.ID != user.ID {
		t.Fatalf("verified email did not resolve the existing account")
	}
	if resolution.Source != IdentitySourceEmailMatch {
		t.Fatalf("expected source %q, got %q", IdentitySourceEmailMatch, resolution.Source)
	}
}

// TestUserIdentityMigrationIsRegisteredAndIdempotent pins the registry entry and
// the fact that every boot re-runs the whole migration list with no version
// table (backend/database/db.go:118-135): `user_identity` must be created by the
// second pass without an error and without touching existing links.
func TestUserIdentityMigrationIsRegisteredAndIdempotent(t *testing.T) {
	registered := false
	for _, migration := range Migrations {
		table, ok := migration.(TableMigration)
		if !ok {
			continue
		}
		if _, isIdentity := table.Model.(*UserIdentity); isIdentity {
			registered = true
		}
	}
	if !registered {
		t.Fatalf("UserIdentity is not in the Migrations registry, the table would never be created")
	}

	DB := userIdentityTestDB(t)
	user := createIdentityTestUser(t, DB, "dave", "dave@example.com", false)
	req := UserIdentityRequest{Issuer: "https://issuer", Subject: "sub-dave", Email: "dave@example.com", EmailVerified: true}
	if _, err := LinkUserIdentity(DB, user, req, false); err != nil {
		t.Fatalf("linking failed: %v", err)
	}

	for _, migration := range Migrations {
		if err := migration.Migrate(DB); err != nil {
			t.Fatalf("re-running migration %T failed: %v", migration, err)
		}
	}

	var links int64
	if err := DB.Model(&UserIdentity{}).Count(&links).Error; err != nil {
		t.Fatalf("failed to count links: %v", err)
	}
	if links != 1 {
		t.Fatalf("expected the existing link to survive a re-migration, got %d rows", links)
	}
	if _, _, found, err := FindUserByIdentity(DB, "https://issuer", "sub-dave"); err != nil || !found {
		t.Fatalf("link no longer resolves after re-migration, found=%v err=%v", found, err)
	}
}

// TestResolveUserIdentityThroughBootstrapHelper wires the resolution order to
// ResolveOrCreateBootstrapUser, the helper the server bootstrap map uses: the
// admin-config run on a fresh database legitimately mints the admin, and a later
// second agent mapping must not mint a twin - it links to the canonical `admin`
// row through an operator action instead.
func TestResolveUserIdentityThroughBootstrapHelper(t *testing.T) {
	DB := userIdentityTestDB(t)

	adminMapping := func(tx *gorm.DB) (IdentityBootstrapResult, error) {
		return ResolveOrCreateBootstrapUser(tx, BootstrapUserSpec{Username: "admin", PasswordHash: "hashed", IsAdmin: true})
	}
	first, err := ResolveUserIdentity(DB, UserIdentityRequest{Issuer: "https://issuer", Subject: "sub-operator"}, IdentityResolveOptions{Bootstrap: adminMapping})
	var refused *AdminLinkRefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("expected the fresh admin mint to be refused for auto-linking, got %v", err)
	}
	if first.User != nil {
		t.Fatalf("a refused admin link must not report a resolved user")
	}

	var admins []User
	if err := DB.Where("is_admin = ?", true).Find(&admins).Error; err != nil {
		t.Fatalf("failed to read admins: %v", err)
	}
	if len(admins) != 1 || admins[0].Username != "admin" {
		t.Fatalf("expected exactly the canonical admin row to exist, got %+v", admins)
	}

	// The operator action (slice 2) is the supported way to bind an admin.
	link, err := LinkUserIdentity(DB, &admins[0], UserIdentityRequest{Issuer: "https://issuer", Subject: "sub-operator"}, true)
	if err != nil {
		t.Fatalf("explicit operator link failed: %v", err)
	}
	if link.UserId != admins[0].ID {
		t.Fatalf("operator link bound the wrong user")
	}

	again, err := ResolveUserIdentity(DB, UserIdentityRequest{Issuer: "https://issuer", Subject: "sub-operator"}, IdentityResolveOptions{Bootstrap: adminMapping})
	if err != nil {
		t.Fatalf("login after the operator link failed: %v", err)
	}
	if again.User == nil || again.User.ID != admins[0].ID || again.Source != IdentitySourceLinked {
		t.Fatalf("login after the operator link did not resolve the canonical admin")
	}

	// A second, differently-named admin mapping for a new identity is refused
	// because an is_admin row already exists.
	twinMapping := func(tx *gorm.DB) (IdentityBootstrapResult, error) {
		return ResolveOrCreateBootstrapUser(tx, BootstrapUserSpec{Username: "open-chat-go-admin", PasswordHash: "hashed", IsAdmin: true})
	}
	_, err = ResolveUserIdentity(DB, UserIdentityRequest{Issuer: "https://issuer", Subject: "sub-twin"}, IdentityResolveOptions{Bootstrap: twinMapping})
	var conflict *AdminConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected AdminConflictError for a second admin mint, got %v", err)
	}
	if conflict.ExistingUsername != "admin" {
		t.Fatalf("conflict named %q, expected the canonical admin", conflict.ExistingUsername)
	}
	var count int64
	if err := DB.Model(&User{}).Where("is_admin = ?", true).Count(&count).Error; err != nil {
		t.Fatalf("failed to count admins: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly one admin row after a refused mint, got %d", count)
	}
}

// TestResolveUserIdentityRequiresIssuerAndSubject guards the input contract; a
// partial identity must not create a wildcard link.
func TestResolveUserIdentityRequiresIssuerAndSubject(t *testing.T) {
	DB := userIdentityTestDB(t)
	if _, err := ResolveUserIdentity(DB, UserIdentityRequest{Issuer: "https://issuer"}, IdentityResolveOptions{}); !errors.Is(err, ErrIdentityIncomplete) {
		t.Fatalf("expected ErrIdentityIncomplete, got %v", err)
	}
}
