package cmd

import (
	"database/sql"
	"encoding/json"
	"os"
)

// printJSON writes v to stdout as a single compact JSON value followed by a
// newline — the --json alternative to the fixed-width text tables on
// search/list commands, so a caller doesn't have to parse column-aligned
// text.
func printJSON(v any) error {
	return json.NewEncoder(os.Stdout).Encode(v)
}

// nullStrPtr and nullIntPtr convert database/sql's Null* types to a plain
// Go pointer (nil when not valid), since sql.NullString/NullInt64 have no
// MarshalJSON of their own and would otherwise serialize as
// {"String":"x","Valid":true} — not what a --json consumer wants.
func nullStrPtr(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	s := v.String
	return &s
}

func nullIntPtr(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	n := v.Int64
	return &n
}
