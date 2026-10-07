package app

import (
	"testing"

	"acline/internal/store"
)

func runnerPass(t *testing.T, st *store.Store, task int64, tree string) {
	t.Helper()
	if _, err := st.AddCheckWithMeta(task, nil, "test", "pass", "ran", "", store.CheckMeta{Source: store.CheckSourceRunner, TreeHash: tree}); err != nil {
		t.Fatal(err)
	}
}

func TestQueueListsWhatOnlyAPersonCanMoveForward(t *testing.T) {
	st := openTestStore(t)
	st.Actor = store.Actor{Type: "human", ID: "ana"}
	pid, err := st.AddProject("api", t.TempDir(), "hotl")
	if err != nil {
		t.Fatal(err)
	}
	opts := store.TaskOpts{Risk: "high", ProjectID: &pid}
	ready, _ := st.AddTask("ready for review", "", "normal", opts)
	runnerPass(t, st, ready, "sha256:now")
	unchecked, _ := st.AddTask("no checks yet", "", "normal", opts) // an agent can still run them
	stale, _ := st.AddTask("checked older code", "", "normal", opts)
	runnerPass(t, st, stale, "sha256:old")
	low, _ := st.AddTask("low risk, passing", "", "normal", store.TaskOpts{ProjectID: &pid})
	runnerPass(t, st, low, "sha256:now")

	if _, err := st.AddSpec("draft spec", "body", store.SpecOpts{ProjectID: &pid}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddDecision("proposed", store.DecisionOpts{ProjectID: &pid}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddMemory("cli", "lesson", "held for review", store.MemoryOpts{ProjectID: &pid, ForcePending: true}); err != nil {
		t.Fatal(err)
	}

	hashes := 0
	q, err := Queue(st, &pid, func(string) string { hashes++; return "sha256:now" })
	if err != nil {
		t.Fatal(err)
	}
	if len(q.AwaitingApproval) != 1 || q.AwaitingApproval[0].Task.ID != ready {
		t.Errorf("AwaitingApproval = %+v, want only #%d (not #%d, #%d, #%d)", q.AwaitingApproval, ready, unchecked, stale, low)
	}
	if hashes != 1 {
		t.Errorf("hashed the project's tree %d times, want once for all its tasks", hashes)
	}
	if len(q.DraftSpecs) != 1 || len(q.ProposedDecisions) != 1 || len(q.PendingMemory) != 1 {
		t.Errorf("specs %d, decisions %d, memory %d: want one of each", len(q.DraftSpecs), len(q.ProposedDecisions), len(q.PendingMemory))
	}

	other, _ := st.AddProject("web", t.TempDir(), "hotl")
	q, err = Queue(st, &other, func(string) string { return "sha256:now" })
	if err != nil {
		t.Fatal(err)
	}
	if len(q.AwaitingApproval)+len(q.DraftSpecs)+len(q.ProposedDecisions)+len(q.PendingMemory) != 0 {
		t.Errorf("another project's queue shows api's items: %+v", q)
	}
}
