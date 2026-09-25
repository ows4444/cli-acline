package store

import (
	"database/sql"
	"strings"
)

// indexForSearch adds one row to the FTS5 index. Called by each Add* method
// right after inserting the source row — see the schema comment on
// search_index for why this is explicit rather than trigger-driven.
func (s *Store) indexForSearch(kind string, refID int64, projectID sql.NullInt64, body string) error {
	_, err := s.DB.Exec(
		`INSERT INTO search_index (kind, ref_id, project_id, body) VALUES (?, ?, ?, ?)`,
		kind, refID, projectID, body,
	)
	return err
}

type SearchHit struct {
	Kind      string
	RefID     int64
	ProjectID sql.NullInt64
	Snippet   string
	// Status is the current status of the underlying row, fetched from its
	// source table at query time (search_index itself is populated once at
	// insert time — see indexForSearch — and never updated on a later
	// status change, so this can't come from the FTS5 row). Meaning is
	// kind-specific: a decision/spec status string, "stale" for a memory
	// entry marked stale (taking precedence over its own status column, on
	// the theory that "stale" is the more useful signal to surface),
	// otherwise a memory/spec status string, "promoted" for a promoted
	// note, or "" (unpromoted note, or the source row no longer exists).
	Status string
	// Score is a cosine similarity in [-1, 1], set only by SemanticSearch
	// (embedding.go) — zero-valued (and meaningless) on a Search hit.
	Score float64
}

// Search runs a keyword query over decisions, memory, notes and specs.
// projectID, when non-nil, restricts results to that project (rows with a
// NULL project_id are always excluded once a scope is given).
//
// query is first tried as FTS5 syntax, so AND/OR/NEAR, "phrases" and prefix*
// work. Ordinary text is not valid FTS5 syntax, though: `foo-bar` parses as a
// column filter ("no such column: bar"), and a stray quote or apostrophe is a
// syntax error. When FTS5 rejects the query, it is retried as a plain
// all-words search (each whitespace-separated word quoted), which is what a
// user typing free text means.
func (s *Store) Search(query string, projectID *int64, limit int) ([]SearchHit, error) {
	if limit <= 0 {
		limit = 20
	}
	out, err := s.searchOnce(query, projectID, limit)
	if err != nil && isFTSQueryError(err) {
		if quoted := ftsQuoteWords(query); quoted != "" {
			out, err = s.searchOnce(quoted, projectID, limit)
		}
	}
	if err != nil {
		return nil, err
	}
	if err := s.attachSearchStatuses(out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) searchOnce(match string, projectID *int64, limit int) ([]SearchHit, error) {
	q := `SELECT kind, ref_id, project_id, snippet(search_index, 3, '[', ']', '...', 10)
		FROM search_index WHERE search_index MATCH ?`
	args := []any{match}
	if projectID != nil {
		q += ` AND project_id = ?`
		args = append(args, *projectID)
	}
	q += ` ORDER BY rank LIMIT ?`
	args = append(args, limit)

	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SearchHit
	for rows.Next() {
		var h SearchHit
		if err := rows.Scan(&h.Kind, &h.RefID, &h.ProjectID, &h.Snippet); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// isFTSQueryError reports whether err is SQLite rejecting the MATCH
// expression itself (as opposed to, say, a locked database).
func isFTSQueryError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "SQL logic error") || strings.Contains(msg, "fts5")
}

// ftsQuoteWords turns free text into an FTS5 query matching rows that contain
// every word: each whitespace-separated word becomes a quoted string (with
// embedded quotes doubled), so punctuation is tokenized away instead of being
// parsed as operators. "" when there is nothing to search for.
func ftsQuoteWords(query string) string {
	words := strings.Fields(query)
	for i, w := range words {
		words[i] = `"` + strings.ReplaceAll(w, `"`, `""`) + `"`
	}
	return strings.Join(words, " ")
}

// reindexForSearch replaces the FTS row for (kind, refID) with body, for rows
// whose indexed text can change after creation (a revised spec). indexForSearch
// alone only ever inserts, which left the old text searchable and the new text
// invisible.
func (s *Store) reindexForSearch(kind string, refID int64, projectID sql.NullInt64, body string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM search_index WHERE kind = ? AND ref_id = ?`, kind, refID); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`INSERT INTO search_index (kind, ref_id, project_id, body) VALUES (?, ?, ?, ?)`,
		kind, refID, projectID, body,
	); err != nil {
		return err
	}
	return tx.Commit()
}

// attachSearchStatuses fills in each hit's Status by batch-fetching, one
// query per kind present in hits, from the row's actual source table —
// grouped rather than one query per hit, since a search result page is
// typically a handful of hits across at most four kinds.
func (s *Store) attachSearchStatuses(hits []SearchHit) error {
	byKind := map[string][]int64{}
	for _, h := range hits {
		byKind[h.Kind] = append(byKind[h.Kind], h.RefID)
	}

	decisionStatus, err := s.fetchStatusColumn("decisions", byKind["decision"])
	if err != nil {
		return err
	}
	specStatus, err := s.fetchStatusColumn("specs", byKind["spec"])
	if err != nil {
		return err
	}
	memoryStatus, err := s.fetchMemoryStatuses(byKind["memory"])
	if err != nil {
		return err
	}
	notePromoted, err := s.fetchNotePromotions(byKind["note"])
	if err != nil {
		return err
	}

	for i := range hits {
		switch hits[i].Kind {
		case "decision":
			hits[i].Status = decisionStatus[hits[i].RefID]
		case "spec":
			hits[i].Status = specStatus[hits[i].RefID]
		case "memory":
			if ms, ok := memoryStatus[hits[i].RefID]; ok {
				if ms.stale {
					hits[i].Status = "stale"
				} else {
					hits[i].Status = ms.status
				}
			}
		case "note":
			if notePromoted[hits[i].RefID] {
				hits[i].Status = "promoted"
			}
		}
	}
	return nil
}

func placeholders(n int) string {
	if n == 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func int64Args(ids []int64) []any {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return args
}

// fetchStatusColumn returns id -> status for the given ids from table's
// `status` column. table is always a fixed literal from a call site in this
// file, never user input, so building the query with it is safe.
func (s *Store) fetchStatusColumn(table string, ids []int64) (map[int64]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := s.DB.Query(`SELECT id, status FROM `+table+` WHERE id IN (`+placeholders(len(ids))+`)`, int64Args(ids)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var id int64
		var status string
		if err := rows.Scan(&id, &status); err != nil {
			return nil, err
		}
		out[id] = status
	}
	return out, rows.Err()
}

type memoryStatusRow struct {
	status string
	stale  bool
}

func (s *Store) fetchMemoryStatuses(ids []int64) (map[int64]memoryStatusRow, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := s.DB.Query(`SELECT id, status, stale FROM memory WHERE id IN (`+placeholders(len(ids))+`)`, int64Args(ids)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]memoryStatusRow{}
	for rows.Next() {
		var id int64
		var status string
		var stale int
		if err := rows.Scan(&id, &status, &stale); err != nil {
			return nil, err
		}
		out[id] = memoryStatusRow{status: status, stale: stale != 0}
	}
	return out, rows.Err()
}

func (s *Store) fetchNotePromotions(ids []int64) (map[int64]bool, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := s.DB.Query(`SELECT id, promoted_to FROM notes WHERE id IN (`+placeholders(len(ids))+`)`, int64Args(ids)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		var promotedTo sql.NullString
		if err := rows.Scan(&id, &promotedTo); err != nil {
			return nil, err
		}
		out[id] = promotedTo.Valid
	}
	return out, rows.Err()
}
