package database

import (
	"backend/runtimecfg"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const RequireEmailVerificationEnvKey = "OCI_REQUIRE_EMAIL_VERIFICATION"

func RequireEmailVerificationFromRuntimeConfig() bool {
	v, ok := runtimecfg.GetAll()[RequireEmailVerificationEnvKey]
	if !ok {
		return true
	}
	required, err := strconv.ParseBool(strings.TrimSpace(v.Value))
	if err != nil {
		return true
	}
	return required
}

func EnsureAccountStateRowForUser(DB *gorm.DB, user *User) error {
	if DB == nil || user == nil || user.ID == 0 {
		return nil
	}
	if !DB.Migrator().HasTable("account_states") {
		return nil
	}

	signupDate := user.CreatedAt
	if signupDate.IsZero() {
		signupDate = time.Now().UTC()
	}

	return DB.Exec(
		"INSERT INTO account_states (uuid, created_at, updated_at, user_id, signup_date, is_email_verified) SELECT ?, ?, ?, ?, ?, ? WHERE NOT EXISTS (SELECT 1 FROM account_states WHERE user_id = ?)",
		uuid.NewString(),
		signupDate,
		signupDate,
		user.ID,
		signupDate,
		true,
		user.ID,
	).Error
}

func IsUserEmailVerified(DB *gorm.DB, userID uint) (bool, error) {
	if DB == nil || userID == 0 {
		return true, nil
	}
	if !DB.Migrator().HasTable("account_states") {
		return true, nil
	}

	var row struct {
		IsEmailVerified bool `gorm:"column:is_email_verified"`
	}
	err := DB.Table("account_states").Select("is_email_verified").Where("user_id = ?", userID).Take(&row).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return true, nil
		}
		return false, err
	}
	return row.IsEmailVerified, nil
}

func SetUserEmailVerified(DB *gorm.DB, userID uint, verified bool) error {
	if DB == nil || userID == 0 {
		return nil
	}
	if !DB.Migrator().HasTable("account_states") {
		return nil
	}

	updates := map[string]interface{}{
		"is_email_verified": verified,
		"updated_at":        time.Now().UTC(),
	}
	if verified {
		now := time.Now().UTC()
		updates["email_verified_at"] = now
		updates["email_verification_token_hash"] = ""
		updates["email_verification_code_hash"] = ""
		updates["email_verification_expires_at"] = nil
	} else {
		updates["email_verified_at"] = nil
	}
	return DB.Table("account_states").Where("user_id = ?", userID).Updates(updates).Error
}
