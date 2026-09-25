package app

import (
	"errors"

	"acline/internal/store"
)

// DashboardData is the shared "what needs my attention right now" bootstrap
// view: active session, open tasks needing attention, approved specs,
// accepted decisions, approved memory, and the review/decay/dependency
// nudges. `acline dashboard` and the MCP acline_dashboard tool used to run
// this same sequence of store queries independently (and had drifted once,
// with two different meanings of "needs attention");
// Dashboard is now the single place that assembles it, so each adapter only
// has to format it (Printf lines for the CLI, a JSON-shaped *Out for MCP).
type DashboardData struct {
	Session               *store.Session
	Tasks                 []store.Task
	TasksNeedingAttention []store.Task
	ApprovedSpecs         []store.Spec
	SpecsAwaitingPlan     []store.Spec // approved, with no live plan and no tasks
	DraftPlans            []store.Plan // proposed, awaiting a person
	AcceptedDecisions     []store.Decision
	Memory                []store.MemoryEntry
	PendingMemoryCount    int
	DraftedMemoryCount    int // subset of PendingMemoryCount: auto-drafted from failures
	DecayingMemoryCount   int
	UnverifiedDeps        []store.Dependency
	// ApprovalTokenEnabled is false while identity is self-declared: without a
	// token, whoever sets ACLINE_ACTOR_TYPE=human is a person as far as the store
	// can tell. The dashboard says so rather than implying approvals are enforced.
	ApprovalTokenEnabled bool
}

// Dashboard assembles DashboardData scoped to projectID (nil = unscoped).
func Dashboard(st *store.Store, projectID *int64) (DashboardData, error) {
	var d DashboardData

	if sess, err := st.CurrentSession(); err == nil {
		d.Session = sess
	} else if !errors.Is(err, store.ErrNoActiveSession) {
		return d, err
	}

	tasks, err := st.ListTasks(store.TaskFilter{ProjectID: projectID})
	if err != nil {
		return d, err
	}
	d.Tasks = tasks
	for _, t := range tasks {
		if t.NeedsAttention() {
			d.TasksNeedingAttention = append(d.TasksNeedingAttention, t)
		}
	}

	if d.ApprovedSpecs, err = st.ListSpecs("approved", projectID); err != nil {
		return d, err
	}
	if d.AcceptedDecisions, err = st.ListDecisions("accepted", projectID); err != nil {
		return d, err
	}
	if d.Memory, err = st.ListMemory(store.MemoryFilter{Status: "approved", ProjectID: projectID}); err != nil {
		return d, err
	}
	if d.SpecsAwaitingPlan, err = st.SpecsAwaitingPlan(projectID); err != nil {
		return d, err
	}
	if d.DraftPlans, err = st.DraftPlans(projectID); err != nil {
		return d, err
	}
	if d.PendingMemoryCount, err = st.CountPendingMemoryIn(projectID); err != nil {
		return d, err
	}
	if d.DraftedMemoryCount, err = st.CountDraftedMemoryIn(projectID); err != nil {
		return d, err
	}
	if d.DecayingMemoryCount, err = st.CountDecayCandidatesIn(store.DefaultMemoryDecayDays, projectID); err != nil {
		return d, err
	}
	if d.UnverifiedDeps, err = st.ListDependencies(projectID, true); err != nil {
		return d, err
	}
	if d.ApprovalTokenEnabled, err = st.ApprovalTokenEnabled(); err != nil {
		return d, err
	}

	return d, nil
}
