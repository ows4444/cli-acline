package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Eval records measured accuracy for a class of work. Promotion to a higher
// autonomy level should cite one of these rather than a hunch.
type Eval struct {
	ID         int64
	TaskID     sql.NullInt64
	ProjectID  sql.NullInt64
	Suite      string
	PassRate   float64
	SampleSize sql.NullInt64
	Note       sql.NullString
	ActorType  sql.NullString
	ActorID    sql.NullString
	CreatedAt  string
}

// DefaultPromotionThreshold is the pass rate a suite must hold before a task
// can be promoted to a less supervised autonomy level.
const DefaultPromotionThreshold = 0.90

func (s *Store) AddEval(taskID, projectID *int64, suite string, passRate float64, sampleSize int64, note string) (int64, error) {
	note = scrubText(note)
	if suite == "" {
		return 0, errors.New("eval suite name is required")
	}
	if passRate < 0 || passRate > 1 {
		return 0, fmt.Errorf("pass rate %v out of range (expected 0..1)", passRate)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	var size any
	if sampleSize > 0 {
		size = sampleSize
	}
	res, err := s.DB.Exec(
		`INSERT INTO evals (task_id, project_id, suite, pass_rate, sample_size, note, actor_type, actor_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		nullInt(taskID), nullInt(projectID), suite, passRate, size, nullStr(note), s.Actor.Type, s.Actor.ID, now,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListEvals returns recorded evals, optionally scoped to a project and/or a
// suite name.
func (s *Store) ListEvals(projectID *int64, suite string, limit int) ([]Eval, error) {
	if limit <= 0 {
		limit = 50
	}
	q := `SELECT id, task_id, project_id, suite, pass_rate, sample_size, note, actor_type, actor_id, created_at FROM evals`
	var where []string
	var args []any
	if suite != "" {
		where = append(where, `suite = ?`)
		args = append(args, suite)
	}
	if projectID != nil {
		where = append(where, `project_id = ?`)
		args = append(args, *projectID)
	}
	if len(where) > 0 {
		q += ` WHERE ` + strings.Join(where, ` AND `)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Eval
	for rows.Next() {
		var e Eval
		if err := rows.Scan(&e.ID, &e.TaskID, &e.ProjectID, &e.Suite, &e.PassRate, &e.SampleSize,
			&e.Note, &e.ActorType, &e.ActorID, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// LatestEval returns the most recent eval for a suite, unscoped by
// project — autonomy promotion (PromoteAutonomy below) judges a suite's
// accuracy on its own merits regardless of which project last ran it.
func (s *Store) LatestEval(suite string) (*Eval, error) {
	evals, err := s.ListEvals(nil, suite, 1)
	if err != nil {
		return nil, err
	}
	if len(evals) == 0 {
		return nil, fmt.Errorf("no eval recorded for suite %q", suite)
	}
	return &evals[0], nil
}

// ErrAgentCannotSelfPromote: an agent may cite an eval to promote a task, but
// not one it recorded itself, nor lower the bar. `eval record` takes the pass
// rate as typed, so an agent-recorded eval followed by an agent promotion would
// be an agent granting itself autonomy with nothing measured in between.
var ErrAgentCannotSelfPromote = errors.New("an agent cannot promote autonomy on its own evidence: a person must record the eval, or run the promotion")

// PromoteAutonomy moves a task to a less supervised level, but only when the
// cited suite's latest pass rate clears the threshold. For an agent, that eval
// must have been recorded by a person and the threshold stays at least the
// default; see ErrAgentCannotSelfPromote.
func (s *Store) PromoteAutonomy(taskID int64, to, suite string, threshold float64) error {
	if !ValidAutonomy[to] {
		return fmt.Errorf("invalid autonomy %q", to)
	}
	if threshold <= 0 {
		threshold = DefaultPromotionThreshold
	}
	agent := s.Actor.Type == "agent"
	if agent && threshold < DefaultPromotionThreshold {
		return fmt.Errorf("%w: the threshold cannot go below %.0f%%", ErrAgentCannotSelfPromote, DefaultPromotionThreshold*100)
	}
	e, err := s.LatestEval(suite)
	if err != nil {
		return fmt.Errorf("cannot promote without measured accuracy: %w", err)
	}
	if agent && e.ActorType.String == "agent" {
		return fmt.Errorf("%w: suite %q's latest eval (#%d) was recorded by an agent", ErrAgentCannotSelfPromote, suite, e.ID)
	}
	if e.PassRate < threshold {
		return fmt.Errorf("suite %q pass rate %.1f%% is below the %.1f%% threshold for promotion to %s",
			suite, e.PassRate*100, threshold*100, to)
	}
	// Not UpdateTaskAutonomy: the measured eval above is what authorizes this
	// change, so it must not also demand a person. It logs its own event.
	if _, err := s.GetTask(taskID); err != nil {
		return err
	}
	res, err := s.DB.Exec(`UPDATE tasks SET autonomy = ?, updated_at = ? WHERE id = ?`,
		to, time.Now().UTC().Format(time.RFC3339), taskID)
	if err != nil {
		return err
	}
	if err := mustExist(res, "task", taskID); err != nil {
		return err
	}
	_, err = s.LogEvent(&taskID, nil, "autonomy_promoted",
		fmt.Sprintf("autonomy -> %s (suite %s at %.1f%%)", to, suite, e.PassRate*100))
	return err
}
