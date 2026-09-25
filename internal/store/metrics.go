package store

// Metrics captures the "verification tax" view: not how much was produced,
// but how much checking that production required and how often it came back.
type Metrics struct {
	TasksTotal       int
	TasksDone        int
	TasksByActor     map[string]int
	ChecksTotal      int
	ChecksFailed     int
	ApprovalsTotal   int
	Overrides        int
	Reworked         int // tasks that left 'done' after reaching it
	UnverifiedDeps   int
	PendingMemory    int
	EventsByActor    map[string]int
	TokensIn         int64
	TokensOut        int64
	CostUSD          float64
	Sessions         int
	LatestEvals      []Eval
	PolicyViolations int
	GuardDenials     int
}

func (s *Store) ComputeMetrics() (*Metrics, error) {
	return s.ComputeMetricsFor(nil)
}

// ComputeMetricsFor is ComputeMetrics for one project (nil: the whole store).
// A project's rows are its tasks and what hangs off them (checks, approvals,
// evals), its own memory, dependencies and sessions, and the events of its
// tasks and sessions, the same rule as EventFilter.ProjectID.
func (s *Store) ComputeMetricsFor(projectID *int64) (*Metrics, error) {
	m := &Metrics{
		TasksByActor:  map[string]int{},
		EventsByActor: map[string]int{},
	}
	// scoped adds the project condition for a table of kind to a query that has
	// (where = true) or lacks a WHERE clause, and returns its arguments.
	const inTasks = `task_id IN (SELECT id FROM tasks WHERE project_id = ?)`
	scoped := func(kind string, where bool) (string, []any) {
		if projectID == nil {
			return "", nil
		}
		cond, n := `project_id = ?`, 1
		switch kind {
		case "task_rows":
			cond = inTasks
		case "events":
			cond, n = `(`+inTasks+` OR session_id IN (SELECT id FROM sessions WHERE project_id = ?))`, 2
		case "dependencies":
			cond, n = `(project_id = ? OR `+inTasks+`)`, 2
		}
		args := make([]any, n)
		for i := range args {
			args[i] = *projectID
		}
		if where {
			return ` AND ` + cond, args
		}
		return ` WHERE ` + cond, args
	}
	count := func(dest *int, query, kind string, where bool) error {
		cond, args := scoped(kind, where)
		return s.DB.QueryRow(query+cond, args...).Scan(dest)
	}

	if err := count(&m.TasksTotal, `SELECT COUNT(*) FROM tasks`, "tasks", false); err != nil {
		return nil, err
	}
	if err := count(&m.TasksDone, `SELECT COUNT(*) FROM tasks WHERE status = 'done'`, "tasks", true); err != nil {
		return nil, err
	}
	cond, args := scoped("tasks", false)
	if err := s.countBy(`SELECT COALESCE(actor_type, 'unknown'), COUNT(*) FROM tasks`+cond+` GROUP BY 1`, m.TasksByActor, args...); err != nil {
		return nil, err
	}
	cond, args = scoped("events", false)
	if err := s.countBy(`SELECT COALESCE(actor_type, 'unknown'), COUNT(*) FROM events`+cond+` GROUP BY 1`, m.EventsByActor, args...); err != nil {
		return nil, err
	}
	if err := count(&m.ChecksTotal, `SELECT COUNT(*) FROM checks`, "task_rows", false); err != nil {
		return nil, err
	}
	if err := count(&m.ChecksFailed, `SELECT COUNT(*) FROM checks WHERE status = 'fail'`, "task_rows", true); err != nil {
		return nil, err
	}
	if err := count(&m.ApprovalsTotal, `SELECT COUNT(*) FROM approvals`, "task_rows", false); err != nil {
		return nil, err
	}
	if err := count(&m.Overrides, `SELECT COUNT(*) FROM approvals WHERE decision = 'overridden'`, "task_rows", true); err != nil {
		return nil, err
	}
	if err := count(&m.UnverifiedDeps, `SELECT COUNT(*) FROM dependencies WHERE verified = 0`, "dependencies", true); err != nil {
		return nil, err
	}
	if err := count(&m.PendingMemory, `SELECT COUNT(*) FROM memory WHERE status = 'pending' AND stale = 0`, "memory", true); err != nil {
		return nil, err
	}
	if err := count(&m.PolicyViolations, `SELECT COUNT(*) FROM events WHERE type = 'policy_violation'`, "events", true); err != nil {
		return nil, err
	}
	if err := count(&m.GuardDenials, `SELECT COUNT(*) FROM events WHERE type = 'guard_denied'`, "events", true); err != nil {
		return nil, err
	}

	// Rework: a task that recorded a move to 'done' but is not currently done.
	// The append-only event log is what makes this answerable at all.
	cond, args = "", nil
	if projectID != nil {
		cond, args = ` AND t.project_id = ?`, []any{*projectID}
	}
	if err := s.DB.QueryRow(`
		SELECT COUNT(DISTINCT e.task_id) FROM events e
		JOIN tasks t ON t.id = e.task_id
		WHERE e.type = 'status_change' AND e.message = 'status -> done' AND t.status != 'done'`+cond, args...).Scan(&m.Reworked); err != nil {
		return nil, err
	}

	// Cost is the third lens alongside utilization and impact.
	cond, args = scoped("sessions", false)
	if err := s.DB.QueryRow(`
		SELECT COUNT(*), COALESCE(SUM(tokens_in), 0), COALESCE(SUM(tokens_out), 0), COALESCE(SUM(cost_usd), 0)
		FROM sessions`+cond, args...).Scan(&m.Sessions, &m.TokensIn, &m.TokensOut, &m.CostUSD); err != nil {
		return nil, err
	}

	// Most recent result per suite, for the autonomy-promotion picture.
	cond, args = scoped("task_rows", false)
	rows, err := s.DB.Query(`
		SELECT e.id, e.task_id, e.suite, e.pass_rate, e.sample_size, e.note, e.actor_type, e.actor_id, e.created_at
		FROM evals e
		JOIN (SELECT suite, MAX(id) AS max_id FROM evals`+cond+` GROUP BY suite) latest
		  ON e.id = latest.max_id
		ORDER BY e.suite`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var e Eval
		if err := rows.Scan(&e.ID, &e.TaskID, &e.Suite, &e.PassRate, &e.SampleSize,
			&e.Note, &e.ActorType, &e.ActorID, &e.CreatedAt); err != nil {
			return nil, err
		}
		m.LatestEvals = append(m.LatestEvals, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return m, nil
}

func (s *Store) countBy(query string, dest map[string]int, args ...any) error {
	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return err
		}
		dest[k] = n
	}
	return rows.Err()
}
