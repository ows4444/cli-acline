package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type Milestone struct {
	ID          int64
	Name        string
	Description sql.NullString
	Status      string
	TargetDate  sql.NullString
	ProjectID   sql.NullInt64
	ActorType   sql.NullString
	ActorID     sql.NullString
	Model       sql.NullString
	CreatedAt   string
	UpdatedAt   string
}

// MilestoneProgress summarizes the tasks assigned to a milestone.
type MilestoneProgress struct {
	Total     int
	Done      int
	Cancelled int
}

var ValidMilestoneStatuses = map[string]bool{
	"planned": true, "active": true, "done": true, "cancelled": true,
}

type MilestoneOpts struct {
	Description string
	TargetDate  string
	ProjectID   *int64
}

// dateLayout is the accepted --target format: a plain calendar date, not a
// timestamp, since a milestone is due on a day, not at an instant.
const dateLayout = "2006-01-02"

func validTargetDate(targetDate string) error {
	if targetDate == "" {
		return nil
	}
	if _, err := time.Parse(dateLayout, targetDate); err != nil {
		return fmt.Errorf("invalid target date %q: expected YYYY-MM-DD", targetDate)
	}
	return nil
}

func (s *Store) AddMilestone(name string, opts MilestoneOpts) (int64, error) {
	if err := validTargetDate(opts.TargetDate); err != nil {
		return 0, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.DB.Exec(
		`INSERT INTO milestones (name, description, status, target_date, project_id, actor_type, actor_id, model, created_at, updated_at)
		 VALUES (?, ?, 'planned', ?, ?, ?, ?, ?, ?, ?)`,
		name, nullStr(opts.Description), nullStr(opts.TargetDate), nullInt(opts.ProjectID),
		s.Actor.Type, s.Actor.ID, nullStr(s.Actor.Model), now, now,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

const milestoneColumns = `id, name, description, status, target_date, project_id, actor_type, actor_id, model, created_at, updated_at`

func scanMilestone(row interface{ Scan(...any) error }) (*Milestone, error) {
	m := &Milestone{}
	err := row.Scan(&m.ID, &m.Name, &m.Description, &m.Status, &m.TargetDate, &m.ProjectID,
		&m.ActorType, &m.ActorID, &m.Model, &m.CreatedAt, &m.UpdatedAt)
	return m, err
}

func (s *Store) GetMilestone(id int64) (*Milestone, error) {
	row := s.DB.QueryRow(`SELECT `+milestoneColumns+` FROM milestones WHERE id = ?`, id)
	m, err := scanMilestone(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("milestone #%d: %w", id, ErrNotFound)
		}
		return nil, err
	}
	return m, nil
}

func (s *Store) ListMilestones(status string, projectID *int64) ([]Milestone, error) {
	q := `SELECT ` + milestoneColumns + ` FROM milestones`
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
	q += ` ORDER BY (target_date IS NULL), target_date, id`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Milestone
	for rows.Next() {
		m, err := scanMilestone(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

func (s *Store) SetMilestoneStatus(id int64, status string) error {
	if !ValidMilestoneStatuses[status] {
		return fmt.Errorf("invalid milestone status %q", status)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.DB.Exec(`UPDATE milestones SET status = ?, updated_at = ? WHERE id = ?`, status, now, id)
	if err != nil {
		return err
	}
	return mustExist(res, "milestone", id)
}

func (s *Store) SetMilestoneTarget(id int64, targetDate string) error {
	if err := validTargetDate(targetDate); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.DB.Exec(`UPDATE milestones SET target_date = ?, updated_at = ? WHERE id = ?`, nullStr(targetDate), now, id)
	if err != nil {
		return err
	}
	return mustExist(res, "milestone", id)
}

// SetTaskMilestone assigns (or, with milestoneID nil, clears) the milestone a task belongs to.
func (s *Store) SetTaskMilestone(taskID int64, milestoneID *int64) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.DB.Exec(`UPDATE tasks SET milestone_id = ?, updated_at = ? WHERE id = ?`, nullInt(milestoneID), now, taskID)
	if err != nil {
		return err
	}
	return mustExist(res, "task", taskID)
}

func (s *Store) MilestoneTasks(milestoneID int64) ([]Task, error) {
	rows, err := s.DB.Query(`SELECT `+taskColumns+` FROM tasks WHERE milestone_id = ? ORDER BY
		CASE priority WHEN 'urgent' THEN 0 WHEN 'high' THEN 1 WHEN 'normal' THEN 2 ELSE 3 END, id`, milestoneID)
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

func (s *Store) GetMilestoneProgress(milestoneID int64) (MilestoneProgress, error) {
	var p MilestoneProgress
	row := s.DB.QueryRow(
		`SELECT COUNT(*),
			SUM(CASE WHEN status = 'done' THEN 1 ELSE 0 END),
			SUM(CASE WHEN status = 'cancelled' THEN 1 ELSE 0 END)
		 FROM tasks WHERE milestone_id = ?`, milestoneID)
	var done, cancelled sql.NullInt64
	if err := row.Scan(&p.Total, &done, &cancelled); err != nil {
		return p, err
	}
	p.Done = int(done.Int64)
	p.Cancelled = int(cancelled.Int64)
	return p, nil
}
