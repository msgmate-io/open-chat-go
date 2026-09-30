package cmd

import (
	"backend/database"
	"testing"
)

func TestApplyUserBootstrapConfigFilesPersistsGitUsernames(t *testing.T) {
	DB := database.SetupDatabase(*setupBootstrapUserTestDB(t))

	spec := `[
		{"username":"jannis@little-world.com","email":"jannis@little-world.com","password":"StrongPass1!","git_usernames":["JannisToelle"]},
		{"username":"sean@little-world.com","email":"sean@little-world.com","password":"StrongPass1!","git_usernames":["Simba14"]}
	]`
	if err := applyUserBootstrapConfigFiles(DB, []string{spec}, false); err != nil {
		t.Fatalf("applyUserBootstrapConfigFiles failed: %v", err)
	}

	jannis, err := database.FindUserByGitUsername(DB, "jannistoelle")
	if err != nil {
		t.Fatalf("FindUserByGitUsername failed: %v", err)
	}
	if jannis == nil || jannis.Email != "jannis@little-world.com" {
		t.Fatalf("expected jannis mapped, got %#v", jannis)
	}

	sean, err := database.FindUserByGitUsername(DB, "Simba14")
	if err != nil {
		t.Fatalf("FindUserByGitUsername failed: %v", err)
	}
	if sean == nil || sean.Email != "sean@little-world.com" {
		t.Fatalf("expected sean mapped, got %#v", sean)
	}
}
