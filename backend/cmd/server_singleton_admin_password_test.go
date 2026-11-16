package cmd

import (
	"backend/database"
	"backend/server/util"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// A singleton admin provisioned by an identity-provider / bridge sign-in has an
// empty password hash (see integrationhost.usersAdapter.Create). Because the
// bootstrap pass only hashed the password when it created the row, such an
// account could never satisfy a password confirmation: the configured master
// password was locked out of every re-authenticated admin action, e.g. the raw
// config view (ci#1088).
func TestEnsureSingletonAdminUserRestoresMissingPasswordHash(t *testing.T) {
	DB := database.SetupTestDatabase(t, *setupBootstrapUserTestDB(t))

	if err, created := util.CreateUserPwPreHashed(DB, "admin", "", true); err != nil {
		t.Fatalf("failed to create passwordless admin: %v", err)
	} else if created == nil {
		t.Fatal("expected the passwordless admin to be created")
	}

	admin, err := ensureSingletonAdminUser(DB, "MasterPass1!", false)
	if err != nil {
		t.Fatalf("ensureSingletonAdminUser failed: %v", err)
	}
	if admin == nil {
		t.Fatal("expected an admin user")
	}
	if admin.PasswordHash == "" {
		t.Fatal("expected the bootstrap password to be stored for a passwordless admin")
	}
	if compareErr := bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte("MasterPass1!")); compareErr != nil {
		t.Fatalf("expected the configured master password to verify after the repair, got %v", compareErr)
	}

	// The repair must be persisted, not only returned in memory.
	var stored database.User
	if err := DB.First(&stored, "is_admin = ?", true).Error; err != nil {
		t.Fatalf("failed to reload admin: %v", err)
	}
	if compareErr := bcrypt.CompareHashAndPassword([]byte(stored.PasswordHash), []byte("MasterPass1!")); compareErr != nil {
		t.Fatalf("expected the restored password to be persisted, got %v", compareErr)
	}
}

// An admin that already has a usable password must keep it: the bootstrap pass
// only repairs an unusable hash, it never silently replaces an operator's
// password with the configured bootstrap one.
func TestEnsureSingletonAdminUserKeepsExistingPassword(t *testing.T) {
	DB := database.SetupTestDatabase(t, *setupBootstrapUserTestDB(t))

	if err, created := util.CreateUser(DB, "admin", "OperatorPass1!", true); err != nil {
		t.Fatalf("failed to create admin: %v", err)
	} else if created == nil {
		t.Fatal("expected the admin to be created")
	}
	var stored database.User
	if err := DB.First(&stored, "is_admin = ?", true).Error; err != nil {
		t.Fatalf("failed to reload admin: %v", err)
	}
	before := stored.PasswordHash
	if before == "" {
		t.Fatal("expected the created admin to have a password hash")
	}

	admin, err := ensureSingletonAdminUser(DB, "MasterPass1!", false)
	if err != nil {
		t.Fatalf("ensureSingletonAdminUser failed: %v", err)
	}
	if admin.PasswordHash != before {
		t.Fatal("expected an existing password hash to be left untouched")
	}
	if compareErr := bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte("OperatorPass1!")); compareErr != nil {
		t.Fatalf("expected the operator password to keep working, got %v", compareErr)
	}
	if compareErr := bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte("MasterPass1!")); compareErr == nil {
		t.Fatal("expected the bootstrap password not to replace an existing password")
	}
}

// unusablePasswordHash is the guard that decides whether the repair runs. It has
// to treat every value a password check can never accept as unusable and a real
// bcrypt hash as usable.
func TestUnusablePasswordHash(t *testing.T) {
	valid, err := bootstrapPasswordHash("SomePass1!")
	if err != nil {
		t.Fatalf("failed to hash password: %v", err)
	}
	cases := map[string]bool{
		"":            true,
		"   ":         true,
		"not-a-hash":  true,
		"hashed_pw12": true,
		valid:         false,
	}
	for hash, want := range cases {
		if got := unusablePasswordHash(hash); got != want {
			t.Fatalf("unusablePasswordHash(%q) = %v, want %v", hash, got, want)
		}
	}
}

// bootstrapPasswordHash must keep honouring the pre-hashed `hashed_<bcrypt>`
// form used by ROOT_CREDENTIALS / bootstrap.users.
func TestBootstrapPasswordHashPrefersPreHashedValue(t *testing.T) {
	preHashed, err := bcrypt.GenerateFromPassword([]byte("PreHashedPass1!"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("failed to hash password: %v", err)
	}
	got, err := bootstrapPasswordHash("hashed_" + string(preHashed))
	if err != nil {
		t.Fatalf("bootstrapPasswordHash failed: %v", err)
	}
	if got != string(preHashed) {
		t.Fatal("expected a pre-hashed password to be used verbatim")
	}

	hashed, err := bootstrapPasswordHash("PlainPass1!")
	if err != nil {
		t.Fatalf("bootstrapPasswordHash failed: %v", err)
	}
	if compareErr := bcrypt.CompareHashAndPassword([]byte(hashed), []byte("PlainPass1!")); compareErr != nil {
		t.Fatalf("expected a plain password to be bcrypt-hashed, got %v", compareErr)
	}
}
