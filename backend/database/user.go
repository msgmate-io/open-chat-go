package database

import (
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"net/mail"
	"sort"
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
	// GitUsernames is a JSON array of provider logins (e.g. GitHub usernames)
	// mapped to this account. The git integration uses it to resolve which
	// Open-Chat user triggered a repository event so the dispatched coding
	// interaction is owned by (and runs as) that user.
	GitUsernames string `json:"git_usernames,omitempty" gorm:"type:text"`
}

// NormalizeGitUsernames lowercases, trims, de-duplicates and sorts provider
// logins so equality checks and lookups are stable.
func NormalizeGitUsernames(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, raw := range in {
		value := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "@")))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

// GitUsernameList decodes the stored JSON array of provider logins.
func (u *User) GitUsernameList() []string {
	if u == nil || strings.TrimSpace(u.GitUsernames) == "" {
		return []string{}
	}
	var names []string
	if err := json.Unmarshal([]byte(u.GitUsernames), &names); err != nil {
		return []string{}
	}
	return NormalizeGitUsernames(names)
}

// SetUserGitUsernames persists the normalized provider logins for a user.
func SetUserGitUsernames(DB *gorm.DB, userID uint, usernames []string) error {
	if DB == nil || userID == 0 {
		return fmt.Errorf("user is required")
	}
	normalized := NormalizeGitUsernames(usernames)
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return err
	}
	return DB.Model(&User{}).Where("id = ?", userID).Update("git_usernames", string(encoded)).Error
}

// FindUserByGitUsername returns the user that has the given provider login
// mapped to it, or (nil, nil) when no user maps it. The login is matched
// case-insensitively and an optional "@" prefix is ignored.
func FindUserByGitUsername(DB *gorm.DB, username string) (*User, error) {
	if DB == nil {
		return nil, fmt.Errorf("db is required")
	}
	target := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(username), "@")))
	if target == "" {
		return nil, nil
	}
	candidates := []User{}
	if err := DB.Where("git_usernames IS NOT NULL AND git_usernames <> ''").Find(&candidates).Error; err != nil {
		return nil, err
	}
	for i := range candidates {
		for _, name := range candidates[i].GitUsernameList() {
			if name == target {
				return &candidates[i], nil
			}
		}
	}
	return nil, nil
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
