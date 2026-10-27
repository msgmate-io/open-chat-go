package database

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type pgCompatProbe struct {
	ID   uint            `gorm:"primaryKey"`
	Raw  json.RawMessage `gorm:"type:jsonb"`
	Cust JSONRaw         `gorm:"type:jsonb"`
	Body []byte          `gorm:"type:bytea"`
}

// TestPostgresJSONCompat is a manual probe (enable with PG_DSN) that verifies the
// JSON column types the models use actually round-trip on PostgreSQL via the real
// driver, since the SQLite schema uses `blob`/`jsonb` type names.
//
// It deliberately drives the connection through the *unsafe* production query
// mode (`default_query_exec_mode=exec` with a disabled statement cache) so it
// also proves ensureJSONSafeQueryMode repairs it: without the safeguard a
// `json.RawMessage`/`[]byte` parameter is encoded as `bytea` and every jsonb
// write fails with SQLSTATE 22P02 (ci#878).
func TestPostgresJSONCompat(t *testing.T) {
	dsn := os.Getenv("PG_DSN")
	if dsn == "" {
		t.Skip("set PG_DSN to run the postgres compatibility probe")
	}
	// Assert the safety net actually rewrites the unsafe mode.
	unsafeDSN := appendDSNParam(appendDSNParam(dsn, "default_query_exec_mode", "exec"), "statement_cache_capacity", "0")
	safeDSN := ensureJSONSafeQueryMode(unsafeDSN)
	if strings.Contains(strings.ToLower(safeDSN), "default_query_exec_mode=exec") {
		t.Fatalf("ensureJSONSafeQueryMode left the unsafe exec mode in place: %q", safeDSN)
	}

	db, err := gorm.Open(postgres.Open(safeDSN), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.Exec("DROP TABLE IF EXISTS pg_compat_probes").Error; err != nil {
		t.Fatalf("drop: %v", err)
	}
	if err := db.AutoMigrate(&pgCompatProbe{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	row := pgCompatProbe{
		Raw:  json.RawMessage(`{"a":1}`),
		Cust: JSONRaw(`{"b":2}`),
		Body: []byte{0x01, 0x02, 0x03},
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("insert: %v", err)
	}
	var got pgCompatProbe
	if err := db.First(&got, row.ID).Error; err != nil {
		t.Fatalf("select: %v", err)
	}
	t.Logf("raw=%s cust=%s body=%v", string(got.Raw), string(got.Cust), got.Body)
	if string(got.Raw) != `{"a": 1}` && string(got.Raw) != `{"a":1}` {
		t.Errorf("Raw round-trip mismatch: %q", string(got.Raw))
	}
	if string(got.Cust) != `{"b":2}` && string(got.Cust) != `{"b": 2}` {
		t.Errorf("Cust round-trip mismatch: %q", string(got.Cust))
	}
}
