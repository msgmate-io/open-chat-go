package database

import (
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"net/mail"
	"strings"
)

type User struct {
	Model
	Name             string `json:"name"`
	Username         string `json:"username" gorm:"size:160;uniqueIndex:idx_users_username"`
	Email            string `json:"-" gorm:"size:320;uniqueIndex:idx_users_email"`
	PasswordHash     string `json:"-"`
	ContactToken     string `json:"contact_token"`
	IsAdmin          bool   `json:"is_admin"`
	IsAutomated      bool   `json:"is_automated" gorm:"default:false"`
	TwoFactorEnabled bool   `json:"two_factor_enabled" gorm:"default:false"`
	TwoFactorSecret  string `json:"-"`
}

func RandomUsername() string {
	raw := strings.ReplaceAll(uuid.NewString(), "-", "")
	if len(raw) < 12 {
		return "usr_" + raw
	}
	return "usr_" + raw[:12]
}

func EnsureUniqueRandomUsername(DB *gorm.DB) (string, error) {
	if DB == nil {
		return "", fmt.Errorf("db is required")
	}
	for attempts := 0; attempts < 10; attempts++ {
		candidate := RandomUsername()
		var count int64
		if err := DB.Model(&User{}).Where("username = ?", candidate).Count(&count).Error; err != nil {
			return "", err
		}
		if count == 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("failed to generate unique username")
}

type PublicProfile struct {
	Model
	UserId      uint            `json:"user_id" gorm:"index"`
	User        User            `json:"user" gorm:"foreignKey:UserId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	ProfileData json.RawMessage `json:"profile_data" gorm:"type:jsonb"`
}

type Contact struct {
	Model
	ContactToken  string `json:"contact_token" gorm:"index"`
	OwningUserId  uint   `json:"-" gorm:"index"`
	ContactUserId uint   `json:"-" gorm:"index"`
	OwningUser    User   `json:"-" gorm:"foreignKey:OwningUserId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	ContactUser   User   `json:"contact_user" gorm:"foreignKey:ContactUserId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
}

func (u *User) AddContact(
	DB *gorm.DB,
	user *User,
) (*Contact, error) {
	contact := Contact{
		OwningUserId:  u.ID,
		ContactUserId: user.ID,
	}

	r := DB.Create(&contact)

	if r.Error != nil {
		return nil, r.Error
	}

	return &contact, nil
}

func (u *User) AfterCreate(tx *gorm.DB) error {
	permission := Permission{UserId: u.ID, Permission: PermissionCreateAPITokens}
	if err := tx.Where("user_id = ? AND permission = ?", u.ID, PermissionCreateAPITokens).FirstOrCreate(&permission).Error; err != nil {
		return err
	}
	createBotsPermission := Permission{UserId: u.ID, Permission: PermissionCreateBots}
	if err := tx.Where("user_id = ? AND permission = ?", u.ID, PermissionCreateBots).FirstOrCreate(&createBotsPermission).Error; err != nil {
		return err
	}
	if err := EnsureAccountStateRowForUser(tx, u); err != nil {
		return err
	}
	if _, parseErr := mail.ParseAddress(strings.TrimSpace(u.Email)); parseErr == nil {
		if err := EnsureEmailVerificationIdentityVerifiedByDefault(tx, u.Email); err != nil {
			return err
		}
	}
	return EnsureDefaultAccessTokenForUser(tx, u.ID)
}

func RegisterUser(
	DB *gorm.DB,
	name string,
	email string,
	password []byte,
) (*User, error) {
	hashedPassword, err := bcrypt.GenerateFromPassword(password, bcrypt.DefaultCost)

	if err != nil {
		return nil, err
	}

	_, err = mail.ParseAddress(email)
	if err != nil {
		return nil, err
	}

	var user User = User{
		Name:             name,
		Username:         email,
		Email:            email,
		PasswordHash:     string(hashedPassword),
		TwoFactorEnabled: false,
		TwoFactorSecret:  "",
	}

	r := DB.Create(&user)

	if r.Error != nil {
		return nil, r.Error
	}

	return &user, nil
}
