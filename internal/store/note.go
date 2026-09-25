package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type Note struct {
	ID         int64
	ProjectID  sql.NullInt64
	Body       string
	Source     string
	ActorType  sql.NullString
	ActorID    sql.NullString
	Model      sql.NullString
	RoleID     sql.NullInt64
	CreatedAt  string
	PromotedTo sql.NullString
	PromotedID sql.NullInt64
}

var ValidNoteSources = map[string]bool{"manual": true, "conversation": true}

// AddNote records a fast, unstructured capture — the landing zone before a
// human (or `acline reflect`) decides whether it's durable enough to promote
// into a decision or memory entry.
func (s *Store) AddNote(projectID *int64, body, source string) (int64, error) {
	return s.AddNoteWithRole(projectID, nil, body, source)
}

// AddNoteWithRole is AddNote plus an explicit role_id (see Store.ResolveRole).
func (s *Store) AddNoteWithRole(projectID, roleID *int64, body, source string) (int64, error) {
	body = scrubText(body)
	if source == "" {
		source = "manual"
	}
	if !ValidNoteSources[source] {
		return 0, fmt.Errorf("invalid note source %q", source)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.DB.Exec(
		`INSERT INTO notes (project_id, body, source, actor_type, actor_id, model, role_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		nullInt(projectID), body, source, s.Actor.Type, s.Actor.ID, nullStr(s.Actor.Model), nullInt(roleID), now,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := s.indexForSearch("note", id, nullInt(projectID), body); err != nil {
		return 0, err
	}
	// Deliberately not embedded (unlike decision/memory/spec): notes are
	// captured automatically and often in bulk by the session-end and
	// pre-compact hooks' MEMORY_LOG scan (`acline hook`), not a deliberate user action, so
	// embedding every one would mean a silent per-note API call/cost the
	// user never asked for. A note that earns its keep gets embedded once
	// promoted into a memory or decision, via those Add* calls instead.
	return id, nil
}

const noteColumns = `id, project_id, body, source, actor_type, actor_id, model, role_id, created_at, promoted_to, promoted_id`

func scanNote(row interface{ Scan(...any) error }) (*Note, error) {
	n := &Note{}
	if err := row.Scan(&n.ID, &n.ProjectID, &n.Body, &n.Source, &n.ActorType, &n.ActorID, &n.Model, &n.RoleID,
		&n.CreatedAt, &n.PromotedTo, &n.PromotedID); err != nil {
		return nil, err
	}
	return n, nil
}

func (s *Store) GetNote(id int64) (*Note, error) {
	row := s.DB.QueryRow(`SELECT `+noteColumns+` FROM notes WHERE id = ?`, id)
	n, err := scanNote(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("note #%d: %w", id, ErrNotFound)
	}
	return n, err
}

type NoteFilter struct {
	ProjectID      *int64
	UnpromotedOnly bool
}

func (s *Store) ListNotes(f NoteFilter) ([]Note, error) {
	q := `SELECT ` + noteColumns + ` FROM notes`
	var where []string
	var args []any
	if f.ProjectID != nil {
		where = append(where, `project_id = ?`)
		args = append(args, *f.ProjectID)
	}
	if f.UnpromotedOnly {
		where = append(where, `promoted_to IS NULL`)
	}
	if len(where) > 0 {
		q += ` WHERE ` + strings.Join(where, ` AND `)
	}
	q += ` ORDER BY id`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Note
	for rows.Next() {
		n, err := scanNote(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *n)
	}
	return out, rows.Err()
}

// MarkPromoted records that a note produced a decision or memory row, so it
// drops out of the unpromoted queue `acline reflect` works from.
func (s *Store) MarkPromoted(id int64, kind string, promotedID int64) error {
	res, err := s.DB.Exec(`UPDATE notes SET promoted_to = ?, promoted_id = ? WHERE id = ?`, kind, promotedID, id)
	if err != nil {
		return err
	}
	return mustExist(res, "note", id)
}
