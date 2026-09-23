package user

import (
	"encoding/base32"
	"testing"
	"time"
)

func TestNormalizeTOTPSecret(t *testing.T) {
	normalized, err := NormalizeTOTPSecret("  jbswy3dp ehpk3pxp  ")
	if err != nil {
		t.Fatalf("NormalizeTOTPSecret returned error: %v", err)
	}
	if normalized != "JBSWY3DPEHPK3PXP" {
		t.Fatalf("expected JBSWY3DPEHPK3PXP, got %q", normalized)
	}
}

func TestNormalizeTOTPSecretRejectsInvalid(t *testing.T) {
	if _, err := NormalizeTOTPSecret("not-base32-1"); err == nil {
		t.Fatalf("expected error for invalid base32 secret")
	}
	if _, err := NormalizeTOTPSecret("   "); err == nil {
		t.Fatalf("expected error for empty secret")
	}
}

func TestVerifyTOTPAcceptsSpacedLowercaseSecret(t *testing.T) {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString("JBSWY3DPEHPK3PXP")
	if err != nil {
		t.Fatalf("failed to decode test secret: %v", err)
	}
	now := time.Unix(1700000000, 0)
	code := hotp(key, uint64(now.Unix())/30, 6)
	if !VerifyTOTP(" jbswy3dp ehpk3pxp ", code, now) {
		t.Fatalf("expected spaced/lowercase secret to verify")
	}
	if VerifyTOTP("not-base32-1", code, now) {
		t.Fatalf("expected invalid secret to fail verification")
	}
}
