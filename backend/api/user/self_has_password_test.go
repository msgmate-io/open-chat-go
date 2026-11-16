package user

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"backend/database"
)

// ci#1111: a client must be able to tell "password set" from "no password yet"
// so the profile UI can offer *set* rather than *change* password to an account
// that was provisioned by a Google/OIDC login and therefore starts without a
// hash.
func TestSelfReportsHasPassword(t *testing.T) {
	cases := []struct {
		name         string
		passwordHash string
		want         bool
	}{
		{name: "google_provisioned_account_has_no_password", passwordHash: "", want: false},
		{name: "account_with_password_reports_it", passwordHash: "$2a$10$abcdefghijklmnopqrstuv", want: true},
		{name: "whitespace_only_hash_counts_as_no_password", passwordHash: "   ", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &UserHandler{}
			user := &database.User{
				Name:         "Identity Test",
				Username:     "identity-test",
				PasswordHash: tc.passwordHash,
			}

			req := httptest.NewRequest("GET", "/api/v1/user/self", nil)
			req = req.WithContext(context.WithValue(req.Context(), "user", user))

			rr := httptest.NewRecorder()
			h.Self(rr, req)

			if rr.Code != 200 {
				t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
			}

			var payload struct {
				Username    string `json:"username"`
				HasPassword bool   `json:"has_password"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
				t.Fatalf("expected a decodable self response: %v (%s)", err, rr.Body.String())
			}
			if payload.HasPassword != tc.want {
				t.Fatalf("expected has_password=%v, got %v", tc.want, payload.HasPassword)
			}
			if payload.Username != user.Username {
				t.Fatalf("expected username %q, got %q", user.Username, payload.Username)
			}

			// The boolean is the only thing about the password that may leave the
			// server; the hash stays unexported.
			if bytes.Contains(rr.Body.Bytes(), []byte("password_hash")) {
				t.Fatalf("self response leaked password_hash: %s", rr.Body.String())
			}
		})
	}
}
