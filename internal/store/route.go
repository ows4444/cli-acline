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
	Blockers      []string // unmet gate blockers, once checks are settled: an agent's first, else a person's
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
	finish := func(st routeStep) (*Route, error) {
		r.Action, r.Reason, r.NeedsHuman = st.action, st.reason, st.human
		if st.role != "" {
			if ro, err := s.GetRoleByName(nullIntPtr(t.ProjectID), st.role); err == nil {
				r.Role = ro
			}
		}
		return r, nil
	}

	if t.Status == "done" || t.Status == "cancelled" {
		return finish(routeStep{RouteNone, "task is " + t.Status, "", false})
	}
	if t.Deferred {
		return finish(routeStep{RouteNone, "task is deferred", "", false})
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

	if st, err := s.routeOnState(t, r); err != nil || st != nil {
		if err != nil {
			return nil, err
		}
		return finish(*st)
	}
	checks, err := s.ListChecks(taskID)
	if err != nil {
		return nil, err
	}
	if st := routeOnChecks(t, len(criteria), checks, r); st != nil {
		return finish(*st)
	}

	gate, err := s.EvaluateGate(taskID)
	if err != nil {
		return nil, err
	}
	if !gate.OK() {
		// Evidence an agent can produce comes before asking a person: an
		// approval is of the code as verified, so it waits for the checks.
		if agent := gate.AgentBlockers(); len(agent) > 0 {
			r.Blockers = agent
			return finish(routeStep{RouteVerify, fmt.Sprintf("the gate needs %d more check result(s) acline ran", len(agent)), "qa", false})
		}
		r.Blockers = gate.PersonBlockers
		return finish(routeStep{RouteRequestApproval, "checks are settled; the gate still needs a human decision", "manager", true})
	}
	return finish(routeStep{RouteComplete, "gate satisfied", "", false})
}

// routeStep is a route decision: the action, why, the role it goes to and
// whether a person must take it.
type routeStep struct {
	action, reason, role string
	human                bool
}

// routeOnState routes a task its own state holds up: blocked, its spec still a
// draft, or prerequisites not done (listed on r). nil means none applies.
func (s *Store) routeOnState(t *Task, r *Route) (*routeStep, error) {
	if t.Status == "blocked" {
		why := "blocked with no recorded reason (record one: acline task update " + fmt.Sprint(t.ID) + " --status blocked --reason \"...\")"
		if t.BlockedReason.Valid {
			why = "blocked: " + t.BlockedReason.String
		}
		return &routeStep{RouteResolveBlocker, why, "scrummaster", true}, nil
	}
	if t.SpecID.Valid {
		if sp, err := s.GetSpec(t.SpecID.Int64); err == nil && sp.Status == "draft" {
			return &routeStep{RouteApproveSpec, fmt.Sprintf("spec #%d is still a draft", sp.ID), "manager", true}, nil
		}
	}
	open, err := s.OpenPrerequisites(t.ID)
	if err != nil {
		return nil, err
	}
	if len(open) > 0 {
		for _, p := range open {
			r.WaitingOn = append(r.WaitingOn, prerequisiteLabel(p))
		}
		return &routeStep{RouteWaitDependency, fmt.Sprintf("waiting on %d prerequisite task(s)", len(open)), "", false}, nil
	}
	return nil, nil
}

// routeOnChecks routes a task its checks decide: failing (listed on r), a
// skipped scan at high risk, everything skipped, or nothing recorded yet. nil
// means the checks are settled and the gate decides.
func routeOnChecks(t *Task, criteria int, checks []Check, r *Route) *routeStep {
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
		return &routeStep{RouteFixChecks, fmt.Sprintf("%d check(s) failing", len(r.FailingChecks)), "developer", false}
	}
	if t.Risk == "high" || t.Risk == "critical" {
		for _, kind := range []string{"sast", "sca"} {
			if c, ok := latest[kind]; ok && c.Status == "skipped" {
				return &routeStep{RouteVerify, kind + " scan was skipped; install the tool and run: acline check run", "security", false}
			}
		}
	}
	if len(latest) > 0 && everyResultSkipped(latest) {
		return &routeStep{RouteVerify, "every recorded check was skipped: run one acline can run", "qa", false}
	}
	if len(checks) == 0 {
		switch t.Status {
		case "backlog", "todo":
			if criteria == 0 {
				return &routeStep{RouteDefineCriteria, "no acceptance criteria yet", "architect", false}
			}
			return &routeStep{RouteStartWork, "not started", "developer", false}
		}
		return &routeStep{RouteVerify, "work is underway but no verification is recorded", "qa", false}
	}
	return nil
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

// everyResultSkipped reports whether every newest result per kind is skipped.
func everyResultSkipped(latest map[string]Check) bool {
	for _, c := range latest {
		if c.Status != "skipped" {
			return false
		}
	}
	return true
}
