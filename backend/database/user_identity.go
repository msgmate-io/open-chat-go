package database

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// This file implements the explicit "external identity <-> local account" link
// (ci#1111). Before it existed, the identity-provider integration re-derived the
// account from an OIDC identity on *every* login by matching username first and
// email only as an error fallback, so an alias/`+`-address/differently cased
// Google email silently forked a second (privileged) account - the two admin
// rows on ci.msgmate.io. The link table makes the mapping a first-class,
// persistent fact: `(issuer, subject) -> user_id`, unique per pair.

// IdentityLinkSource records *why* a link exists so an operator can tell a
// deliberate admin repair from an automatic login-time match.
const (
	IdentityLinkSourceBootstrap = "bootstrap"
	IdentityLinkSourceLogin     = "login"
	IdentityLinkSourceAdmin     = "admin"
	IdentityLinkSourceSelf      = "self"
)

// ErrIdentityAlreadyLinked is returned when a `(issuer, subject)` pair is
// already bound to a *different* account. Linking is never silently re-pointed.
var ErrIdentityAlreadyLinked = errors.New("this identity is already linked to another account")

// ErrIdentityLinkRequired is returned instead of provisioning a second
// privileged account: an existing account already matches the identity, so the
// caller must link it explicitly. It is the loud failure ci#1111 asks for.
var ErrIdentityLinkRequired = errors.New("an existing account matches this identity; refusing to provision a duplicate account, link the identity explicitly")

// UserIdentity binds one external OIDC identity to exactly one local account.
// A user may hold many identities and, independently, a password.
type UserIdentity struct {
	Model
	UserId      uint       `json:"user_id" gorm:"not null;index;uniqueIndex:idx_user_identity_issuer_subject"`
	User        User       `json:"-" gorm:"foreignKey:UserId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	Issuer      string     `json:"issuer" gorm:"size:320;not null;uniqueIndex:idx_user_identity_issuer_subject"`
	Subject     string     `json:"subject" gorm:"size:320;not null;uniqueIndex:idx_user_identity_issuer_subject"`
	Email       string     `json:"email" gorm:"size:320"`
	Name        string     `json:"name" gorm:"size:320"`
	Source      string     `json:"source" gorm:"size:64"`
	LastLoginAt *time.Time `json:"last_login_at"`
}

func (UserIdentity) TableName() string { return "user_identities" }

// NormalizeIdentityKey trims the issuer/subject pair. Subjects are opaque OIDC
// `sub` values and are therefore compared verbatim (never case-folded); only
// surrounding whitespace is stripped.
func NormalizeIdentityKey(issuer, subject string) (string, string) {
	return strings.TrimSpace(issuer), strings.TrimSpace(subject)
}

// NormalizeEmail lower-cases an email for the heuristic lookups only. The link
// table itself is never keyed on email.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// FindUserByIdentity resolves the account bound to `(issuer, subject)`. This is
// the primary login lookup: a linked identity always wins over any heuristic.
func FindUserByIdentity(DB *gorm.DB, issuer, subject string) (*User, *UserIdentity, error) {
	if DB == nil {
		return nil, nil, errors.New("db is required")
	}
	issuer, subject = NormalizeIdentityKey(issuer, subject)
	if issuer == "" || subject == "" {
		return nil, nil, errors.New("issuer and subject are required")
	}
	var link UserIdentity
	if err := DB.Where("issuer = ? AND subject = ?", issuer, subject).First(&link).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	var user User
	if err := DB.First(&user, "id = ?", link.UserId).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, &link, nil
		}
		return nil, &link, err
	}
	return &user, &link, nil
}

// LinkUserIdentity persists the identity -> account mapping. It is idempotent
// for the same account and refuses to re-point a pair that is already bound to
// another account (ErrIdentityAlreadyLinked).
func LinkUserIdentity(DB *gorm.DB, userID uint, issuer, subject, email, name, source string) (*UserIdentity, error) {
	if DB == nil {
		return nil, errors.New("db is required")
	}
	if userID == 0 {
		return nil, errors.New("user id is required")
	}
	issuer, subject = NormalizeIdentityKey(issuer, subject)
	if issuer == "" || subject == "" {
		return nil, errors.New("issuer and subject are required")
	}
	if source == "" {
		source = IdentityLinkSourceLogin
	}

	var existing UserIdentity
	err := DB.Unscoped().Where("issuer = ? AND subject = ?", issuer, subject).First(&existing).Error
	switch {
	case err == nil:
		if existing.DeletedAt.Valid {
			// A previously unlinked pair is re-linked to the requesting account:
			// restore the row so the unique index stays authoritative.
			if existing.UserId != userID {
				if err := DB.Unscoped().Model(&existing).Updates(map[string]interface{}{
					"user_id":       userID,
					"email":         strings.TrimSpace(email),
					"name":          strings.TrimSpace(name),
					"source":        source,
					"deleted_at":    nil,
					"last_login_at": time.Now(),
				}).Error; err != nil {
					return nil, err
				}
			} else if err := DB.Unscoped().Model(&existing).Update("deleted_at", nil).Error; err != nil {
				return nil, err
			}
			existing.DeletedAt = gorm.DeletedAt{}
			existing.UserId = userID
			existing.Source = source
			return &existing, nil
		}
		if existing.UserId != userID {
			return nil, fmt.Errorf("%w (user_id=%d)", ErrIdentityAlreadyLinked, existing.UserId)
		}
		// Same account: keep the display metadata fresh, never touch the binding.
		if err := DB.Model(&existing).Updates(map[string]interface{}{
			"email": strings.TrimSpace(email),
			"name":  strings.TrimSpace(name),
		}).Error; err != nil {
			return nil, err
		}
		return &existing, nil
	default:
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}

	link := UserIdentity{
		UserId:  userID,
		Issuer:  issuer,
		Subject: subject,
		Email:   strings.TrimSpace(email),
		Name:    strings.TrimSpace(name),
		Source:  source,
	}
	if err := DB.Create(&link).Error; err != nil {
		// A concurrent login may have won the race: re-read and compare owners
		// so the loser reports a link conflict instead of a bare driver error.
		if again := DB.Where("issuer = ? AND subject = ?", issuer, subject).First(&existing); again.Error == nil {
			if existing.UserId != userID {
				return nil, fmt.Errorf("%w (user_id=%d)", ErrIdentityAlreadyLinked, existing.UserId)
			}
			return &existing, nil
		}
		return nil, err
	}
	return &link, nil
}

// ListUserIdentities returns the identities linked to an account, newest last.
func ListUserIdentities(DB *gorm.DB, userID uint) ([]UserIdentity, error) {
	if DB == nil {
		return nil, errors.New("db is required")
	}
	var links []UserIdentity
	if err := DB.Where("user_id = ?", userID).Order("created_at asc").Find(&links).Error; err != nil {
		return nil, err
	}
	return links, nil
}

// UnlinkUserIdentity hard-deletes a link so the `(issuer, subject)` pair becomes
// available again. Soft-deleting would keep occupying the unique index and make
// a later, deliberate re-link impossible.
func UnlinkUserIdentity(DB *gorm.DB, userID uint, identityUUID string) error {
	if DB == nil {
		return errors.New("db is required")
	}
	identityUUID = strings.TrimSpace(identityUUID)
	if userID == 0 || identityUUID == "" {
		return errors.New("user id and identity uuid are required")
	}
	var link UserIdentity
	if err := DB.Unscoped().Where("uuid = ? AND user_id = ?", identityUUID, userID).First(&link).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return gorm.ErrRecordNotFound
		}
		return err
	}
	return DB.Unscoped().Delete(&UserIdentity{}, link.ID).Error
}

// TouchIdentityLastLogin records a successful sign-in through the identity.
func TouchIdentityLastLogin(DB *gorm.DB, identityID uint, when time.Time) error {
	if DB == nil || identityID == 0 {
		return nil
	}
	return DB.Model(&UserIdentity{}).Where("id = ?", identityID).Update("last_login_at", when).Error
}

// IdentityAccountRequest describes the identity-provider login/provisioning
// intent: which external identity signed in and what the bootstrap map says it
// should look like.
type IdentityAccountRequest struct {
	Issuer   string
	Subject  string
	Email    string
	Username string
	Name     string
	IsAdmin  bool
	// Provision allows creating a *new* account when nothing matches. When it is
	// false an unmatched identity simply yields (nil, false, nil) so the caller
	// can reject it with its own message.
	Provision bool
}

// ResolveIdentityAccount is the link-first resolution the identity-provider
// integration should call instead of matching username/email itself:
//
//  1. an existing `(issuer, subject)` link wins outright;
//  2. otherwise an existing account is matched by username, then by normalised
//     email, and the identity is *persisted* against it;
//  3. only when nothing matches is a new account provisioned - and for an admin
//     identity that can never happen silently while a plausible admin account
//     already exists (ErrIdentityLinkRequired).
//
// `created` reports whether a new row was provisioned; `linked` is set when a
// link was written during this call.
func ResolveIdentityAccount(DB *gorm.DB, req IdentityAccountRequest) (user *User, created bool, linked bool, err error) {
	if DB == nil {
		return nil, false, false, errors.New("db is required")
	}
	issuer, subject := NormalizeIdentityKey(req.Issuer, req.Subject)
	if issuer == "" || subject == "" {
		return nil, false, false, errors.New("issuer and subject are required")
	}

	if existing, _, err := FindUserByIdentity(DB, issuer, subject); err != nil {
		return nil, false, false, err
	} else if existing != nil {
		return existing, false, false, nil
	}

	if match, err := findAccountForIdentity(DB, req.Username, req.Email); err != nil {
		return nil, false, false, err
	} else if match != nil {
		if _, err := LinkUserIdentity(DB, match.ID, issuer, subject, req.Email, req.Name, IdentityLinkSourceLogin); err != nil {
			return nil, false, false, err
		}
		return match, false, true, nil
	}

	if req.IsAdmin {
		if duplicate, err := findPlausibleAdminDuplicate(DB, req.Username, req.Email); err != nil {
			return nil, false, false, err
		} else if duplicate != nil {
			return nil, false, false, fmt.Errorf("%w (candidate user uuids: %s)", ErrIdentityLinkRequired, duplicate.UUID)
		}
	}

	if !req.Provision {
		return nil, false, false, nil
	}

	user, err = provisionIdentityAccount(DB, req)
	if err != nil {
		return nil, false, false, err
	}
	if _, err := LinkUserIdentity(DB, user.ID, issuer, subject, req.Email, req.Name, IdentityLinkSourceBootstrap); err != nil {
		return nil, false, false, err
	}
	return user, true, true, nil
}

// findAccountForIdentity is the one-time initial-link heuristic. Username is
// exact; email is compared normalised (trimmed, lower-cased).
func findAccountForIdentity(DB *gorm.DB, username, email string) (*User, error) {
	if username = strings.TrimSpace(username); username != "" {
		var user User
		err := DB.Where("username = ?", username).First(&user).Error
		if err == nil {
			return &user, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}
	if email = NormalizeEmail(email); email != "" {
		var users []User
		if err := DB.Where("lower(email) = ?", email).Limit(2).Find(&users).Error; err != nil {
			return nil, err
		}
		if len(users) == 1 {
			return &users[0], nil
		}
	}
	return nil, nil
}

// findPlausibleAdminDuplicate looks for an existing privileged account that the
// identity probably belongs to but that the exact heuristics did not match: the
// same email local part, or an email/username containing the mapping's
// username. Returning it makes the caller fail loudly instead of forking a
// second admin (the ci.msgmate.io pair).
func findPlausibleAdminDuplicate(DB *gorm.DB, username, email string) (*User, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	localPart := ""
	if at := strings.Index(email, "@"); at > 0 {
		localPart = NormalizeEmail(email[:at])
	}
	if username == "" && localPart == "" {
		return nil, nil
	}

	var admins []User
	if err := DB.Where("is_admin = ?", true).Limit(50).Find(&admins).Error; err != nil {
		return nil, err
	}
	for i := range admins {
		candidate := admins[i]
		candidateUsername := strings.ToLower(strings.TrimSpace(candidate.Username))
		candidateEmail := NormalizeEmail(candidate.Email)
		candidateLocal := ""
		if at := strings.Index(candidateEmail, "@"); at > 0 {
			candidateLocal = candidateEmail[:at]
		}
		if username != "" && candidateUsername == username {
			return &candidate, nil
		}
		if localPart != "" && candidateLocal == localPart {
			return &candidate, nil
		}
		if username != "" && candidateUsername != "" &&
			(strings.Contains(candidateUsername, username) || strings.Contains(username, candidateUsername)) {
			return &candidate, nil
		}
		if localPart != "" && candidateLocal != "" &&
			(strings.Contains(candidateLocal, localPart) || strings.Contains(localPart, candidateLocal)) {
			return &candidate, nil
		}
	}
	return nil, nil
}

// provisionIdentityAccount creates an OIDC-provisioned account. It deliberately
// starts *without* a password: the account is reachable through the linked
// identity, and the owner can attach a password later through the authenticated
// set-password endpoint (that is what makes "SSO user sets a password" work
// without a random, unusable bcrypt hash).
func provisionIdentityAccount(DB *gorm.DB, req IdentityAccountRequest) (*User, error) {
	username := strings.TrimSpace(req.Username)
	if username == "" && NormalizeEmail(req.Email) != "" {
		username = NormalizeEmail(req.Email)
	}
	if username == "" {
		var err error
		if username, err = EnsureUniqueRandomUsername(DB); err != nil {
			return nil, err
		}
	}
	var taken int64
	if err := DB.Model(&User{}).Where("username = ?", username).Count(&taken).Error; err != nil {
		return nil, err
	}
	if taken > 0 {
		var err error
		if username, err = EnsureUniqueRandomUsername(DB); err != nil {
			return nil, err
		}
	}

	email := NormalizeEmail(req.Email)
	if email != "" {
		var emailTaken int64
		if err := DB.Model(&User{}).Where("lower(email) = ?", email).Count(&emailTaken).Error; err != nil {
			return nil, err
		}
		if emailTaken > 0 {
			return nil, fmt.Errorf("%w (email already in use)", ErrIdentityLinkRequired)
		}
	}

	user := User{
		Name:             strings.TrimSpace(req.Name),
		Username:         username,
		Email:            email,
		PasswordHash:     "",
		IsAdmin:          req.IsAdmin,
		TwoFactorEnabled: false,
	}
	if err := DB.Create(&user).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

// AccountHasPassword reports whether the account can authenticate with a
// password. Account creation path no longer decides this; the presence of a
// hash does.
func AccountHasPassword(user *User) bool {
	return user != nil && strings.TrimSpace(user.PasswordHash) != ""
}

// MinAccountPasswordLength is the minimum accepted password length for the
// set-password endpoint.
const MinAccountPasswordLength = 8

// ErrAccountHasNoPassword is returned when password login is attempted on an
// account that only has linked identities.
var ErrAccountHasNoPassword = errors.New("this account has no password configured; sign in with a linked single-sign-on identity or set a password first")

// SetUserPassword attaches or replaces the account password. It does not check
// the previous password - authorization is the caller's job (authenticated
// endpoint, current-password check or admin action).
func SetUserPassword(DB *gorm.DB, userID uint, password string) error {
	if DB == nil {
		return errors.New("db is required")
	}
	if userID == 0 {
		return errors.New("user id is required")
	}
	if len(password) < MinAccountPasswordLength {
		return fmt.Errorf("password must be at least %d characters", MinAccountPasswordLength)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return DB.Model(&User{}).Where("id = ?", userID).Update("password_hash", string(hash)).Error
}

// VerifyUserPassword checks a password against the stored hash and reports
// ErrAccountHasNoPassword for identity-only accounts.
func VerifyUserPassword(user *User, password string) error {
	if !AccountHasPassword(user) {
		return ErrAccountHasNoPassword
	}
	return bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password))
}
