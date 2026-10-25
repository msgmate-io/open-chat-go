package database

import (
	"encoding/json"
	"os"
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
func TestPostgresJSONCompat(t *testing.T) {
	dsn := os.Getenv("PG_DSN")
	if dsn == "" {
		t.Skip("set PG_DSN to run the postgres compatibility probe")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
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
