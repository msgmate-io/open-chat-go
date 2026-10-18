package sandboxsession

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func newTestIssuer(t *testing.T) *TokenIssuer {
	t.Helper()
	issuer, err := NewTokenIssuer([]byte("server-key"))
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	return issuer
}

func TestAttachTokenRoundTrip(t *testing.T) {
	issuer := newTestIssuer(t)

	token, err := issuer.Mint("sbx-1", "sess-1", "admin-1", time.Hour)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if strings.Contains(token, "admin-1") {
		t.Fatalf("token must not carry plaintext owner id: %q", token)
	}

	claims, err := issuer.Verify("sbx-1", "sess-1", "admin-1", token)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if claims.SandboxUUID != "sbx-1" || claims.SessionID != "sess-1" || claims.OwnerUUID != "admin-1" {
		t.Fatalf("unexpected claims: %+v", claims)
	}
	if time.Until(claims.ExpiresAt) <= 0 {
		t.Fatalf("expected a future expiry, got %s", claims.ExpiresAt)
	}
}

func TestAttachTokenRejectsReplayAndTampering(t *testing.T) {
	issuer := newTestIssuer(t)
	token, err := issuer.Mint("sbx-1", "sess-1", "admin-1", time.Hour)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	cases := map[string]struct {
		sandbox string
		session string
		owner   string
		token   string
	}{
		"other sandbox":  {"sbx-2", "sess-1", "admin-1", token},
		"other session":  {"sbx-1", "sess-2", "admin-1", token},
		"other owner":    {"sbx-1", "sess-1", "admin-2", token},
		"garbage":        {"sbx-1", "sess-1", "admin-1", "nope"},
		"empty":          {"sbx-1", "sess-1", "admin-1", ""},
		"flipped last":   {"sbx-1", "sess-1", "admin-1", token[:len(token)-1] + "0"},
		"wrong sig type": {"sbx-1", "sess-1", "admin-1", strings.Replace(token, ".", ".zz", 1)},
	}
	for name, c := range cases {
		if _, err := issuer.Verify(c.sandbox, c.session, c.owner, c.token); !errors.Is(err, ErrInvalidAttachToken) {
			t.Fatalf("%s: expected ErrInvalidAttachToken, got %v", name, err)
		}
	}

	foreign, err := NewTokenIssuer([]byte("another-key"))
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	if _, err := foreign.Verify("sbx-1", "sess-1", "admin-1", token); !errors.Is(err, ErrInvalidAttachToken) {
		t.Fatalf("expected a token from another key to be rejected, got %v", err)
	}
}

func TestAttachTokenExpires(t *testing.T) {
	issuer := newTestIssuer(t)
	now := time.Now()
	issuer.now = func() time.Time { return now }

	token, err := issuer.Mint("sbx-1", "sess-1", "admin-1", time.Minute)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if _, err := issuer.Verify("sbx-1", "sess-1", "admin-1", token); err != nil {
		t.Fatalf("expected fresh token to verify: %v", err)
	}

	issuer.now = func() time.Time { return now.Add(2 * time.Minute) }
	if _, err := issuer.Verify("sbx-1", "sess-1", "admin-1", token); !errors.Is(err, ErrInvalidAttachToken) {
		t.Fatalf("expected expired token to be rejected, got %v", err)
	}
}

func TestNewTokenIssuerRejectsEmptyKey(t *testing.T) {
	if _, err := NewTokenIssuer(nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected empty key to be rejected, got %v", err)
	}
	key, err := NewRandomKey()
	if err != nil || len(key) != 32 {
		t.Fatalf("expected a 32 byte random key, got %d (%v)", len(key), err)
	}
}

func TestMintRequiresIdentifiers(t *testing.T) {
	issuer := newTestIssuer(t)
	if _, err := issuer.Mint("", "sess-1", "admin-1", time.Hour); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected missing sandbox id to fail, got %v", err)
	}
	if _, err := issuer.Mint("sbx-1", " ", "admin-1", time.Hour); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected missing session id to fail, got %v", err)
	}
}
