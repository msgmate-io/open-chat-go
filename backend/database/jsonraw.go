package database

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
)

// JSONRaw is a tolerant JSON column type. It behaves like json.RawMessage but
// can be scanned from databases that return the underlying column as either a
// []byte (BLOB) or a string (TEXT). Some legacy rows store jsonb columns as
// TEXT, which would otherwise fail to scan into json.RawMessage (on newer Go
// versions json.RawMessage is jsontext.Value and implements neither
// sql.Scanner nor driver.Valuer).
type JSONRaw json.RawMessage

// Scan implements sql.Scanner so both BLOB and TEXT columns are accepted.
func (j *JSONRaw) Scan(src any) error {
	if src == nil {
		*j = nil
		return nil
	}
	switch v := src.(type) {
	case []byte:
		*j = append((*j)[:0], v...)
		return nil
	case string:
		*j = append((*j)[:0], v...)
		return nil
	default:
		return fmt.Errorf("unsupported Scan, storing driver.Value type %T into type *database.JSONRaw", src)
	}
}

// jsonRawValueAsText selects how JSONRaw is encoded for writes. It is set once
// by SetupDatabase: PostgreSQL jsonb columns need a text parameter (a []byte is
// sent as bytea and rejected with "invalid input syntax for type json", 22P02,
// ci#878), while SQLite keeps the historical []byte/BLOB encoding that eagerly
// scans back into the json.RawMessage fields much of the codebase still uses.
var jsonRawValueAsText bool

// Value implements driver.Valuer. Whether it returns a string or []byte depends
// on the active backend (see jsonRawValueAsText). Scan accepts both.
func (j JSONRaw) Value() (driver.Value, error) {
	if j == nil {
		return nil, nil
	}
	if jsonRawValueAsText {
		return string(j), nil
	}
	return []byte(j), nil
}

// MarshalJSON keeps the stored JSON raw instead of base64-encoding the
// underlying byte slice when a struct containing this field is marshalled.
func (j JSONRaw) MarshalJSON() ([]byte, error) {
	if j == nil {
		return []byte("null"), nil
	}
	return j, nil
}

// UnmarshalJSON stores the raw JSON bytes when decoding into this type.
func (j *JSONRaw) UnmarshalJSON(data []byte) error {
	if j == nil {
		return fmt.Errorf("database.JSONRaw: UnmarshalJSON on nil pointer")
	}
	if data == nil {
		*j = nil
		return nil
	}
	*j = append((*j)[:0], data...)
	return nil
}
