package secretstore

import "context"

type sessionCtxKey struct{}

// WithSessionKey attaches the authorization cache key (a session id or
// bearer-token hash) to a context. Password-based providers read it during
// Unseal to locate the caller's cached key material.
func WithSessionKey(ctx context.Context, sessionKey string) context.Context {
	return context.WithValue(ctx, sessionCtxKey{}, sessionKey)
}

// SessionKeyFromContext returns the session key attached by WithSessionKey.
func SessionKeyFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	value, ok := ctx.Value(sessionCtxKey{}).(string)
	if !ok || value == "" {
		return "", false
	}
	return value, true
}

// RevokeSessions drops any cached authorization for each session key from every
// registered provider. It is called on logout.
func RevokeSessions(sessionKeys ...string) {
	providers := Providers()
	for _, sessionKey := range sessionKeys {
		if sessionKey == "" {
			continue
		}
		for _, provider := range providers {
			provider.Revoke(context.Background(), AuthParams{SessionKey: sessionKey})
		}
	}
}
