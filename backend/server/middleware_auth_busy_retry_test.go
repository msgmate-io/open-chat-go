package server

import (
	"errors"
	"testing"
	"time"
)

// The bearer-token lookup runs on every authenticated request and includes an
// idempotent write (EnsureAccountStateRowForUser). A transient SQLite write
// lock used to make the lookup return "no rows", which the auth middleware
// cannot distinguish from an invalid credential, so a momentary backend hiccup
// was surfaced to clients as a hard 403 (open-chat-go-ci#854). runAuthQuery
// retries only transient lock errors.
func TestRunAuthQueryRetriesTransientSQLiteLock(t *testing.T) {
	attempts := 0
	err := runAuthQuery("test: flaky lookup", func() error {
		attempts++
		if attempts < 3 {
			return errors.New("database is locked (5) (SQLITE_BUSY)")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected the lock error to be retried away, got %v", err)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
}

func TestRunAuthQueryDoesNotRetryNonLockErrors(t *testing.T) {
	attempts := 0
	schemaErr := errors.New("no such table: access_tokens")
	err := runAuthQuery("test: schema error", func() error {
		attempts++
		return schemaErr
	})
	if !errors.Is(err, schemaErr) {
		t.Fatalf("expected the schema error to be returned unchanged, got %v", err)
	}
	if attempts != 1 {
		t.Fatalf("a non-lock error must not be retried, got %d attempts", attempts)
	}
}

func TestRunAuthQueryGivesUpAfterBoundedBudget(t *testing.T) {
	attempts := 0
	err := runAuthQuery("test: persistent lock", func() error {
		attempts++
		return errors.New("database table is locked: account_states")
	})
	if err == nil {
		t.Fatal("a persistent lock must still surface as an error")
	}
	if attempts != authBusyRetryPolicy.Attempts {
		t.Fatalf("expected %d attempts, got %d", authBusyRetryPolicy.Attempts, attempts)
	}
}

// A live request path must not inherit the very generous startup policy
// (20 attempts, up to 5s backoff); authentication should fail fast when the
// database really is unavailable.
func TestAuthBusyRetryPolicyIsRequestScoped(t *testing.T) {
	if authBusyRetryPolicy.Attempts > 5 {
		t.Fatalf("auth retry budget too large for a request path: %d attempts", authBusyRetryPolicy.Attempts)
	}
	if authBusyRetryPolicy.MaxDelay > 500*time.Millisecond {
		t.Fatalf("auth retry backoff too large for a request path: %s", authBusyRetryPolicy.MaxDelay)
	}
}
