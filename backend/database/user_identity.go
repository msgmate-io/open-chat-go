package database

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// UserIdentity is the durable link between one external identity (an OIDC
// issuer + subject pair) and one local user row (ci#1111).
//
// Why it exists: the login path used to re-derive "which local user is this?"
// from strings on every login - the identity bootstrap map, or the
// `Email == username` heuristic that `util.CreateUser`/`util.CreateUserPwPreHashed`
// mint (backend/server/util/util.go:99,143) and that `ensureBootstrapUser`
// overwrites afterwards (backend/cmd/server.go:763-770). Two login methods for
// one human (Google + password) therefore forked into two accounts. The link row
// makes the answer explicit and stable: resolve through it first, and persist it
// the first time an identity resolves, so the next login cannot drift.
//
// The table is `user_identity` (pinned in TableName so gorm's pluralization
// cannot rename it under us); (issuer, subject) is unique, so the database -
// not application code - refuses to bind one identity to two users.
type UserIdentity struct {
	Model
	// UserId is the local account this identity belongs to.
	UserId uint `json:"-" gorm:"not null;index"`
	User   User `json:"-" gorm:"foreignKey:UserId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	// Issuer is the OIDC issuer the identity came from, eg a Keycloak realm URL.
	Issuer string `json:"issuer" gorm:"type:varchar(320);not null;uniqueIndex:idx_user_identity_issuer_subject"`
	// Subject is the issuer-scoped stable subject claim ("sub").
	Subject string `json:"subject" gorm:"type:varchar(320);not null;uniqueIndex:idx_user_identity_issuer_subject"`
	// Email is the last email claim seen for this identity. It is informational:
	// it is never used to re-derive the owning user on a later login.
	Email string `json:"-" gorm:"type:varchar(320);not null;default:''"`
	// EmailVerified records whether the email claim was verified when the link
	// was created. The email fallback resolution only trusts a verified claim.
	EmailVerified bool `json:"email_verified" gorm:"not null;default:false"`
	// LastLoginAt is touched on every successful resolution through this link.
	LastLoginAt *time.Time `json:"last_login_at"`
}

// TableName pins the link table name.
func (UserIdentity) TableName() string {
	return "user_identity"
}

// UserIdentityRequest is the identity a login just authenticated.
type UserIdentityRequest struct {
	// Issuer is the OIDC issuer (or another stable provider identity).
	Issuer string
	// Subject is the provider's stable, issuer-scoped user id ("sub").
	Subject string
	// Email is the email claim, used only by the documented fallbacks.
	Email string
	// EmailVerified must be true for the email fallback to be considered.
	EmailVerified bool
}

// IdentitySource names which step of the resolution order produced the user.
type IdentitySource string

const (
	// IdentitySourceLinked means the durable link resolved the user.
	IdentitySourceLinked IdentitySource = "linked"
	// IdentitySourceBootstrapCreated means the bootstrap mapping minted the user.
	IdentitySourceBootstrapCreated IdentitySource = "bootstrap_created"
	// IdentitySourceBootstrapMatched means the bootstrap mapping resolved an
	// existing user.
	IdentitySourceBootstrapMatched IdentitySource = "bootstrap_matched"
	// IdentitySourceEmailMatch means the verified-email fallback resolved an
	// existing user (it never creates one).
	IdentitySourceEmailMatch IdentitySource = "email_match"
)

// IdentityBootstrapResult is what a bootstrap mapping reports back: the user it
// resolved or minted, and whether this call created the row. `Created` is what
// arms the duplicate-admin guard, so a resolver that mints a user must report it
// truthfully.
type IdentityBootstrapResult struct {
	User    *User
	Created bool
}

// IdentityResolveOptions carries the caller's existing resolution behaviour plus
// the admin-linking policy.
type IdentityResolveOptions struct {
	// Bootstrap is the existing bootstrap map lookup, run against the resolution
	// transaction so a refused mint is rolled back instead of left behind.
	Bootstrap func(tx *gorm.DB) (IdentityBootstrapResult, error)
	// AllowAdminLink enables linking an identity to an `is_admin = true` row.
	// Auto-link leaves it false: binding an identity to an admin is an explicit
	// operator action (the slice-2 `oci admin identity link` command), never a
	// side effect of a login.
	AllowAdminLink bool
}

// IdentityResolution is the outcome of ResolveUserIdentity.
type IdentityResolution struct {
	// User is the resolved account, nil when no mapping matched and the caller
	// has to create a new user before linking it.
	User *User
	// Identity is the persisted link. It is nil when User is nil.
	Identity *UserIdentity
	// Source names the step that resolved the user.
	Source IdentitySource
}

// ErrIdentityIncomplete is returned when the request does not carry both an
// issuer and a subject.
var ErrIdentityIncomplete = errors.New("identity request requires a non-empty issuer and subject")

// IdentityLinkedElsewhereError reports that the identity is already bound to a
// different user than the one the mapping proposed. It is never retried by
// silently rebinding.
type IdentityLinkedElsewhereError struct {
	Issuer  string
	Subject string
	UserId  uint
}

func (e *IdentityLinkedElsewhereError) Error() string {
	return fmt.Sprintf("identity %s|%s is already linked to user id %d; refusing to rebind it", e.Issuer, e.Subject, e.UserId)
}

// AdminLinkRefusedError reports that resolving an identity would have bound it
// to an `is_admin = true` account without an explicit operator action.
type AdminLinkRefusedError struct {
	Issuer   string
	Subject  string
	Username string
	Email    string
}

func (e *AdminLinkRefusedError) Error() string {
	return fmt.Sprintf(
		"refusing to auto-link identity %s|%s to admin account %q (email %q): linking an identity to an is_admin account requires the explicit operator action `oci admin identity link`",
		e.Issuer, e.Subject, e.Username, e.Email,
	)
}

// AdminConflictError reports that a bootstrap mapping was about to create a new
// account while an existing `is_admin` account plausibly matches. It is a loud
// stop, not a warning: minting a second admin is the ci#1111 account fork.
type AdminConflictError struct {
	Issuer  string
	Subject string
	Email   string
	// ExistingUsername is the canonical admin row that already owns the match.
	ExistingUsername string
	// ExistingUserId is its id.
	ExistingUserId uint
	// MintedAdmin is true when the mapping tried to create a second admin row.
	MintedAdmin bool
}

func (e *AdminConflictError) Error() string {
	if e.MintedAdmin {
		return fmt.Sprintf(
			"refusing to create a second admin account for identity %s|%s: the existing is_admin account %q (id %d) is canonical (it owns the reserved `admin` username); link the identity to it with `oci admin identity link` instead of minting a twin",
			e.Issuer, e.Subject, e.ExistingUsername, e.ExistingUserId,
		)
	}
	return fmt.Sprintf(
		"refusing to create a new account for identity %s|%s: the existing is_admin account %q (id %d) plausibly matches (verified email %q); link the identity to it with `oci admin identity link` instead of minting a second account",
		e.Issuer, e.Subject, e.ExistingUsername, e.ExistingUserId, e.Email,
	)
}

// ResolveUserIdentity maps an authenticated external identity onto a local user,
// in the order the ci#1111 plan fixes:
//
//  1. the durable link (`user_identity` by issuer+subject) - no re-derivation;
//  2. the existing bootstrap map (opts.Bootstrap), which may create a user;
//  3. a verified-email match against `users.email`, which never creates.
//
// Once resolved the link is persisted, so the same identity resolves to the same
// user on every later login no matter what the bootstrap map or the email
// heuristics would say. When nothing matches, User is nil and the caller creates
// the account and links it with LinkUserIdentity.
//
// Resolution and linking are two transactions on purpose:
//   - the resolution transaction rolls a refused bootstrap mint back, so the
//     duplicate-admin guard leaves no twin admin and no orphan account behind;
//   - the link transaction then fails closed on its own, so an admin auto-link
//     refusal reports the error without destroying the (canonical) account the
//     operator still has to bind with the explicit slice-2 command.
func ResolveUserIdentity(DB *gorm.DB, req UserIdentityRequest, opts IdentityResolveOptions) (IdentityResolution, error) {
	if DB == nil {
		return IdentityResolution{}, fmt.Errorf("database is required")
	}
	req, err := normalizeIdentityRequest(req)
	if err != nil {
		return IdentityResolution{}, err
	}

	// Step 1, outside the transaction: the cheap read that carries almost every
	// login, since a linked identity never touches the heuristics again.
	if resolution, found, err := resolveThroughLink(DB, req); err != nil || found {
		return resolution, err
	}

	var (
		candidate *User
		source    IdentitySource
	)
	err = DB.Transaction(func(tx *gorm.DB) error {
		// Re-read inside the transaction so two concurrent first logins race on
		// the unique index instead of both minting.
		if _, found, err := resolveThroughLink(tx, req); err != nil {
			return err
		} else if found {
			// Another request linked the identity first; step 1 resolves it.
			return nil
		}

		// Step 2: the existing bootstrap map.
		if opts.Bootstrap != nil {
			result, err := opts.Bootstrap(tx)
			if err != nil {
				return err
			}
			if result.User != nil && result.User.ID != 0 {
				if result.Created {
					if err := guardBootstrapCreatedUser(tx, req, result.User); err != nil {
						return err
					}
					source = IdentitySourceBootstrapCreated
				} else {
					source = IdentitySourceBootstrapMatched
				}
				candidate = result.User
				return nil
			}
		}

		// Step 3: verified-email fallback. It attaches to an existing account and
		// never creates one, so it cannot fork an account on its own.
		if req.EmailVerified && req.Email != "" {
			var user User
			err := tx.Where("LOWER(email) = ?", strings.ToLower(req.Email)).First(&user).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if err == nil {
				candidate = &user
				source = IdentitySourceEmailMatch
			}
		}

		// Unresolved: no bootstrap match and no email match. The caller mints the
		// account and calls LinkUserIdentity.
		return nil
	})
	if err != nil {
		return IdentityResolution{}, err
	}

	if candidate == nil {
		// Either nothing matched, or the identity was linked by a concurrent
		// request - resolve that through the link.
		if resolution, found, err := resolveThroughLink(DB, req); err != nil || found {
			return resolution, err
		}
		return IdentityResolution{}, nil
	}

	link, err := LinkUserIdentity(DB, candidate, req, opts.AllowAdminLink)
	if err != nil {
		return IdentityResolution{}, err
	}
	return IdentityResolution{User: candidate, Identity: link, Source: source}, nil
}

// LinkUserIdentity persists the link for a user the caller just created (or
// explicitly chose). It is the creation path that pairs with an unresolved
// ResolveUserIdentity, and the entry point the slice-2 operator command uses with
// AllowAdminLink set.
func LinkUserIdentity(DB *gorm.DB, user *User, req UserIdentityRequest, allowAdminLink bool) (*UserIdentity, error) {
	if DB == nil {
		return nil, fmt.Errorf("database is required")
	}
	if user == nil || user.ID == 0 {
		return nil, fmt.Errorf("a persisted user is required to link an identity")
	}
	req, err := normalizeIdentityRequest(req)
	if err != nil {
		return nil, err
	}

	var link *UserIdentity
	err = DB.Transaction(func(tx *gorm.DB) error {
		if existing, found, err := resolveThroughLink(tx, req); err != nil {
			return err
		} else if found {
			if existing.User != nil && existing.User.ID != user.ID {
				return &IdentityLinkedElsewhereError{Issuer: req.Issuer, Subject: req.Subject, UserId: existing.User.ID}
			}
			link = existing.Identity
			return nil
		}
		persisted, err := persistIdentityLink(tx, req, user, allowAdminLink)
		if err != nil {
			return err
		}
		link = persisted
		return nil
	})
	if err != nil {
		return nil, err
	}
	return link, nil
}

// BootstrapUserSpec describes the account a bootstrap mapping names.
type BootstrapUserSpec struct {
	// Username is the bootstrap username, also the default email (see
	// util.CreateUser: Email is minted as the username, which is exactly the
	// drift ci#1111 has to stop re-deriving from).
	Username string
	// Email overrides the minted email when the operator configures one
	// (backend/cmd/server.go:763-770 does this as a post-create overwrite).
	Email string
	// PasswordHash is an already-hashed password; empty leaves the hash unset.
	PasswordHash string
	// IsAdmin marks the account as an admin.
	IsAdmin bool
}

// ResolveOrCreateBootstrapUser is the bootstrap-map step of the resolution
// order, expressed so it can report `Created` truthfully.
//
// The legacy helpers cannot: util.CreateUser / util.CreateUserPwPreHashed
// (backend/server/util/util.go:88,123) return the existing row and a nil error
// when the username or email is already taken, so a caller cannot tell a fresh
// mint from a match - which is precisely why a duplicate admin could slip
// through. This helper runs the same lookup, creates when nothing matched, and
// says which happened so the duplicate-admin guard can arm.
func ResolveOrCreateBootstrapUser(tx *gorm.DB, spec BootstrapUserSpec) (IdentityBootstrapResult, error) {
	if tx == nil {
		return IdentityBootstrapResult{}, fmt.Errorf("database is required")
	}
	username := strings.TrimSpace(spec.Username)
	if username == "" {
		return IdentityBootstrapResult{}, fmt.Errorf("bootstrap username is required")
	}

	var existing User
	err := tx.Where("email = ? OR username = ?", username, username).First(&existing).Error
	if err == nil {
		return IdentityBootstrapResult{User: &existing, Created: false}, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return IdentityBootstrapResult{}, fmt.Errorf("error reading bootstrap user %q: %w", username, err)
	}

	email := strings.TrimSpace(spec.Email)
	if email == "" {
		email = username
	}
	created := User{
		Name:         username,
		Username:     username,
		Email:        email,
		PasswordHash: spec.PasswordHash,
		ContactToken: uuid.NewString(),
		IsAdmin:      spec.IsAdmin,
	}
	if err := tx.Create(&created).Error; err != nil {
		return IdentityBootstrapResult{}, fmt.Errorf("error writing bootstrap user %q: %w", username, err)
	}
	return IdentityBootstrapResult{User: &created, Created: true}, nil
}

// FindUserByIdentity resolves an identity through the durable link only. found is
// false (with a nil error) when no link exists.
func FindUserByIdentity(DB *gorm.DB, issuer, subject string) (user *User, identity *UserIdentity, found bool, err error) {
	req, err := normalizeIdentityRequest(UserIdentityRequest{Issuer: issuer, Subject: subject})
	if err != nil {
		return nil, nil, false, err
	}
	resolution, found, err := resolveThroughLink(DB, req)
	if err != nil || !found {
		return nil, nil, false, err
	}
	return resolution.User, resolution.Identity, true, nil
}

// resolveThroughLink is step 1: the durable link, read back with its user.
func resolveThroughLink(DB *gorm.DB, req UserIdentityRequest) (IdentityResolution, bool, error) {
	var link UserIdentity
	err := DB.Where("issuer = ? AND subject = ?", req.Issuer, req.Subject).First(&link).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return IdentityResolution{}, false, nil
	}
	if err != nil {
		return IdentityResolution{}, false, err
	}

	var user User
	if err := DB.First(&user, link.UserId).Error; err != nil {
		// A link whose user is gone must not silently resolve to someone else.
		return IdentityResolution{}, false, fmt.Errorf("identity link %s|%s points at missing user id %d: %w", req.Issuer, req.Subject, link.UserId, err)
	}

	now := time.Now().UTC()
	if err := DB.Model(&UserIdentity{}).Where("id = ?", link.ID).Update("last_login_at", now).Error; err != nil {
		return IdentityResolution{}, false, err
	}
	link.LastLoginAt = &now
	return IdentityResolution{User: &user, Identity: &link, Source: IdentitySourceLinked}, true, nil
}

// guardBootstrapCreatedUser is the ci#1111 duplicate-admin stop. A bootstrap
// mapping may create accounts freely, but it may not create a SECOND admin, and
// it may not mint a fresh account for an identity whose verified email already
// belongs to an `is_admin` row - that is the plausible match that forked
// `admin` and `open-chat-go-admin` in the first place.
//
// It runs inside the resolution transaction, so refusing rolls the mint back: no
// half-created admin, no orphan link.
func guardBootstrapCreatedUser(tx *gorm.DB, req UserIdentityRequest, created *User) error {
	if created.IsAdmin {
		var canonical User
		err := tx.Where("is_admin = ? AND id <> ?", true, created.ID).Order("id").First(&canonical).Error
		if err == nil {
			return &AdminConflictError{
				Issuer:           req.Issuer,
				Subject:          req.Subject,
				Email:            req.Email,
				ExistingUsername: canonical.Username,
				ExistingUserId:   canonical.ID,
				MintedAdmin:      true,
			}
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
	}

	if req.EmailVerified && req.Email != "" {
		var matches []User
		if err := tx.Where("is_admin = ? AND LOWER(email) = ?", true, strings.ToLower(req.Email)).Limit(5).Find(&matches).Error; err != nil {
			return err
		}
		for _, match := range matches {
			if match.ID == created.ID {
				continue
			}
			return &AdminConflictError{
				Issuer:           req.Issuer,
				Subject:          req.Subject,
				Email:            req.Email,
				ExistingUsername: match.Username,
				ExistingUserId:   match.ID,
			}
		}
	}
	return nil
}

// persistIdentityLink inserts the link row, enforcing the admin policy and
// turning a unique-index violation into "already linked elsewhere" instead of a
// twin link.
func persistIdentityLink(tx *gorm.DB, req UserIdentityRequest, user *User, allowAdminLink bool) (*UserIdentity, error) {
	if user == nil || user.ID == 0 {
		return nil, fmt.Errorf("a persisted user is required to link an identity")
	}
	if user.IsAdmin && !allowAdminLink {
		return nil, &AdminLinkRefusedError{
			Issuer:   req.Issuer,
			Subject:  req.Subject,
			Username: user.Username,
			Email:    user.Email,
		}
	}

	now := time.Now().UTC()
	link := UserIdentity{
		UserId:        user.ID,
		Issuer:        req.Issuer,
		Subject:       req.Subject,
		Email:         req.Email,
		EmailVerified: req.EmailVerified,
		LastLoginAt:   &now,
	}
	if err := tx.Create(&link).Error; err != nil {
		if isUniqueViolation(err) {
			return nil, &IdentityLinkedElsewhereError{Issuer: req.Issuer, Subject: req.Subject, UserId: user.ID}
		}
		return nil, err
	}
	return &link, nil
}

var _ Migration = TableMigration{Model: &UserIdentity{}}

func normalizeIdentityRequest(req UserIdentityRequest) (UserIdentityRequest, error) {
	req.Issuer = strings.TrimSpace(req.Issuer)
	req.Subject = strings.TrimSpace(req.Subject)
	req.Email = strings.TrimSpace(req.Email)
	if req.Issuer == "" || req.Subject == "" {
		return req, ErrIdentityIncomplete
	}
	return req, nil
}

// isUniqueViolation reports whether err is a unique-index violation on either
// supported driver (SQLite in tests/dev, PostgreSQL in production).
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique constraint failed") ||
		strings.Contains(msg, "duplicate key value violates unique constraint") ||
		strings.Contains(msg, "unique constraint") && strings.Contains(msg, "violat")
}
