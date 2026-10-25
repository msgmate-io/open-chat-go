package database

import (
	"net/url"
	"regexp"
	"strings"

	"backend/logging"
)

// unsafeQueryMode matches the pgx query-execution modes that encode a Go
// `[]byte` parameter as `bytea` instead of a JSON `text` literal. Every
// `jsonb`/`json` column that the codebase writes as `json.RawMessage`/`[]byte`
// (most of the integrations, and any raw `Update("col", encodedJSON)` that has
// not been migrated to database.JSONRaw) is then rejected by PostgreSQL with
// SQLSTATE 22P02 ("invalid input syntax for type json", ci#878).
var unsafeQueryMode = regexp.MustCompile(`(?i)(default_query_exec_mode=)(exec|simple_protocol)(&|\s|$)`)

// ensureJSONSafeQueryMode rewrites a PostgreSQL DSN so the connection never runs
// in a query-execution mode that breaks `[]byte` JSON writes.
//
// The migration off SQLite shipped a production DSN that pinned
// `default_query_exec_mode=exec` together with `statement_cache_capacity=0`.
// pgx requires an explicit non-caching mode when the statement cache is
// disabled, and `exec` (the simple protocol) cannot describe a parameter's
// type, so it falls back to the Go value's type: `[]byte` becomes `bytea` and
// every `jsonb` write fails. `cache_describe` describes each statement (so the
// parameter is typed as `jsonb` and `[]byte`/string bind correctly) without
// needing the statement cache, making it the safe replacement. That keeps the
// SQLite behaviour ("bytes go into the column, bytes come back") identical on
// PostgreSQL for the whole codebase, including the integrations that still pass
// raw `[]byte`, instead of forcing a per-model rewrite (ci#878).
//
// It also repairs the mismatched `statement_cache_capacity=0` + default
// (`cache_statement`) combination, which pgx otherwise rejects at query time
// with "cannot use QueryExecModeCacheStatement with disabled statement cache".
func ensureJSONSafeQueryMode(dsn string) string {
	if unsafeQueryMode.MatchString(dsn) {
		logging.Warnf("PostgreSQL DSN requested an unsafe query mode for JSON columns; using cache_describe so []byte jsonb writes work (ci#878)")
		return unsafeQueryMode.ReplaceAllString(dsn, "${1}cache_describe${3}")
	}

	if statementCacheDisabled(dsn) && !dsnHasParam(dsn, "default_query_exec_mode") {
		logging.Warnf("PostgreSQL DSN disables the statement cache without selecting a query mode; adding cache_describe (ci#878)")
		return appendDSNParam(dsn, "default_query_exec_mode", "cache_describe")
	}

	return dsn
}

// dsnHasParam reports whether the DSN (URL or keyword/value form) carries the
// given parameter.
func dsnHasParam(dsn, key string) bool {
	_, ok := dsnParamValue(dsn, key)
	return ok
}

// statementCacheDisabled reports whether the DSN explicitly sets
// statement_cache_capacity to 0.
func statementCacheDisabled(dsn string) bool {
	if v, ok := dsnParamValue(dsn, "statement_cache_capacity"); ok {
		return strings.TrimSpace(v) == "0"
	}
	return false
}

// dsnParamValue returns the value of a DSN parameter in either the URL form
// (postgres://...?a=b&c=d) or the keyword/value form (host=... a=b c=d).
func dsnParamValue(dsn, key string) (string, bool) {
	if strings.Contains(dsn, "://") {
		u, err := url.Parse(dsn)
		if err != nil {
			return "", false
		}
		if values, ok := u.Query()[key]; ok && len(values) > 0 {
			return values[0], true
		}
		return "", false
	}

	for _, token := range strings.Fields(dsn) {
		k, v, ok := strings.Cut(token, "=")
		if ok && strings.EqualFold(k, key) {
			return v, true
		}
	}
	return "", false
}

// appendDSNParam adds a parameter to a DSN, preserving its original form.
func appendDSNParam(dsn, key, value string) string {
	if strings.Contains(dsn, "://") {
		u, err := url.Parse(dsn)
		if err != nil {
			return dsn
		}
		q := u.Query()
		q.Set(key, value)
		u.RawQuery = q.Encode()
		return u.String()
	}
	return dsn + " " + key + "=" + value
}
