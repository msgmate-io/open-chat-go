package database

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"gorm.io/gorm"
)

func GenerateRawAccessToken() (raw string, prefix string, hash string, err error) {
	buf := make([]byte, 24)
	if _, err = rand.Read(buf); err != nil {
		return "", "", "", err
	}
	raw = "ocat_" + hex.EncodeToString(buf)
	prefix = raw
	if len(prefix) > 18 {
		prefix = prefix[:18]
	}
	sum := sha256.Sum256([]byte(raw))
	hash = hex.EncodeToString(sum[:])
	return raw, prefix, hash, nil
}

func EnsureDefaultAccessTokenForUser(tx *gorm.DB, userID uint) error {
	var count int64
	if err := tx.Model(&AccessToken{}).Where("user_id = ?", userID).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	_, prefix, hash, err := GenerateRawAccessToken()
	if err != nil {
		return fmt.Errorf("failed generating default token: %w", err)
	}

	defaultToken := AccessToken{
		UserId:      userID,
		Name:        "Default API token",
		TokenPrefix: prefix,
		TokenHash:   hash,
	}
	return tx.Create(&defaultToken).Error
}

// RevokeChildAccessTokens revokes all tokens derived from the given parent
// token. Child tokens are also validated against their parent at resolve
// time; this cascade makes revocation explicit and auditable.
func RevokeChildAccessTokens(tx *gorm.DB, parentTokenID uint) error {
	now := time.Now()
	return tx.Model(&AccessToken{}).
		Where("parent_token_id = ? AND revoked_at IS NULL", parentTokenID).
		Update("revoked_at", &now).Error
}

// CleanupExpiredBrowserAccessTokens removes expired browser-audience tokens
// for a user. Best effort housekeeping; expiry is enforced at resolve time
// regardless.
func CleanupExpiredBrowserAccessTokens(tx *gorm.DB, userID uint) {
	now := time.Now()
	tx.Where("user_id = ? AND audience = ? AND expires_at IS NOT NULL AND expires_at < ?", userID, AudienceBrowserAPI, now).
		Delete(&AccessToken{})
}

// EnforceActiveBrowserTokenLimit keeps the number of active (non-revoked,
// non-expired) browser-audience tokens for a user at or below limit by
// evicting the oldest ones. Browser tokens are ephemeral session credentials
// and are intentionally rotated: issuing new browser tokens must never fail
// or exhaust the regular API token quota, so surplus tokens are hard deleted
// like expired browser tokens. Best effort housekeeping.
func EnforceActiveBrowserTokenLimit(tx *gorm.DB, userID uint, limit int) {
	if tx == nil || limit <= 0 {
		return
	}
	now := time.Now()
	activeIDs := []uint{}
	if err := tx.Model(&AccessToken{}).
		Where("user_id = ? AND audience = ? AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)", userID, AudienceBrowserAPI, now).
		Order("created_at ASC, id ASC").
		Pluck("id", &activeIDs).Error; err != nil {
		return
	}
	if len(activeIDs) <= limit {
		return
	}
	evictIDs := activeIDs[:len(activeIDs)-limit]
	tx.Where("id IN ? AND audience = ?", evictIDs, AudienceBrowserAPI).Delete(&AccessToken{})
}
