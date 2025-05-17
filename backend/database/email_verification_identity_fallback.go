package database

import (
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func normalizeIdentityEmail(email string) string {
	return strings.TrimSpace(strings.ToLower(email))
}

func EnsureEmailVerificationIdentityVerifiedByDefault(DB *gorm.DB, email string) error {
	email = normalizeIdentityEmail(email)
	if DB == nil || email == "" {
		return nil
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return nil
	}
	if !DB.Migrator().HasTable("email_verification_identities") {
		return nil
	}
	now := time.Now().UTC()
	return DB.Exec(
		"INSERT INTO email_verification_identities (uuid, created_at, updated_at, email, is_email_verified, email_verified_at) SELECT ?, ?, ?, ?, ?, ? WHERE NOT EXISTS (SELECT 1 FROM email_verification_identities WHERE email = ?)",
		uuid.NewString(),
		now,
		now,
		email,
		true,
		now,
		email,
	).Error
}

func SetEmailVerificationIdentityVerified(DB *gorm.DB, email string, verified bool) error {
	email = normalizeIdentityEmail(email)
	if DB == nil || email == "" {
		return nil
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return nil
	}
	if !DB.Migrator().HasTable("email_verification_identities") {
		return nil
	}
	if err := EnsureEmailVerificationIdentityVerifiedByDefault(DB, email); err != nil {
		return err
	}
	updates := map[string]interface{}{
		"is_email_verified": verified,
		"updated_at":        time.Now().UTC(),
	}
	if verified {
		now := time.Now().UTC()
		updates["email_verified_at"] = &now
		updates["email_verification_token_hash"] = ""
		updates["email_verification_code_hash"] = ""
		updates["email_verification_expires_at"] = nil
	} else {
		updates["email_verified_at"] = nil
	}
	return DB.Table("email_verification_identities").Where("email = ?", email).Updates(updates).Error
}
