package database

import (
	"strings"
	"testing"
)

func TestEnsureJSONSafeQueryMode(t *testing.T) {
	tests := []struct {
		name string
		dsn  string
		want string
	}{
		{
			name: "keyword/value exec mode is rewritten",
			dsn:  "host=postgres user=openchat password=secret dbname=openchat port=5432 sslmode=disable TimeZone=UTC default_query_exec_mode=exec statement_cache_capacity=0",
			want: "default_query_exec_mode=cache_describe",
		},
		{
			name: "keyword/value simple_protocol is rewritten",
			dsn:  "host=postgres user=openchat default_query_exec_mode=simple_protocol",
			want: "default_query_exec_mode=cache_describe",
		},
		{
			name: "url exec mode is rewritten",
			dsn:  "postgres://openchat:openchat@localhost:5433/openchat?sslmode=disable&default_query_exec_mode=exec&statement_cache_capacity=0",
			want: "default_query_exec_mode=cache_describe",
		},
		{
			name: "disabled statement cache without a mode gains cache_describe",
			dsn:  "host=postgres user=openchat statement_cache_capacity=0",
			want: "default_query_exec_mode=cache_describe",
		},
		{
			name: "url disabled statement cache gains cache_describe",
			dsn:  "postgres://openchat:openchat@localhost:5432/openchat?statement_cache_capacity=0",
			want: "default_query_exec_mode=cache_describe",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ensureJSONSafeQueryMode(tt.dsn)
			if !strings.Contains(got, tt.want) {
				t.Fatalf("ensureJSONSafeQueryMode() = %q, want it to contain %q", got, tt.want)
			}
			if strings.Contains(strings.ToLower(got), "default_query_exec_mode=exec") ||
				strings.Contains(strings.ToLower(got), "default_query_exec_mode=simple_protocol") {
				t.Fatalf("ensureJSONSafeQueryMode() left an unsafe query mode: %q", got)
			}
			if !strings.Contains(got, "statement_cache_capacity=0") && strings.Contains(tt.dsn, "statement_cache_capacity=0") {
				t.Fatalf("ensureJSONSafeQueryMode() dropped statement_cache_capacity: %q", got)
			}
		})
	}
}

func TestEnsureJSONSafeQueryModeLeavesSafeDSNUnchanged(t *testing.T) {
	for _, dsn := range []string{
		"host=postgres user=openchat password=secret dbname=openchat port=5432 sslmode=disable TimeZone=UTC",
		"postgres://openchat:openchat@localhost:5432/openchat?sslmode=disable",
		"host=postgres user=openchat default_query_exec_mode=cache_describe statement_cache_capacity=0",
		"host=postgres user=openchat statement_cache_capacity=5",
	} {
		if got := ensureJSONSafeQueryMode(dsn); got != dsn {
			t.Fatalf("ensureJSONSafeQueryMode(%q) = %q, want unchanged", dsn, got)
		}
	}
}
