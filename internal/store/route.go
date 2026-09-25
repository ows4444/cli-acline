package store

import "fmt"

// Route actions, in the order a task normally moves through them.
const (
	RouteNone            = "none"
	RouteResolveBlocker  = "resolve_blocker"
	RouteApproveSpec     = "approve_spec"
	RouteWaitDependency  = "wait_on_dependency"
	RouteDefineCriteria  = "define_criteria"
	RouteStartWork       = "start_work"
	RouteFixChecks       = "fix_failing_checks"
	RouteVerify          = "verify"
	RouteRequestApproval = "request_approval"
	RouteComplete        = "complete"
)

// Route is the answer to "what should happen to this task next, by whom, with
// what context". It is derived entirely from recorded state (status, spec,
// criteria, checks, gate), never from chat, and is advisory: nothing enforces
// that the suggested role acts, exactly like NextRoleHint.
type Route struct {
	TaskID     int64
	Title      string
	Status     string
	Action     string
	Reason     string
	NeedsHuman bool  // the next step is a person's decision, not an agent's
	Role       *Role // suggested role; nil when the project has no such role
	SpecID     *int64

	WaitingOn     []string // unfinished prerequisites, as "#id title [status]"
	Blockers      []string // unmet gate blockers (approval only, once checks are settled)
	OpenCriteria  []string
	FailingChecks []string // "<kind>: <detail>" for each latest failing check
	Lessons       []MemoryEntry
}

// RouteTask computes the next step for one task.
func (s *Store) RouteTask(taskID int64) (*Route, error) {
	t, err := s.GetTask(taskID)
	if err != nil {
		return nil, err
	}
	r := &Route{TaskID: t.ID, Title: t.Title, Status: t.Status}
	if t.SpecID.Valid {
		r.SpecID = &t.SpecID.Int64
	}
	var projectID *int64
	if t.ProjectID.Valid {
		projectID = &t.ProjectID.Int64
	}
	role := func(name string) {
		if ro, err := s.GetRoleByName(projectID, name); err == nil {
			r.Role = ro
		}
	}
	finish := func(action, reason, roleName string, human bool) (*Route, error) {
		r.Action, r.Reason, r.NeedsHuman = action, reason, human
		if roleName != "" {
			role(roleName)
		}
		return r, nil
	}

	if t.Status == "done" || t.Status == "cancelled" {
		return finish(RouteNone, "task is "+t.Status, "", false)
	}
	if t.Deferred {
		return finish(RouteNone, "task is deferred", "", false)
	}

	criteria, err := s.ListCriteria(taskID)
	if err != nil {
		return nil, err
	}
	for _, c := range criteria {
		if !c.Done {
			r.OpenCriteria = append(r.OpenCriteria, c.Text)
		}
	}
	if lessons, err := s.RecallForTask(taskID); err == nil {
		r.Lessons = lessons
	}

	if t.Status == "blocked" {
		why := "blocked with no recorded reason (record one: acline task update " + fmt.Sprint(t.ID) + " --status blocked --reason \"...\")"
		if t.BlockedReason.Valid {
			why = "blocked: " + t.BlockedReason.String
		}
		return finish(RouteResolveBlocker, why, "scrummaster", true)
	}
	if t.SpecID.Valid {
		if sp, err := s.GetSpec(t.SpecID.Int64); err == nil && sp.Status == "draft" {
			return finish(RouteApproveSpec, fmt.Sprintf("spec #%d is still a draft", sp.ID), "manager", true)
		}
	}

	open, err := s.OpenPrerequisites(taskID)
	if err != nil {
		return nil, err
	}
	if len(open) > 0 {
		for _, p := range open {
			r.WaitingOn = append(r.WaitingOn, prerequisiteLabel(p))
		}
		return finish(RouteWaitDependency, fmt.Sprintf("waiting on %d prerequisite task(s)", len(open)), "", false)
	}

	checks, err := s.ListChecks(taskID)
	if err != nil {
		return nil, err
	}
	latest := make(map[string]Check)
	for _, c := range checks {
		latest[c.Kind] = c // append-only: the newest result per kind is current
	}
	for _, c := range latest {
		if c.Status == "fail" {
			r.FailingChecks = append(r.FailingChecks, c.Kind+": "+c.Detail.String)
		}
	}
	if len(r.FailingChecks) > 0 {
		return finish(RouteFixChecks, fmt.Sprintf("%d check(s) failing", len(r.FailingChecks)), "developer", false)
	}

	if t.Risk == "high" || t.Risk == "critical" {
		for _, kind := range []string{"sast", "sca"} {
			if c, ok := latest[kind]; ok && c.Status == "skipped" {
				return finish(RouteVerify, kind+" scan was skipped; install the tool and run: acline check run", "security", false)
			}
		}
	}

	if len(checks) == 0 {
		switch t.Status {
		case "backlog", "todo":
			if len(criteria) == 0 {
				return finish(RouteDefineCriteria, "no acceptance criteria yet", "architect", false)
			}
			return finish(RouteStartWork, "not started", "developer", false)
		}
		return finish(RouteVerify, "work is underway but no verification is recorded", "qa", false)
	}

	gate, err := s.EvaluateGate(taskID)
	if err != nil {
		return nil, err
	}
	if !gate.OK() {
		r.Blockers = gate.Blockers
		return finish(RouteRequestApproval, "checks are settled; the gate still needs a human decision", "manager", true)
	}
	return finish(RouteComplete, "gate satisfied", "", false)
}

// NextTask picks the task to work on next: the highest-priority open task
// whose next step an agent can take, falling back to the highest-priority one
// that is waiting on a person or on a prerequisite. It returns nil when nothing is open.
func (s *Store) NextTask(projectID *int64) (*Route, error) {
	tasks, err := s.ListTasks(TaskFilter{ProjectID: projectID})
	if err != nil {
		return nil, err
	}
	var waiting *Route
	for _, t := range tasks {
		r, err := s.RouteTask(t.ID)
		if err != nil {
			return nil, err
		}
		if r.Action == RouteNone {
			continue
		}
		if !r.NeedsHuman && r.Action != RouteWaitDependency {
			return r, nil
		}
		if waiting == nil {
			waiting = r
		}
	}
	return waiting, nil
}
