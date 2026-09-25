package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Decision struct {
	ID           int64
	Title        string
	Status       string
	Scope        sql.NullString
	SupersededBy sql.NullInt64
	Context      sql.NullString
	DecisionText sql.NullString
	Rationale    sql.NullString
	ProjectID    sql.NullInt64
	ActorType    sql.NullString
	ActorID      sql.NullString
	Model        sql.NullString
	CreatedAt    string
	UpdatedAt    string
}

var ValidDecisionStatuses = map[string]bool{
	"proposed": true, "accepted": true, "rejected": true, "deprecated": true, "superseded": true,
}

type DecisionOpts struct {
	Scope     string
	Context   string
	Decision  string
	Rationale string
	ProjectID *int64
}

func (s *Store) AddDecision(title string, opts DecisionOpts) (int64, error) {
	title = scrubText(title)
	opts.Context, opts.Decision, opts.Rationale = scrubText(opts.Context), scrubText(opts.Decision), scrubText(opts.Rationale)
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.DB.Exec(
		`INSERT INTO decisions (title, status, scope, context, decision, rationale, project_id,
			actor_type, actor_id, model, created_at, updated_at)
		 VALUES (?, 'proposed', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		title, nullStr(opts.Scope), nullStr(opts.Context), nullStr(opts.Decision), nullStr(opts.Rationale),
		nullInt(opts.ProjectID), s.Actor.Type, s.Actor.ID, nullStr(s.Actor.Model), now, now,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	body := title + " " + opts.Context + " " + opts.Decision + " " + opts.Rationale
	if err := s.indexForSearch("decision", id, nullInt(opts.ProjectID), body); err != nil {
		return 0, err
	}
	s.indexForSemanticSearch("decision", id, nullInt(opts.ProjectID), body)
	return id, nil
}

// ErrAgentCannotAcceptDecision is returned when an agent actor tries to accept
// a decision without an approval token. Same rule as spec approval: accepting
// is what makes a decision something later work is checked against.
var ErrAgentCannotAcceptDecision = errors.New("an agent cannot accept a decision: a person must decide")

// AcceptDecision marks a decision accepted. Refused for an agent actor unless
// the approval token is enabled and presented.
func (s *Store) AcceptDecision(id int64, token string) error {
	if err := s.requirePerson(token, ErrAgentCannotAcceptDecision); err != nil {
		return err
	}
	return s.setDecisionStatus(id, "accepted")
}

// ErrAgentCannotRetireDecision is returned when an agent actor tries to reject or
// supersede an accepted decision without an approval token. Retiring one removes
// a rule later work is checked against, so it is as much a person's call as
// accepting it was. Rejecting or superseding a decision still only proposed stays
// open: nothing depends on it yet.
var ErrAgentCannotRetireDecision = errors.New("an agent cannot reject or supersede an accepted decision: a person must decide (propose a replacement with `acline decision add`)")

// retireDecision refuses an agent without the approval token when decision id is
// accepted.
func (s *Store) retireDecision(id int64, token string) error {
	d, err := s.GetDecision(id)
	if err != nil {
		return err
	}
	if d.Status != "accepted" {
		return nil
	}
	return s.requirePerson(token, ErrAgentCannotRetireDecision)
}

// RejectDecision marks a decision rejected; see ErrAgentCannotRetireDecision.
func (s *Store) RejectDecision(id int64, token string) error {
	if err := s.retireDecision(id, token); err != nil {
		return err
	}
	return s.setDecisionStatus(id, "rejected")
}

func (s *Store) setDecisionStatus(id int64, status string) error {
	if !ValidDecisionStatuses[status] {
		return fmt.Errorf("invalid decision status %q", status)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	return s.changeWithEvent("decision", id, "decision_recorded", fmt.Sprintf("decision #%d %s", id, status),
		`UPDATE decisions SET status = ?, updated_at = ? WHERE id = ?`, status, now, id)
}

// SupersedeDecision marks id as superseded by newID; see
// ErrAgentCannotRetireDecision.
func (s *Store) SupersedeDecision(id, newID int64, token string) error {
	if id == newID {
		return fmt.Errorf("decision #%d cannot supersede itself", id)
	}
	if _, err := s.GetDecision(newID); err != nil {
		return fmt.Errorf("superseding decision: %w", err)
	}
	if err := s.retireDecision(id, token); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	return s.changeWithEvent("decision", id, "decision_recorded", fmt.Sprintf("decision #%d superseded by #%d", id, newID),
		`UPDATE decisions SET status = 'superseded', superseded_by = ?, updated_at = ? WHERE id = ?`, newID, now, id)
}

const decisionColumns = `id, title, status, scope, superseded_by, context, decision, rationale, project_id,
	actor_type, actor_id, model, created_at, updated_at`

func scanDecision(row interface{ Scan(...any) error }) (*Decision, error) {
	d := &Decision{}
	if err := row.Scan(&d.ID, &d.Title, &d.Status, &d.Scope, &d.SupersededBy, &d.Context,
		&d.DecisionText, &d.Rationale, &d.ProjectID, &d.ActorType, &d.ActorID, &d.Model,
		&d.CreatedAt, &d.UpdatedAt); err != nil {
		return nil, err
	}
	return d, nil
}

func (s *Store) GetDecision(id int64) (*Decision, error) {
	row := s.DB.QueryRow(`SELECT `+decisionColumns+` FROM decisions WHERE id = ?`, id)
	d, err := scanDecision(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("decision #%d: %w", id, ErrNotFound)
	}
	return d, err
}

func (s *Store) ListDecisions(status string, projectID *int64) ([]Decision, error) {
	q := `SELECT ` + decisionColumns + ` FROM decisions`
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
	var out []Decision
	for rows.Next() {
		d, err := scanDecision(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}
