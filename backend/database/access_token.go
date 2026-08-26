package database

import (
	"context"
	"strings"
	"time"
)

// AccessToken stores hashed API credentials for user API access.
type AccessToken struct {
	Model
	UserId      uint       `json:"-" gorm:"index"`
	User        User       `json:"-" gorm:"foreignKey:UserId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	Name        string     `json:"name" gorm:"type:varchar(120);not null"`
	TokenPrefix string     `json:"token_prefix" gorm:"type:varchar(40);not null;index"`
	TokenHash   string     `json:"-" gorm:"type:char(64);not null;uniqueIndex"`
	LastUsedAt  *time.Time `json:"last_used_at" gorm:"default:null"`
	ExpiresAt   *time.Time `json:"expires_at" gorm:"default:null"`
	RevokedAt   *time.Time `json:"revoked_at" gorm:"default:null;index"`
	// Audience restricts a token to a specific API audience. Empty means the
	// token is a general-purpose credential valid on every authenticated API.
	// Non-empty audiences are default-deny: only explicitly allowlisted routes
	// accept them.
	Audience string `json:"audience,omitempty" gorm:"type:varchar(60);not null;default:'';index"`
	// Scopes is a comma separated list of scopes granted to the token. Empty
	// means unrestricted (legacy behavior). Only meaningful for restricted
	// audiences.
	Scopes string `json:"scopes,omitempty" gorm:"type:varchar(255);not null;default:''"`
	// ParentTokenId links a derived (e.g. browser) token to the credential it
	// was exchanged from. Revocation or expiry of the parent immediately
	// invalidates the child.
	ParentTokenId *uint `json:"-" gorm:"index"`
}

const (
	// AudienceBrowserAPI marks short-lived tokens minted for browser/API
	// clients embedded in third party frontends.
	AudienceBrowserAPI = "browser-api"

	ScopeBotsRead         = "bots:read"
	ScopeBotsWrite        = "bots:write"
	ScopeInteractionsList = "interactions:list"
	ScopeInteractionsRead = "interactions:read"
)

// ValidTokenScopes is the set of scopes that can be granted to restricted
// tokens.
var ValidTokenScopes = map[string]struct{}{
	ScopeBotsRead:         {},
	ScopeBotsWrite:        {},
	ScopeInteractionsList: {},
	ScopeInteractionsRead: {},
}

func IsValidTokenScope(scope string) bool {
	_, ok := ValidTokenScopes[strings.TrimSpace(scope)]
	return ok
}

// ParseTokenScopes splits a stored scope string into individual scopes.
func ParseTokenScopes(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	parts := strings.Split(trimmed, ",")
	scopes := make([]string, 0, len(parts))
	for _, part := range parts {
		if scope := strings.TrimSpace(part); scope != "" {
			scopes = append(scopes, scope)
		}
	}
	return scopes
}

// JoinTokenScopes serializes scopes for storage.
func JoinTokenScopes(scopes []string) string {
	return strings.Join(scopes, ",")
}

// IsRestricted reports whether the token is restricted by audience or scopes.
func (t *AccessToken) IsRestricted() bool {
	if t == nil {
		return false
	}
	return strings.TrimSpace(t.Audience) != "" || strings.TrimSpace(t.Scopes) != ""
}

// HasScope reports whether the token grants the given scope. Unrestricted
// tokens (empty scope list) grant everything.
func (t *AccessToken) HasScope(scope string) bool {
	if t == nil {
		return false
	}
	scopes := ParseTokenScopes(t.Scopes)
	if len(scopes) == 0 {
		return true
	}
	for _, granted := range scopes {
		if granted == scope {
			return true
		}
	}
	return false
}

// ScopesSubset reports whether requested scopes are a subset of the parent
// scopes. An empty parent scope list represents full authority.
func ScopesSubset(parentScopes, requestedScopes []string) bool {
	if len(parentScopes) == 0 {
		return true
	}
	granted := make(map[string]struct{}, len(parentScopes))
	for _, scope := range parentScopes {
		granted[scope] = struct{}{}
	}
	for _, scope := range requestedScopes {
		if _, ok := granted[scope]; !ok {
			return false
		}
	}
	return true
}

type accessTokenContextKeyType string

const AccessTokenContextKey accessTokenContextKeyType = "access_token"

func ContextWithAccessToken(ctx context.Context, token *AccessToken) context.Context {
	return context.WithValue(ctx, AccessTokenContextKey, token)
}

// AccessTokenFromContext returns the bearer token resolved by the auth
// middleware, or nil when the request was authenticated via session cookie.
func AccessTokenFromContext(ctx context.Context) *AccessToken {
	if ctx == nil {
		return nil
	}
	token, ok := ctx.Value(AccessTokenContextKey).(*AccessToken)
	if !ok {
		return nil
	}
	return token
}

// IsBrowserToken reports whether the request was authenticated with a
// restricted browser-audience token.
func IsBrowserToken(ctx context.Context) bool {
	token := AccessTokenFromContext(ctx)
	return token != nil && token.Audience == AudienceBrowserAPI
}
