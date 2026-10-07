package tui

import (
	"strings"
	"testing"

	"github.com/ows4444/tui/tuitest"

	"acline/internal/store"
)

// seedReview puts one of each kind of item in the queue: task #1 (ready for
// approval), a draft spec, a revised draft plan, a proposed decision and a
// memory entry held for review.
func seedReview(t *testing.T, st *store.Store) {
	t.Helper()
	id, _ := st.AddTask("ship the login fix", "", "normal", store.TaskOpts{Risk: "high"})
	if _, err := st.AddCheckWithMeta(id, nil, "test", "pass", "ran", "", store.CheckMeta{Source: store.CheckSourceRunner, TreeHash: "sha256:now"}); err != nil {
		t.Fatal(err)
	}
	st.AddSpec("draft payments spec", "Charge cards.\nRefund within 30 days.")
	approved, _ := st.AddSpec("search spec", "body")
	if err := st.ApproveSpec(approved, ""); err != nil {
		t.Fatal(err)
	}
	v1, err := st.ProposePlan(approved, store.PlanInput{Items: []store.PlanItemInput{{Ref: "idx", Title: "build the index"}, {Ref: "ui", Title: "search box"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.RevisePlan(v1, store.PlanInput{Note: "split the ui", Items: []store.PlanItemInput{
		{Ref: "idx", Title: "build the index", Risk: "medium"}, {Ref: "box", Title: "search box"}, {Ref: "res", Title: "results page"},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddDecision("use sqlite fts5", store.DecisionOpts{Context: "need search", Decision: "fts5", Rationale: "already embedded"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddMemory("cli", "pitfall", "cobra reads flags before env", store.MemoryOpts{ForcePending: true}); err != nil {
		t.Fatal(err)
	}
}

func reviewSession(t *testing.T, st *store.Store) *tuitest.Session {
	t.Helper()
	m, err := newShell(st, nil, func(string) string { return "sha256:now" }, reviewScreen{}, tasksScreen{})
	if err != nil {
		t.Fatal(err)
	}
	s := tuitest.New(m, 140, 40)
	t.Cleanup(s.Close)
	return s
}

func TestReviewListsEverythingWaitingOnAPerson(t *testing.T) {
	st := openTestStore(t)
	seedReview(t, st)
	s := reviewSession(t, st)
	shows(t, s, "1 of 5 waiting on you",
		"> task #1  ship the login fix", "spec #1  draft payments spec", "plan #2  for spec #2 search spec",
		"decision #1  use sqlite fts5", "memory #1  cobra reads flags before env",
		"Waiting on you", "human approval required")
	s.Keys("down")
	shows(t, s, "Spec #1 draft payments spec", "Refund within 30 days.")
	s.Keys("down")
	shows(t, s, "Items, compared with plan #1 v1", "split the ui",
		"idx    build the index", "(risk)", "box    search box", "res    results page",
		"ui     search box", "(no longer in the plan)", "approving creates 3 task(s)")
	s.Keys("down")
	shows(t, s, "Context", "need search", "Rationale", "already embedded")
}

func TestReviewApprovesAndRejectsEachKind(t *testing.T) {
	st := openTestStore(t)
	seedReview(t, st)
	s := reviewSession(t, st)

	s.Keys("a", "y")
	shows(t, s, "approval #1 recorded for task #1", "1 of 4 waiting on you", "> spec #1")
	s.Keys("X")
	shows(t, s, "a spec cannot be rejected")
	s.Keys("a", "y")
	shows(t, s, "spec #1 approved", "> plan #2")
	s.Keys("a", "y")
	shows(t, s, "plan #2 approved: 3 task(s) created", "> decision #1")
	s.Keys("X", "y")
	shows(t, s, "decision #1 rejected", "> memory #1")
	s.Keys("a", "y")
	shows(t, s, "memory #1 approved", "Nothing is waiting on you.")

	if d, _ := st.ListDecisions("rejected", nil); len(d) != 1 {
		t.Errorf("rejected decisions = %d, want 1", len(d))
	}
	if tasks, _ := st.ListTasks(store.TaskFilter{}); len(tasks) != 4 {
		t.Errorf("tasks = %d, want the first plus the plan's 3", len(tasks))
	}
}

func TestReviewAsksForTheTokenWhenTheStoreWantsIt(t *testing.T) {
	st := openTestStore(t)
	seedReview(t, st)
	token, err := st.EnableApprovalToken()
	if err != nil {
		t.Fatal(err)
	}
	s := reviewSession(t, st)
	s.Keys("up") // the list wraps no further than the first item
	s.Keys("down", "down", "down", "down")
	shows(t, s, "> memory #1")
	s.Keys("a", "y")
	shows(t, s, "Approval token for approving memory #1")
	s.Keys(token)
	if strings.Contains(rendered(s), token[:8]) {
		t.Fatal("the token is visible while typed")
	}
	s.Keys("enter")
	shows(t, s, "memory #1 approved")
}

func TestEnterOnAReviewTaskOpensItsDetail(t *testing.T) {
	st := openTestStore(t)
	seedReview(t, st)
	s := reviewSession(t, st)
	s.Keys("enter")
	shows(t, s, "[Tasks]", "Gate: 1 blocker(s)", "(person) human approval required")
}
