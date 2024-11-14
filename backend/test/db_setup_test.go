package test

import (
	"backend/database"
	"testing"
)

func TestSetupDatabase_SQLite(t *testing.T) {
	db := database.SetupDatabase("sqlite", "data.db", true)
	var user database.User

	db.First(&user, "email = ?", "tim+test@timschupp.de")
}
