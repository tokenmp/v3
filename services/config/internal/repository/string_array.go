package repository

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
)

// StringArray is a []string that marshals/unmarshals as a JSON array and
// scans/values as PostgreSQL jsonb. Unlike Go's []byte (which JSON-encodes as
// base64), StringArray round-trips as ["a","b"] so API consumers can send and
// receive plain string arrays.
type StringArray []string

// MarshalJSON implements json.Marshaler. nil/empty outputs "[]".
func (a StringArray) MarshalJSON() ([]byte, error) {
	if len(a) == 0 {
		return []byte("[]"), nil
	}
	return json.Marshal([]string(a))
}

// UnmarshalJSON implements json.Unmarshaler. Accepts a JSON array; empty or
// null yields an empty (non-nil) slice. Type mismatch returns an error.
func (a *StringArray) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*a = StringArray{}
		return nil
	}
	var s []string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("StringArray: expected JSON array, got %s", dataTypeName(data))
	}
	if s == nil {
		s = []string{}
	}
	*a = StringArray(s)
	return nil
}

// Scan implements sql.Scanner. Reads jsonb []byte into the string slice.
func (a *StringArray) Scan(src any) error {
	if src == nil {
		*a = StringArray{}
		return nil
	}
	b, ok := src.([]byte)
	if !ok {
		return fmt.Errorf("StringArray.Scan: unexpected type %T", src)
	}
	if len(b) == 0 {
		*a = StringArray{}
		return nil
	}
	var s []string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("StringArray.Scan: %w", err)
	}
	if s == nil {
		s = []string{}
	}
	*a = StringArray(s)
	return nil
}

// Value implements driver.Valuer. Returns JSON-encoded []byte for GORM jsonb.
func (a StringArray) Value() (driver.Value, error) {
	if len(a) == 0 {
		return []byte("[]"), nil
	}
	return json.Marshal([]string(a))
}

// dataTypeName returns a human-readable name for the JSON value type.
func dataTypeName(data []byte) string {
	if len(data) == 0 {
		return "empty"
	}
	switch data[0] {
	case '"':
		return "string"
	case '{':
		return "object"
	case '[':
		return "array"
	case 't', 'f':
		return "bool"
	case 'n':
		return "null"
	default:
		if data[0] >= '0' && data[0] <= '9' || data[0] == '-' {
			return "number"
		}
		return "unknown"
	}
}
