package database

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

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
