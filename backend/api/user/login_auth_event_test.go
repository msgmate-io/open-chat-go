package user

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"backend/database"
	"backend/server/util"
	integrationinterface "github.com/msgmate-io/go-integration-interface/integrationinterface"
	"gorm.io/gorm"
)

func createAccountStatesTableForLoginTest(db *gorm.DB) *gorm.DB {
	return db.Exec(`CREATE TABLE account_states (
		uuid text, created_at datetime, updated_at datetime, user_id integer primary key,
		signup_date datetime, is_email_verified boolean, suspended boolean
	)`)
}

func TestClientIPFromRequest(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/v1/user/login", nil)
	req.RemoteAddr = "203.0.113.9:1234"
	if got := clientIPFromRequest(req); got != "203.0.113.9" {
		t.Fatalf("expected host-only remote addr, got %q", got)
	}
	req.Header.Set("X-Forwarded-For", "198.51.100.4, 10.0.0.1")
	if got := clientIPFromRequest(req); got != "198.51.100.4" {
		t.Fatalf("expected first forwarded IP, got %q", got)
	}
	req.Header.Del("X-Forwarded-For")
	req.Header.Set("X-Real-IP", "198.51.100.7")
	if got := clientIPFromRequest(req); got != "198.51.100.7" {
		t.Fatalf("expected real IP, got %q", got)
	}
}

func TestAuthMethodFromLoginPayload(t *testing.T) {
	cases := []struct {
		payload UserLogin
		want    string
	}{
		{UserLogin{}, "password"},
		{UserLogin{TwoFactorCode: "123456"}, "two_factor"},
		{UserLogin{RecoveryCode: "abc"}, "recovery_code"},
	}
	for _, tc := range cases {
		if got := authMethodFromLoginPayload(tc.payload); got != tc.want {
			t.Fatalf("expected method %q, got %q", tc.want, got)
		}
	}
}

func TestPublishAuthEventReachesRegisteredSink(t *testing.T) {
	var got *integrationinterface.AuthEvent
	integrationinterface.MustRegister(integrationinterface.Definition{
		Name: "zztest_auth_event_sink",
		AuthEventSink: func(_ context.Context, event integrationinterface.AuthEvent) error {
			captured := event
			got = &captured
			return nil
		},
	})

	req := httptest.NewRequest("POST", "/api/v1/user/login", nil)
	req.RemoteAddr = "203.0.113.7:54321"
	req.Header.Set("User-Agent", "test-agent/1.0")

	publishAuthEvent(req, nil, 42, "user@example.com", "password", false, "invalid password")

	if got == nil {
		t.Fatalf("expected auth event to reach registered sink")
	}
	if got.UserID != 42 || got.Identifier != "user@example.com" || got.Method != "password" || got.Success {
		t.Fatalf("unexpected event payload: %+v", *got)
	}
	if got.FailureReason != "invalid password" {
		t.Fatalf("unexpected failure reason: %q", got.FailureReason)
	}
	if got.IP != "203.0.113.7" {
		t.Fatalf("unexpected IP: %q", got.IP)
	}
	if got.UserAgent != "test-agent/1.0" {
		t.Fatalf("unexpected user agent: %q", got.UserAgent)
	}
	if got.OccurredAt.IsZero() {
		t.Fatalf("expected non-zero timestamp")
	}
}

func TestLoginUserRejectsSuspendedAccount(t *testing.T) {
	db := setupRegisterTestDB(t)
	err, user := util.CreateUser(db, "suspended@example.com", "Passw0rd!", false)
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	if err := createAccountStatesTableForLoginTest(db).Error; err != nil {
		t.Fatalf("failed to create account_states table: %v", err)
	}
	if err := db.Exec("INSERT INTO account_states (user_id, suspended, is_email_verified) VALUES (?, ?, ?)", user.ID, true, true).Error; err != nil {
		t.Fatalf("failed to seed suspended state: %v", err)
	}

	loginErr, token, twofa, _ := LoginUser(db, "suspended@example.com", "Passw0rd!", "", "", time.Now().Add(time.Hour))
	if loginErr == nil {
		t.Fatalf("expected suspended login to fail")
	}
	if token != "" || twofa {
		t.Fatalf("expected no token for suspended login (token=%q twofa=%v)", token, twofa)
	}
	if loginErr.Error() != "account suspended" {
		t.Fatalf("unexpected error: %v", loginErr)
	}

	var sessions int64
	db.Model(&database.Session{}).Where("user_id = ?", user.ID).Count(&sessions)
	if sessions != 0 {
		t.Fatalf("expected no session created for suspended user, got %d", sessions)
	}
}
