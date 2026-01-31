package database

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
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

// MintScopedAccessToken creates a short-lived, audience-bound access token for
// a user and returns the raw credential exactly once (only its hash is
// persisted). A non-empty audience makes the token default-deny: the server
// only accepts it on the routes allowlisted for that audience. scopes may be
// empty to grant the whole allowlist. When parentTokenID is non-zero the token
// is linked to that credential, so revoking or expiring the parent invalidates
// the child. A non-positive ttl creates a non-expiring token (used for tests).
func MintScopedAccessToken(tx *gorm.DB, userID uint, name, audience, scopes string, ttl time.Duration, parentTokenID uint) (string, AccessToken, error) {
	if tx == nil {
		return "", AccessToken{}, fmt.Errorf("database handle is nil")
	}
	if userID == 0 {
		return "", AccessToken{}, fmt.Errorf("user id is required")
	}
	trimmedAudience := strings.TrimSpace(audience)
	if trimmedAudience == "" {
		return "", AccessToken{}, fmt.Errorf("audience is required")
	}

	raw, prefix, hash, err := GenerateRawAccessToken()
	if err != nil {
		return "", AccessToken{}, fmt.Errorf("generating token: %w", err)
	}

	var expiresAt *time.Time
	if ttl > 0 {
		expiry := time.Now().Add(ttl)
		expiresAt = &expiry
	}

	token := AccessToken{
		UserId:      userID,
		Name:        strings.TrimSpace(name),
		TokenPrefix: prefix,
		TokenHash:   hash,
		ExpiresAt:   expiresAt,
		Audience:    trimmedAudience,
		Scopes:      strings.TrimSpace(scopes),
	}
	if parentTokenID != 0 {
		pid := parentTokenID
		token.ParentTokenId = &pid
	}
	if err := tx.Create(&token).Error; err != nil {
		return "", AccessToken{}, fmt.Errorf("persisting token: %w", err)
	}
	return raw, token, nil
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
