package app

import (
	"time"

	"acline/internal/store"
)

// StaleSessionAge is how long an active session may go without activity
// before it counts as stale (usually a crashed agent).
const StaleSessionAge = 12 * time.Hour

// GatedTask is a task with its completion gate evaluated against the code as
// it is now.
type GatedTask struct {
	Task store.Task
	Gate store.GateResult
}

// PersonQueue is everything waiting on a person: the work only a person can
// move forward. Unlike Dashboard it evaluates every open task's gate (hashing
// each project's tree once), so it is for a person's view, not every session start.
type PersonQueue struct {
	// AwaitingApproval are open tasks whose gate a person's approval would
	// clear: no blocker an agent could clear is left.
	AwaitingApproval  []GatedTask
	DraftSpecs        []store.Spec
	DraftPlans        []store.Plan
	ProposedDecisions []store.Decision
	PendingMemory     []store.MemoryEntry
	StaleSessions     []store.Session
}

// Queue assembles the PersonQueue for projectID (nil: every project). hash
// fingerprints a directory (worktree.Hash, or a test's stand-in).
func Queue(st *store.Store, projectID *int64, hash func(string) string) (PersonQueue, error) {
	var q PersonQueue
	tasks, err := st.ListTasks(store.TaskFilter{ProjectID: projectID})
	if err != nil {
		return q, err
	}
	trees := map[string]string{} // one hash per directory, however many tasks share it
	memo := func(dir string) string {
		if h, ok := trees[dir]; ok {
			return h
		}
		h := hash(dir)
		trees[dir] = h
		return h
	}
	for _, t := range tasks {
		if t.Status == "done" || t.Status == "cancelled" {
			continue
		}
		g, err := st.EvaluateGateForTree(t.ID, GateTree(st, t.ID, memo))
		if err != nil {
			return q, err
		}
		if len(g.PersonBlockers) > 0 && len(g.AgentBlockers()) == 0 {
			q.AwaitingApproval = append(q.AwaitingApproval, GatedTask{Task: t, Gate: g})
		}
	}
	if q.DraftSpecs, err = st.ListSpecs("draft", projectID); err != nil {
		return q, err
	}
	if q.DraftPlans, err = st.DraftPlans(projectID); err != nil {
		return q, err
	}
	if q.ProposedDecisions, err = st.ListDecisions("proposed", projectID); err != nil {
		return q, err
	}
	if q.PendingMemory, err = st.ListMemory(store.MemoryFilter{Status: "pending", ProjectID: projectID}); err != nil {
		return q, err
	}
	stale, err := st.StaleSessions(StaleSessionAge)
	if err != nil {
		return q, err
	}
	for _, s := range stale {
		if projectID == nil || s.ProjectID.Valid && s.ProjectID.Int64 == *projectID {
			q.StaleSessions = append(q.StaleSessions, s)
		}
	}
	return q, nil
}
