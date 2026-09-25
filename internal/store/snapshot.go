package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// snapshotTables lists every table in dependency-agnostic order — FK checks
// are disabled during import (see LoadSnapshot), so insertion order doesn't
// need to respect foreign keys.
//
// Deliberately absent: `embeddings` (derived vectors; rebuild them with
// `acline reindex-embeddings`), `search_index` (rebuilt on import), and `meta`
// (holds the approval-token hash -- an export must not leak it and an import
// must not be able to reset or replace it). Every
// other table in the schema must be listed here --
// TestSnapshotCoversEverySchemaTable fails if one is added and forgotten.
var snapshotTables = []string{
	"projects", "roles", "specs", "decisions", "milestones", "tasks", "sessions", "events",
	"approvals", "checks", "dependencies", "evals", "memory", "features",
	"task_criteria", "task_links", "notes", "check_runners", "spec_versions",
	"plans", "plan_items", "plan_edges", "plan_criteria",
}

// SnapshotVersion guards the JSON shape. Bump it if a future change to the
// table list or row shape would make an old snapshot ambiguous to import.
// v2 added the `roles` table; a v1 snapshot still imports (its rows carry
// role_id values that resolve against the roles seeded by migrations).
const SnapshotVersion = 2

// Snapshot is the full database as one JSON document, for backup and restore
// (`acline snapshot export` / `import`). It covers the whole shared store, not
// one project, so it is not a per-repo commit artifact.
type Snapshot struct {
	Version    int                         `json:"version"`
	ExportedAt string                      `json:"exported_at"`
	Tables     map[string][]map[string]any `json:"tables"`
}

// SnapshotJSON writes the full database as one indented JSON document.
func (s *Store) SnapshotJSON(w io.Writer) error {
	snap := Snapshot{
		Version:    SnapshotVersion,
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
		Tables:     make(map[string][]map[string]any, len(snapshotTables)),
	}
	for _, table := range snapshotTables {
		rows, err := s.DB.Query("SELECT * FROM " + table + " ORDER BY id")
		if err != nil {
			return fmt.Errorf("reading %s: %w", table, err)
		}
		records, err := rowsToMaps(rows)
		rows.Close()
		if err != nil {
			return fmt.Errorf("reading %s: %w", table, err)
		}
		snap.Tables[table] = records
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(snap)
}

// LoadSnapshot imports a JSON snapshot into the database. Every insert is
// INSERT OR IGNORE — a row whose primary key already exists is left alone —
// so importing is idempotent and never triggers the append-only tables'
// UPDATE/DELETE guards. Foreign keys are disabled for the duration, since a
// snapshot may contain forward references (e.g. a decision superseded by one
// inserted later) that would otherwise require a topological insert order.
//
// Merging rows into a store that already holds work is privileged: a snapshot
// can carry approvals, checks, accepted decisions and done tasks, so an agent
// that could import one could forge the evidence the completion gate rests
// on. Such an import needs a person, or the approval token when one is
// enabled. Seeding an empty store (`acline init` from acline.json) is not
// privileged, since there is nothing yet to forge against.
func (s *Store) LoadSnapshot(r io.Reader) (map[string]int, error) {
	return s.LoadSnapshotWithToken(r, "")
}

// ErrAgentCannotImportSnapshot is returned when an agent tries to merge a
// snapshot into a store that already has data.
var ErrAgentCannotImportSnapshot = errors.New("an agent cannot import a snapshot into a store that already has data: a person must do it")

// LoadSnapshotWithToken is LoadSnapshot with the approval token, for the CLI
// path that prompts for one.
func (s *Store) LoadSnapshotWithToken(r io.Reader, token string) (map[string]int, error) {
	empty, err := s.isEmpty()
	if err != nil {
		return nil, err
	}
	if !empty {
		if err := s.requirePerson(token, ErrAgentCannotImportSnapshot); err != nil {
			return nil, err
		}
	}
	dec := json.NewDecoder(r)
	dec.UseNumber()
	var snap Snapshot
	if err := dec.Decode(&snap); err != nil {
		return nil, fmt.Errorf("parsing snapshot: %w", err)
	}
	if snap.Version > SnapshotVersion {
		return nil, fmt.Errorf("snapshot version %d is newer than this acline build supports (%d) — upgrade acline first", snap.Version, SnapshotVersion)
	}

	if _, err := s.DB.Exec("PRAGMA foreign_keys = OFF"); err != nil {
		return nil, err
	}
	defer s.DB.Exec("PRAGMA foreign_keys = ON")

	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	counts := make(map[string]int, len(snapshotTables))
	for _, table := range snapshotTables {
		rows := snap.Tables[table]
		if len(rows) == 0 {
			continue
		}
		n, err := importTable(tx, table, rows)
		if err != nil {
			return nil, fmt.Errorf("importing %s: %w", table, err)
		}
		if n > 0 {
			counts[table] = n
		}
	}

	// An imported snapshot must leave the audit trail verifiable. Rows are
	// merged by primary key (INSERT OR IGNORE), so importing another store's
	// events into a non-empty store forks the hash chain -- refuse that here
	// rather than commit a trail `acline verify` will then report as tampered.
	if res, err := verifyChain(tx); err != nil {
		return nil, fmt.Errorf("verifying audit trail after import: %w", err)
	} else if !res.OK() {
		return nil, fmt.Errorf("snapshot would break the audit trail's hash chain at event #%d (%s); import into an empty store, or from a snapshot of this same store", res.BadID, res.Reason)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if err := s.rebuildSearchIndex(); err != nil {
		return nil, fmt.Errorf("rebuilding search index: %w", err)
	}
	return counts, nil
}

// isEmpty reports whether the store holds no user data yet (a fresh database):
// no row in any snapshot table except the roles every store is seeded with
// (project_id NULL). Configuration counts as data: projects, check runners and
// can_approve roles all change what later evidence means.
func (s *Store) isEmpty() (bool, error) {
	for _, table := range snapshotTables {
		q := "SELECT COUNT(*) FROM " + table
		if table == "roles" {
			q += " WHERE project_id IS NOT NULL"
		}
		var n int
		if err := s.DB.QueryRow(q).Scan(&n); err != nil {
			return false, err
		}
		if n > 0 {
			return false, nil
		}
	}
	return true, nil
}

// rebuildSearchIndex clears and repopulates search_index from the tables
// that feed it, so a `acline init`-imported snapshot is fully searchable
// without having gone through Add* one row at a time.
func (s *Store) rebuildSearchIndex() error {
	if _, err := s.DB.Exec(`DELETE FROM search_index`); err != nil {
		return err
	}
	type source struct {
		kind, sql string
	}
	sources := []source{
		{"decision", `SELECT id, project_id, title || ' ' || COALESCE(context,'') || ' ' || COALESCE(decision,'') || ' ' || COALESCE(rationale,'') FROM decisions`},
		{"memory", `SELECT id, project_id, body FROM memory`},
		{"note", `SELECT id, project_id, body FROM notes`},
		{"spec", `SELECT id, project_id, title || ' ' || COALESCE(body,'') FROM specs`},
	}
	// The store pins the connection pool to 1 (see Open), so a write must
	// never run while a *sql.Rows from this same *sql.DB is still open —
	// it would block forever waiting for a connection the open read holds.
	// Read each source fully into memory first, then write.
	type indexRow struct {
		id        int64
		projectID sql.NullInt64
		body      string
	}
	for _, src := range sources {
		rows, err := s.DB.Query(src.sql)
		if err != nil {
			return err
		}
		var batch []indexRow
		for rows.Next() {
			var r indexRow
			if err := rows.Scan(&r.id, &r.projectID, &r.body); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, r)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()

		for _, r := range batch {
			if err := s.indexForSearch(src.kind, r.id, r.projectID, r.body); err != nil {
				return err
			}
		}
	}
	return nil
}

func importTable(tx *sql.Tx, table string, rows []map[string]any) (int, error) {
	cols, err := tableColumns(tx, table)
	if err != nil {
		return 0, err
	}

	// table is always one of the hardcoded snapshotTables names (never a key
	// read out of the parsed snapshot's own JSON), and cols come from this
	// database's own schema via PRAGMA table_info -- both safe to Sprintf into
	// SQL as identifiers; row values still go through placeholders below.
	placeholders := make([]string, len(cols))
	for i := range placeholders {
		placeholders[i] = "?"
	}
	stmt := fmt.Sprintf("INSERT OR IGNORE INTO %s (%s) VALUES (%s)",
		table, strings.Join(cols, ", "), strings.Join(placeholders, ", "))

	prepared, err := tx.Prepare(stmt)
	if err != nil {
		return 0, err
	}
	defer prepared.Close()

	n := 0
	for _, row := range rows {
		args := make([]any, len(cols))
		for i, c := range cols {
			args[i] = normalizeJSONValue(row[c])
		}
		res, err := prepared.Exec(args...)
		if err != nil {
			return n, err
		}
		if affected, _ := res.RowsAffected(); affected > 0 {
			n++
		}
	}
	return n, nil
}

// tableColumns reads a table's column names in declaration order via SQLite's
// schema introspection, so the INSERT statement always matches the live
// schema rather than whatever keys happen to be present in the JSON.
func tableColumns(tx *sql.Tx, table string) ([]string, error) {
	rows, err := tx.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cols []string
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		cols = append(cols, name)
	}
	return cols, rows.Err()
}

// normalizeJSONValue converts a json.Decoder(UseNumber)-produced value back
// into something SQLite's driver accepts as a bind argument: whole numbers
// become int64, fractional numbers become float64, everything else passes
// through unchanged (including nil for a JSON null).
func normalizeJSONValue(v any) any {
	num, ok := v.(json.Number)
	if !ok {
		return v
	}
	if i, err := num.Int64(); err == nil {
		return i
	}
	if f, err := num.Float64(); err == nil {
		return f
	}
	return num.String()
}
