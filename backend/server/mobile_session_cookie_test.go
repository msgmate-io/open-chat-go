package server

import (
	"net/url"
	"testing"
)

// TestMobileSessionCookieNameMatchesAndroidClient pins the cookie-name wire
// contract with the Android host app (open-chat-go-mobile:
// MainActivity.sessionCookieNameForServer). That client computes
//
//	"session_id_mobile_" + sha1(lower("<server-id>|<upstream-url>"))[:12]
//
// and reads the cookie out of the local WebView jar to decide whether a server
// is shown as authenticated. If this test fails, the mobile "authenticated"
// badge silently breaks even though login succeeds (open-chat-go-ci#221).
func TestMobileSessionCookieNameMatchesAndroidClient(t *testing.T) {
	cases := []struct {
		name      string
		target    string
		namespace string
		want      string
	}{
		{
			name:      "hosted server with namespace",
			target:    "https://msgmate.io",
			namespace: "msgmate-default",
			want:      "session_id_mobile_546054e6ad4a",
		},
		{
			name:   "no namespace falls back to target only",
			target: "https://msgmate.io",
			want:   "session_id_mobile_f97611cace47",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := url.Parse(tc.target)
			if err != nil {
				t.Fatalf("failed to parse target %q: %v", tc.target, err)
			}
			if got := mobileSessionCookieName(parsed, tc.namespace); got != tc.want {
				t.Fatalf("mobileSessionCookieName(%q, %q) = %q, want %q", tc.target, tc.namespace, got, tc.want)
			}
		})
	}
}
