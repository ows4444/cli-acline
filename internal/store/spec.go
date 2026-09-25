package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Spec is the versioned statement of intent a task is derived from.
// Spec-driven development treats this, not the code, as the anchor.
type Spec struct {
	ID        int64
	Title     string
	Body      sql.NullString
	Status    string
	Version   int
	ProjectID sql.NullInt64
	ActorType sql.NullString
	ActorID   sql.NullString
	Model     sql.NullString
	CreatedAt string
	UpdatedAt string
}

var ValidSpecStatuses = map[string]bool{
	"draft": true, "approved": true, "implemented": true, "superseded": true,
}

// SpecOpts is variadic on AddSpec so existing 2-arg call sites keep
// compiling unchanged; only callers that need project scoping pass one.
type SpecOpts struct {
	ProjectID *int64
}

func (s *Store) AddSpec(title, body string, opts ...SpecOpts) (int64, error) {
	title, body = scrubText(title), scrubText(body)
	var opt SpecOpts
	if len(opts) > 0 {
		opt = opts[0]
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.DB.Exec(
		`INSERT INTO specs (title, body, status, version, project_id, actor_type, actor_id, model, created_at, updated_at)
		 VALUES (?, ?, 'draft', 1, ?, ?, ?, ?, ?, ?)`,
		title, nullStr(body), nullInt(opt.ProjectID), s.Actor.Type, s.Actor.ID, nullStr(s.Actor.Model), now, now,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	specBody := title + " " + body
	if err := s.indexForSearch("spec", id, nullInt(opt.ProjectID), specBody); err != nil {
		return 0, err
	}
	s.indexForSemanticSearch("spec", id, nullInt(opt.ProjectID), specBody)
	return id, nil
}

// ErrAgentCannotApproveSpec is returned when an agent actor tries to approve a
// spec without an approval token. Approving turns a spec into something a plan
// or task can be derived from, so — like plan approval — it needs a person, not
// just an agent that decided its own draft was good.
var ErrAgentCannotApproveSpec = errors.New("an agent cannot approve a spec: a person must decide")

// ApproveSpec marks a spec approved. Refused for an agent actor unless the
// approval token is enabled and presented (see requirePerson in auth.go).
func (s *Store) ApproveSpec(id int64, token string) error {
	if err := s.requirePerson(token, ErrAgentCannotApproveSpec); err != nil {
		return err
	}
	return s.setSpecStatus(id, "approved")
}

func (s *Store) setSpecStatus(id int64, status string) error {
	if !ValidSpecStatuses[status] {
		return fmt.Errorf("invalid spec status %q", status)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	return s.changeWithEvent("spec", id, "spec_recorded", fmt.Sprintf("spec #%d %s", id, status),
		`UPDATE specs SET status = ?, updated_at = ? WHERE id = ?`, status, now, id)
}

// ReviseSpec replaces the body and bumps the version, returning the new version.
//
// A spec that was approved (or implemented) stops being approved when its text
// changes: the approval was of the old text, and approved-spec text feeds
// context export, briefs and orchestrator prompts as authoritative. It goes
// back to draft and needs approving again. What the spec said before is kept in
// spec_versions, and the change is recorded as a spec_revised event, in the same
// transaction as the revision.
func (s *Store) ReviseSpec(id int64, body string) (int, error) {
	body = scrubText(body)
	cur, err := s.GetSpec(id)
	if err != nil {
		return 0, err
	}
	newStatus := cur.Status
	if cur.Status == "approved" || cur.Status == "implemented" {
		newStatus = "draft"
	}
	sessionID := s.currentSessionID()
	now := time.Now().UTC().Format(time.RFC3339)

	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(
		`INSERT INTO spec_versions (spec_id, version, title, body, status, actor_type, actor_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, cur.Version, cur.Title, cur.Body, cur.Status, s.Actor.Type, s.Actor.ID, now,
	); err != nil {
		return 0, err
	}
	res, err := tx.Exec(
		`UPDATE specs SET body = ?, status = ?, version = version + 1, updated_at = ? WHERE id = ?`,
		nullStr(body), newStatus, now, id,
	)
	if err != nil {
		return 0, err
	}
	if err := mustExist(res, "spec", id); err != nil {
		return 0, err
	}
	msg := fmt.Sprintf("spec #%d v%d -> v%d (status %s -> %s)", id, cur.Version, cur.Version+1, cur.Status, newStatus)
	if _, err := s.logEventTx(tx, nil, sessionID, nil, "spec_revised", msg); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	// The indexed text is title + body; keep search (and, when configured,
	// semantic search) in step with the revision.
	text := cur.Title + " " + body
	if err := s.reindexForSearch("spec", id, cur.ProjectID, text); err != nil {
		return 0, err
	}
	s.indexForSemanticSearch("spec", id, cur.ProjectID, text)
	return cur.Version + 1, nil
}

// SpecVersion is what a spec said before one revision.
type SpecVersion struct {
	SpecID    int64
	Version   int
	Title     string
	Body      string
	Status    string // the spec's status when this version was replaced
	ActorType string
	ActorID   string
	CreatedAt string // when it was replaced
}

// ListSpecVersions returns a spec's earlier versions, oldest first. The current
// text is on the spec itself.
func (s *Store) ListSpecVersions(specID int64) ([]SpecVersion, error) {
	rows, err := s.DB.Query(
		`SELECT spec_id, version, title, COALESCE(body, ''), status, COALESCE(actor_type, ''), COALESCE(actor_id, ''), created_at
		 FROM spec_versions WHERE spec_id = ? ORDER BY version`, specID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SpecVersion
	for rows.Next() {
		var v SpecVersion
		if err := rows.Scan(&v.SpecID, &v.Version, &v.Title, &v.Body, &v.Status, &v.ActorType, &v.ActorID, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

const specColumns = `id, title, body, status, version, project_id, actor_type, actor_id, model, created_at, updated_at`

func scanSpec(row interface{ Scan(...any) error }) (*Spec, error) {
	sp := &Spec{}
	if err := row.Scan(&sp.ID, &sp.Title, &sp.Body, &sp.Status, &sp.Version, &sp.ProjectID,
		&sp.ActorType, &sp.ActorID, &sp.Model, &sp.CreatedAt, &sp.UpdatedAt); err != nil {
		return nil, err
	}
	return sp, nil
}

func (s *Store) GetSpec(id int64) (*Spec, error) {
	row := s.DB.QueryRow(`SELECT `+specColumns+` FROM specs WHERE id = ?`, id)
	sp, err := scanSpec(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("spec #%d: %w", id, ErrNotFound)
	}
	return sp, err
}

func (s *Store) ListSpecs(status string, projectID *int64) ([]Spec, error) {
	q := `SELECT ` + specColumns + ` FROM specs`
	var where []string
	var args []any
	if status != "" {
		where = append(where, `status = ?`)
		args = append(args, status)
	}
	if projectID != nil {
		where = append(where, `project_id = ?`)
		args = append(args, *projectID)
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
	var out []Spec
	for rows.Next() {
		sp, err := scanSpec(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sp)
	}
	return out, rows.Err()
}

// TasksForSpec lists the tasks derived from a spec.
func (s *Store) TasksForSpec(specID int64) ([]Task, error) {
	rows, err := s.DB.Query(`SELECT `+taskColumns+` FROM tasks WHERE spec_id = ? ORDER BY id`, specID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}
