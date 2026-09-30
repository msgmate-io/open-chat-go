package database

import (
	"path/filepath"
	"testing"
)

func setupGitUsernameDB(t *testing.T) *DBConfig {
	t.Helper()
	return &DBConfig{
		Backend:  "sqlite",
		FilePath: filepath.Join(t.TempDir(), "git_usernames_test.db"),
		Debug:    false,
		ResetDB:  true,
	}
}

func TestNormalizeGitUsernames(t *testing.T) {
	got := NormalizeGitUsernames([]string{" Simba14 ", "@JannisToelle", "simba14", "", "TBScode"})
	want := []string{"jannistoelle", "simba14", "tbscode"}
	if len(got) != len(want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	}
}

func TestFindUserByGitUsername(t *testing.T) {
	DB := SetupDatabase(*setupGitUsernameDB(t))

	user, err := RegisterUser(DB, "jannis", "jannis@little-world.com", []byte("Passw0rd!"))
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	if err := SetUserGitUsernames(DB, user.ID, []string{"JannisToelle"}); err != nil {
		t.Fatalf("failed to set git usernames: %v", err)
	}

	found, err := FindUserByGitUsername(DB, "@jannistoelle")
	if err != nil {
		t.Fatalf("FindUserByGitUsername failed: %v", err)
	}
	if found == nil || found.ID != user.ID {
		t.Fatalf("expected to resolve user %d, got %#v", user.ID, found)
	}

	missing, err := FindUserByGitUsername(DB, "simba14")
	if err != nil {
		t.Fatalf("FindUserByGitUsername failed: %v", err)
	}
	if missing != nil {
		t.Fatalf("expected no user for unmapped login, got %#v", missing)
	}
}
