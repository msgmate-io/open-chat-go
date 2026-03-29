package util

import (
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestParsePathUUID(t *testing.T) {
	canonical := "0f5b3a1c-9d2e-4f47-8a1b-2c3d4e5f6a7b"

	cases := []struct {
		name      string
		raw       string
		want      string
		wantError bool
	}{
		{name: "canonical", raw: canonical, want: canonical},
		{name: "uppercase is normalised", raw: "0F5B3A1C-9D2E-4F47-8A1B-2C3D4E5F6A7B", want: canonical},
		{name: "surrounding whitespace is trimmed", raw: "  " + canonical + "\n", want: canonical},
		{name: "braced form is normalised", raw: "{" + canonical + "}", want: canonical},
		{name: "urn form is normalised", raw: "urn:uuid:" + canonical, want: canonical},
		{name: "dashless form is normalised", raw: "0f5b3a1c9d2e4f478a1b2c3d4e5f6a7b", want: canonical},
		{name: "nil uuid", raw: uuid.Nil.String(), want: uuid.Nil.String()},
		{name: "empty", raw: "", wantError: true},
		{name: "whitespace only", raw: "   ", wantError: true},
		{name: "not a uuid", raw: "not-a-uuid", wantError: true},
		{name: "truncated", raw: "0f5b3a1c-9d2e-4f47-8a1b", wantError: true},
		{name: "extra segment", raw: canonical + "-extra", wantError: true},
		{name: "path traversal", raw: "../../../etc/passwd", wantError: true},
		{name: "sql injection fragment", raw: "' OR 1=1 --", wantError: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.SetPathValue("chat_uuid", tc.raw)

			got, err := ParsePathUUID(r, "chat_uuid")
			if tc.wantError {
				if err == nil {
					t.Fatalf("expected error for %q, got %q", tc.raw, got)
				}
				if got != "" {
					t.Fatalf("expected empty result on error for %q, got %q", tc.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.raw, err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParsePathUUIDMissingParam(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)

	if _, err := ParsePathUUID(r, "chat_uuid"); err == nil {
		t.Fatal("expected an error when the path parameter is absent")
	}
}

func TestParsePathUUIDBlankParamIsMissing(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.SetPathValue("chat_uuid", "   ")

	if _, err := ParsePathUUID(r, "chat_uuid"); err != ErrMissingPathUUID {
		t.Fatalf("expected ErrMissingPathUUID, got %v", err)
	}
}
