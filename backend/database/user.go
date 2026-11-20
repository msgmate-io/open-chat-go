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

// maxSlugUsernameLength bounds a slug-derived username so the deterministic
// collision suffix still fits the `users.username` column (size:160).
const maxSlugUsernameLength = 100

// SlugifyUsername turns a display name into a lowercase ASCII username
// candidate: runs of characters outside [a-z0-9] collapse into a single dash and
// leading/trailing dashes are dropped. It returns "" when nothing usable is left
// (a name that is entirely non-ASCII, punctuation or whitespace), so callers can
// fall back to a generated username instead of persisting an empty one.
//
// `users.username` is a unique column, so every write path must persist a
// non-empty value (open-chat-go-ci#1149/#1158).
func SlugifyUsername(raw string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(raw)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
		} else if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
		if b.Len() >= maxSlugUsernameLength {
			break
		}
	}
	return strings.Trim(b.String(), "-")
}

// EnsureUniqueUsername derives a non-empty, currently unused username from
// preferred. The slug of preferred is used as the base and a deterministic
// numeric suffix ("-2", "-3", ...) is appended on collision; when preferred has
// no usable characters at all it falls back to a random `usr_…` username. It
// never returns "" with a nil error, which is what keeps a bot-creation write
// from tripping the `users.username` unique index on the second bot
// (open-chat-go-ci#1158).
func EnsureUniqueUsername(DB *gorm.DB, preferred string) (string, error) {
	if DB == nil {
		return "", fmt.Errorf("db is required")
	}
	base := SlugifyUsername(preferred)
	if base == "" {
		return EnsureUniqueRandomUsername(DB)
	}
	for attempt := 0; attempt < 50; attempt++ {
		candidate := base
		if attempt > 0 {
			candidate = fmt.Sprintf("%s-%d", base, attempt+1)
		}
		var count int64
		// Unscoped: the unique index also covers soft-deleted rows, so a
		// soft-deleted user still occupies its username.
		if err := DB.Unscoped().Model(&User{}).Where("username = ?", candidate).Count(&count).Error; err != nil {
			return "", err
		}
		if count == 0 {
			return candidate, nil
		}
	}
	return EnsureUniqueRandomUsername(DB)
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
	if err := EnsurePermission(tx, u.ID, PermissionCreateAPITokens); err != nil {
		return err
	}
	if err := EnsurePermission(tx, u.ID, PermissionCreateBots); err != nil {
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
