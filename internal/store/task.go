package store

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

type Task struct {
	ID             int64
	Title          string
	Description    string
	Status         string
	Priority       string
	Area           sql.NullString
	Type           sql.NullString
	Risk           string
	Autonomy       string
	Deferred       bool
	DeferredReason sql.NullString
	RevisitTrigger sql.NullString
	BlockedReason  sql.NullString // why the task is blocked; NULL unless status is blocked and a reason was given
	SpecID         sql.NullInt64
	DecisionID     sql.NullInt64
	ProjectID      sql.NullInt64
	ParentID       sql.NullInt64
	MilestoneID    sql.NullInt64
	ActorType      sql.NullString
	ActorID        sql.NullString
	Model          sql.NullString
	RoleID         sql.NullInt64
	CreatedAt      string
	UpdatedAt      string
	CompletedAt    sql.NullString
}

type Criterion struct {
	ID        int64
	TaskID    int64
	Text      string
	Pattern   sql.NullString
	Done      bool
	CreatedAt string
}

type Link struct {
	ID            int64
	TaskID        int64
	RelatedTaskID int64
	Relation      string
	CreatedAt     string
}

var ValidStatuses = map[string]bool{
	"backlog": true, "todo": true, "in_progress": true,
	"blocked": true, "review": true, "done": true, "cancelled": true,
}

var ValidPriorities = map[string]bool{
	"low": true, "normal": true, "high": true, "urgent": true,
}

var ValidTaskTypes = map[string]bool{
	"bug": true, "refactor": true, "test": true, "architecture": true, "security": true,
	"performance": true, "reliability": true, "contract": true, "database": true,
	"messaging": true, "ui_ux": true, "accessibility": true, "feature": true,
	"debt": true, "docs": true, "devex": true,
}

var ValidRisks = map[string]bool{
	"low": true, "medium": true, "high": true, "critical": true,
}

// ValidAutonomy: hitl = approve before acting, hotl = act then be reviewed,
var ValidAutonomy = map[string]bool{
	"hitl": true, "hotl": true, "auto": true,
}

var ValidLinkRelations = map[string]bool{
	"depends_on": true, "blocks": true, "related": true,
}

const taskColumns = `id, title, description, status, priority, area, type, risk, autonomy,
	deferred, deferred_reason, revisit_trigger, blocked_reason, spec_id, decision_id, project_id, parent_id, milestone_id,
	actor_type, actor_id, model, role_id, created_at, updated_at, completed_at`

func scanTask(row interface{ Scan(...any) error }) (*Task, error) {
	t := &Task{}
	var desc sql.NullString
	var deferred int
	if err := row.Scan(&t.ID, &t.Title, &desc, &t.Status, &t.Priority, &t.Area, &t.Type,
		&t.Risk, &t.Autonomy, &deferred, &t.DeferredReason, &t.RevisitTrigger, &t.BlockedReason,
		&t.SpecID, &t.DecisionID, &t.ProjectID, &t.ParentID, &t.MilestoneID,
		&t.ActorType, &t.ActorID, &t.Model, &t.RoleID,
		&t.CreatedAt, &t.UpdatedAt, &t.CompletedAt); err != nil {
		return nil, err
	}
	t.Description = desc.String
	t.Deferred = deferred != 0
	return t, nil
}

type TaskOpts struct {
	Area        string
	Type        string
	Risk        string
	Autonomy    string
	SpecID      *int64
	ProjectID   *int64
	ParentID    *int64
	MilestoneID *int64
	RoleID      *int64
	// Token is the approval token, consulted only when creating a task at
	// autonomy "auto" (see ErrAgentCannotLoosenTask).
	Token string
}

func (s *Store) AddTask(title, description, priority string, opts TaskOpts) (int64, error) {
	return s.insertTask(s.DB, title, description, priority, opts)
}

// insertTask is AddTask on any executor, so a caller holding a transaction
// (plan approval) can create tasks atomically with everything else it does.
func (s *Store) insertTask(ex sqlExecer, title, description, priority string, opts TaskOpts) (int64, error) {
	title, description = scrubText(title), scrubText(description)
	if priority == "" {
		priority = "normal"
	}
	if !ValidPriorities[priority] {
		return 0, fmt.Errorf("invalid priority %q", priority)
	}
	if opts.Type != "" && !ValidTaskTypes[opts.Type] {
		return 0, fmt.Errorf("invalid type %q", opts.Type)
	}
	if opts.Risk == "" {
		opts.Risk = "low"
	}
	if !ValidRisks[opts.Risk] {
		return 0, fmt.Errorf("invalid risk %q", opts.Risk)
	}
	if opts.Autonomy == "" {
		opts.Autonomy = "hotl"
	}
	if !ValidAutonomy[opts.Autonomy] {
		return 0, fmt.Errorf("invalid autonomy %q", opts.Autonomy)
	}
	if opts.Autonomy == "auto" {
		if err := s.requirePerson(opts.Token, ErrAgentCannotLoosenTask); err != nil {
			return 0, err
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := ex.Exec(
		`INSERT INTO tasks (title, description, status, priority, area, type, risk, autonomy,
			spec_id, project_id, parent_id, milestone_id, actor_type, actor_id, model, role_id, created_at, updated_at)
		 VALUES (?, ?, 'todo', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		title, description, priority, nullStr(opts.Area), nullStr(opts.Type), opts.Risk, opts.Autonomy,
		nullInt(opts.SpecID), nullInt(opts.ProjectID), nullInt(opts.ParentID), nullInt(opts.MilestoneID),
		s.Actor.Type, s.Actor.ID, nullStr(s.Actor.Model), nullInt(opts.RoleID), now, now,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func nullInt(p *int64) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *p, Valid: true}
}

func nullStr(v string) sql.NullString {
	if v == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: v, Valid: true}
}

// mustExist turns a no-op UPDATE into an error, so operating on a
// non-existent id fails loudly instead of silently succeeding.
func mustExist(res sql.Result, kind string, id int64) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%s #%d: %w", kind, id, ErrNotFound)
	}
	return nil
}

func updateTaskStatus(ex sqlExecer, id int64, status string) error {
	if !ValidStatuses[status] {
		return fmt.Errorf("invalid status %q", status)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	var completedAt any
	if status == "done" {
		completedAt = now
	} else {
		completedAt = nil
	}
	res, err := ex.Exec(
		`UPDATE tasks SET status = ?, updated_at = ?, completed_at = ?, blocked_reason = NULL WHERE id = ?`,
		status, now, completedAt, id,
	)
	if err != nil {
		return err
	}
	return mustExist(res, "task", id)
}

// AssignRole sets the task's current owning role (tasks.role_id) -- the
// workflow-stage handoff (acline spec #2): a task's role_id IS who's
// currently responsible for it, and its history of assignments (via
// role_assigned events, logged by the caller) is the workflow trail.
// roleID may be nil to unassign. Returns the task's previous role_id, so
// the caller can log a meaningful old->new message.
func (s *Store) AssignRole(id int64, roleID *int64) (sql.NullInt64, error) {
	t, err := s.GetTask(id)
	if err != nil {
		return sql.NullInt64{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.DB.Exec(`UPDATE tasks SET role_id = ?, updated_at = ? WHERE id = ?`, nullInt(roleID), now, id)
	if err != nil {
		return sql.NullInt64{}, err
	}
	if err := mustExist(res, "task", id); err != nil {
		return sql.NullInt64{}, err
	}
	return t.RoleID, nil
}

func (s *Store) UpdateTaskPriority(id int64, priority string) error {
	if !ValidPriorities[priority] {
		return fmt.Errorf("invalid priority %q", priority)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	return s.changeTaskWithEvent("task", id, &id, "task_updated", "priority -> "+priority,
		`UPDATE tasks SET priority = ?, updated_at = ? WHERE id = ?`, priority, now, id)
}

func (s *Store) UpdateTaskArea(id int64, area string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	return s.changeTaskWithEvent("task", id, &id, "task_updated", "area -> "+area,
		`UPDATE tasks SET area = ?, updated_at = ? WHERE id = ?`, area, now, id)
}

func (s *Store) UpdateTaskType(id int64, taskType string) error {
	if !ValidTaskTypes[taskType] {
		return fmt.Errorf("invalid type %q", taskType)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	return s.changeTaskWithEvent("task", id, &id, "task_updated", "type -> "+taskType,
		`UPDATE tasks SET type = ?, updated_at = ? WHERE id = ?`, taskType, now, id)
}

// ErrAgentCannotLoosenTask is returned when an agent tries to lower a task's
// risk, loosen its autonomy (hitl -> hotl -> auto), or create a task at
// autonomy "auto". Risk and autonomy decide whether completing the task needs a
// person's approval, so relaxing either is the same kind of decision as
// approving: it needs a person, or the approval token when one is enabled.
// Raising risk and tightening autonomy are always allowed.
var ErrAgentCannotLoosenTask = errors.New("an agent cannot lower a task's risk or loosen its autonomy (auto is earned from a measured eval): a person must decide")

var (
	riskRank     = map[string]int{"low": 0, "medium": 1, "high": 2, "critical": 3}
	autonomyRank = map[string]int{"hitl": 0, "hotl": 1, "auto": 2}
)

// UpdateTaskRisk sets a task's risk. Lowering it is privileged (see
// ErrAgentCannotLoosenTask); every change is recorded as a risk_changed event.
func (s *Store) UpdateTaskRisk(id int64, risk string) error {
	return s.UpdateTaskRiskWithToken(id, risk, "")
}

// UpdateTaskRiskWithToken is UpdateTaskRisk with the approval token.
func (s *Store) UpdateTaskRiskWithToken(id int64, risk, token string) error {
	if !ValidRisks[risk] {
		return fmt.Errorf("invalid risk %q", risk)
	}
	return s.setTaskLevel(id, "risk", risk, token)
}

// UpdateTaskAutonomy sets a task's autonomy. Loosening it is privileged (see
// ErrAgentCannotLoosenTask); every change is recorded as an autonomy_changed
// event. PromoteAutonomy is the eval-backed route to "auto".
func (s *Store) UpdateTaskAutonomy(id int64, autonomy string) error {
	return s.UpdateTaskAutonomyWithToken(id, autonomy, "")
}

// UpdateTaskAutonomyWithToken is UpdateTaskAutonomy with the approval token.
func (s *Store) UpdateTaskAutonomyWithToken(id int64, autonomy, token string) error {
	if !ValidAutonomy[autonomy] {
		return fmt.Errorf("invalid autonomy %q", autonomy)
	}
	return s.setTaskLevel(id, "autonomy", autonomy, token)
}

// setTaskLevel changes tasks.risk or tasks.autonomy and records the change in
// the same transaction, so the value and its audit trail cannot diverge.
func (s *Store) setTaskLevel(id int64, column, to, token string) error {
	t, err := s.GetTask(id)
	if err != nil {
		return err
	}
	from, loosening := t.Risk, riskRank[to] < riskRank[t.Risk]
	if column == "autonomy" {
		from, loosening = t.Autonomy, autonomyRank[to] > autonomyRank[t.Autonomy]
	}
	if from == to {
		return nil
	}
	if loosening {
		if err := s.requirePerson(token, ErrAgentCannotLoosenTask); err != nil {
			return err
		}
	}
	sessionID := s.currentSessionID()
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// column is one of the two literals above, never caller input.
	res, err := tx.Exec(`UPDATE tasks SET `+column+` = ?, updated_at = ? WHERE id = ?`,
		to, time.Now().UTC().Format(time.RFC3339), id)
	if err != nil {
		return err
	}
	if err := mustExist(res, "task", id); err != nil {
		return err
	}
	if _, err := s.logEventTx(tx, &id, sessionID, nil, column+"_changed", fmt.Sprintf("%s %s -> %s", column, from, to)); err != nil {
		return err
	}
	return tx.Commit()
}

// DeferTask sets or clears the deferred disposition, independent of status.
func (s *Store) DeferTask(id int64, deferred bool, reason, revisitTrigger string) error {
	reason, revisitTrigger = scrubText(reason), scrubText(revisitTrigger)
	msg := "deferred"
	if !deferred {
		msg = "no longer deferred"
	} else if reason != "" {
		msg += ": " + reason
	}
	now := time.Now().UTC().Format(time.RFC3339)
	return s.changeTaskWithEvent("task", id, &id, "task_deferred", msg,
		`UPDATE tasks SET deferred = ?, deferred_reason = ?, revisit_trigger = ?, updated_at = ? WHERE id = ?`,
		deferred, nullStr(reason), nullStr(revisitTrigger), now, id)
}

func (s *Store) GetTask(id int64) (*Task, error) {
	row := s.DB.QueryRow(`SELECT `+taskColumns+` FROM tasks WHERE id = ?`, id)
	t, err := scanTask(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("task #%d: %w", id, ErrNotFound)
	}
	return t, err
}

type TaskFilter struct {
	Status    string
	Area      string
	Risk      string
	All       bool
	ProjectID *int64
}

// NeedsAttention is the one definition of "this task deserves a look" shared by
// the CLI dashboard, the MCP dashboard and (via the tasks' needs_attention
// field) the VS Code tree badge and status bar -- they previously each used a
// different rule, so the same word meant different sets of tasks on one screen.
// An open task needs attention when it is blocked, urgent/high priority, or
// high/critical risk.
func (t Task) NeedsAttention() bool {
	if t.Status == "done" || t.Status == "cancelled" {
		return false
	}
	return t.Status == "blocked" ||
		t.Priority == "urgent" || t.Priority == "high" ||
		t.Risk == "high" || t.Risk == "critical"
}

func (s *Store) ListTasks(f TaskFilter) ([]Task, error) {
	q := `SELECT ` + taskColumns + ` FROM tasks`
	var where []string
	var args []any
	if f.Status != "" {
		where = append(where, `status = ?`)
		args = append(args, f.Status)
	} else if !f.All {
		where = append(where, `status NOT IN ('done', 'cancelled')`)
	}
	if f.Area != "" {
		where = append(where, `area = ?`)
		args = append(args, f.Area)
	}
	if f.Risk != "" {
		where = append(where, `risk = ?`)
		args = append(args, f.Risk)
	}
	if f.ProjectID != nil {
		where = append(where, `project_id = ?`)
		args = append(args, *f.ProjectID)
	}
	if len(where) > 0 {
		q += ` WHERE ` + strings.Join(where, ` AND `)
	}
	q += ` ORDER BY
		CASE priority WHEN 'urgent' THEN 0 WHEN 'high' THEN 1 WHEN 'normal' THEN 2 ELSE 3 END,
		id`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, *t)
	}
	return tasks, rows.Err()
}

// --- acceptance criteria (EARS-aware) ---

var earsPatterns = []struct {
	name string
	re   *regexp.Regexp
}{
	{"event", regexp.MustCompile(`(?i)^\s*when\s+.+,\s*the\s+.+\s+shall\s+.+`)},
	{"state", regexp.MustCompile(`(?i)^\s*while\s+.+,\s*the\s+.+\s+shall\s+.+`)},
	{"unwanted", regexp.MustCompile(`(?i)^\s*if\s+.+,\s*then\s+the\s+.+\s+shall\s+.+`)},
	{"optional", regexp.MustCompile(`(?i)^\s*where\s+.+,\s*the\s+.+\s+shall\s+.+`)},
	{"ubiquitous", regexp.MustCompile(`(?i)^\s*the\s+.+\s+shall\s+.+`)},
}

// DetectEARSPattern classifies a criterion against the EARS templates,
// returning "" when the text follows none of them.
func DetectEARSPattern(text string) string {
	for _, p := range earsPatterns {
		if p.re.MatchString(text) {
			return p.name
		}
	}
	return ""
}

func (s *Store) AddCriterion(taskID int64, text string) (int64, string, error) {
	text = scrubText(text)
	if _, err := s.GetTask(taskID); err != nil {
		return 0, "", err
	}
	pattern := DetectEARSPattern(text)
	now := time.Now().UTC().Format(time.RFC3339)
	var id int64
	err := s.writeWithEvent(&taskID, "criterion_added", func(tx *sql.Tx) (string, error) {
		res, err := tx.Exec(
			`INSERT INTO task_criteria (task_id, text, pattern, created_at) VALUES (?, ?, ?, ?)`,
			taskID, text, nullStr(pattern), now,
		)
		if err != nil {
			return "", err
		}
		if id, err = res.LastInsertId(); err != nil {
			return "", err
		}
		return fmt.Sprintf("criterion #%d added: %s", id, text), nil
	})
	return id, pattern, err
}

// SetCriterionDone checks a criterion off (or back on). Open criteria are a gate
// warning, so each change is recorded against the task.
func (s *Store) SetCriterionDone(id int64, done bool) error {
	var taskID int64
	if err := s.DB.QueryRow(`SELECT task_id FROM task_criteria WHERE id = ?`, id).Scan(&taskID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("criterion #%d: %w", id, ErrNotFound)
		}
		return err
	}
	msg := fmt.Sprintf("criterion #%d checked", id)
	if !done {
		msg = fmt.Sprintf("criterion #%d unchecked", id)
	}
	return s.changeTaskWithEvent("criterion", id, &taskID, "criterion_checked", msg,
		`UPDATE task_criteria SET done = ? WHERE id = ?`, done, id)
}

// writeWithEvent runs write and the audit event it describes in one
// transaction; write returns the event message.
func (s *Store) writeWithEvent(taskID *int64, eventType string, write func(*sql.Tx) (string, error)) error {
	sessionID := s.currentSessionID()
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	msg, err := write(tx)
	if err != nil {
		return err
	}
	if _, err := s.logEventTx(tx, taskID, sessionID, nil, eventType, msg); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ListCriteria(taskID int64) ([]Criterion, error) {
	rows, err := s.DB.Query(`SELECT id, task_id, text, pattern, done, created_at FROM task_criteria WHERE task_id = ? ORDER BY id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Criterion
	for rows.Next() {
		var c Criterion
		var done int
		if err := rows.Scan(&c.ID, &c.TaskID, &c.Text, &c.Pattern, &done, &c.CreatedAt); err != nil {
			return nil, err
		}
		c.Done = done != 0
		out = append(out, c)
	}
	return out, rows.Err()
}

// --- task links (depends_on / blocks / related) ---

func (s *Store) AddLink(taskID, relatedTaskID int64, relation string) (int64, error) {
	if !ValidLinkRelations[relation] {
		return 0, fmt.Errorf("invalid relation %q", relation)
	}
	if err := s.validateLink(taskID, relatedTaskID, relation); err != nil {
		return 0, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	var id int64
	err := s.writeWithEvent(&taskID, "link_added", func(tx *sql.Tx) (string, error) {
		res, err := tx.Exec(
			`INSERT INTO task_links (task_id, related_task_id, relation, created_at) VALUES (?, ?, ?, ?)`,
			taskID, relatedTaskID, relation, now,
		)
		if err != nil {
			return "", err
		}
		if id, err = res.LastInsertId(); err != nil {
			return "", err
		}
		return fmt.Sprintf("%s #%d", relation, relatedTaskID), nil
	})
	return id, err
}

func (s *Store) ListLinks(taskID int64) ([]Link, error) {
	rows, err := s.DB.Query(`SELECT id, task_id, related_task_id, relation, created_at FROM task_links WHERE task_id = ? ORDER BY id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Link
	for rows.Next() {
		var l Link
		if err := rows.Scan(&l.ID, &l.TaskID, &l.RelatedTaskID, &l.Relation, &l.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
