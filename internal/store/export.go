package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
)

// ExportRecord is one row of the audit stream. Keeping every table in a single
// typed stream is what makes "show me the record for change X" answerable.
type ExportRecord struct {
	Kind string         `json:"kind"`
	Data map[string]any `json:"data"`
}

var exportTables = []struct {
	kind      string
	table     string
	timeField string
}{
	{"spec", "specs", "created_at"},
	{"spec_version", "spec_versions", "created_at"},
	{"decision", "decisions", "created_at"},
	{"task", "tasks", "created_at"},
	{"session", "sessions", "started_at"},
	{"event", "events", "created_at"},
	{"approval", "approvals", "created_at"},
	{"check", "checks", "created_at"},
	{"dependency", "dependencies", "created_at"},
	{"eval", "evals", "created_at"},
	{"memory", "memory", "created_at"},
	{"feature", "features", "created_at"},
	{"criterion", "task_criteria", "created_at"},
	{"link", "task_links", "created_at"},
	{"note", "notes", "created_at"},
}

// ExportJSONL writes every record as one JSON object per line. `since` is an
// RFC3339 date; empty exports everything.
func (s *Store) ExportJSONL(w io.Writer, since string) (int, error) {
	enc := json.NewEncoder(w)
	total := 0
	for _, t := range exportTables {
		// t.table/t.timeField come only from the hardcoded exportTables literal
		// above, never from caller input -- safe to Sprintf into SQL as table/
		// column identifiers; values still go through placeholders below.
		q := fmt.Sprintf("SELECT * FROM %s", t.table)
		var args []any
		if since != "" {
			q += fmt.Sprintf(" WHERE %s >= ?", t.timeField)
			args = append(args, since)
		}
		q += " ORDER BY id"

		rows, err := s.DB.Query(q, args...)
		if err != nil {
			return total, fmt.Errorf("exporting %s: %w", t.table, err)
		}
		records, err := rowsToMaps(rows)
		rows.Close()
		if err != nil {
			return total, fmt.Errorf("exporting %s: %w", t.table, err)
		}
		for _, rec := range records {
			if err := enc.Encode(ExportRecord{Kind: t.kind, Data: rec}); err != nil {
				return total, err
			}
			total++
		}
	}
	return total, nil
}

func rowsToMaps(rows *sql.Rows) ([]map[string]any, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		rec := make(map[string]any, len(cols))
		for i, c := range cols {
			// SQLite text arrives as []byte; emit it as a string so the
			// export is readable rather than base64.
			if b, ok := vals[i].([]byte); ok {
				rec[c] = string(b)
			} else {
				rec[c] = vals[i]
			}
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}
