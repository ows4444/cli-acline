package tui

import (
	"testing"

	"github.com/ows4444/tui/tuitest"

	"acline/internal/app"
	"acline/internal/store"
)

// specsSession: spec #1 (approved, revised once) with draft plan #1 of two
// items; spec #2 a draft; decision #1 proposed.
func specsSession(t *testing.T) (*tuitest.Session, *store.Store) {
	t.Helper()
	st := openTestStore(t)
	spec, _ := st.AddSpec("search", "Index titles.\nRank by recency.")
	if _, err := app.ReviseSpec(st, spec, "Index titles and bodies.\nRank by recency."); err != nil {
		t.Fatal(err)
	}
	if err := st.ApproveSpec(spec, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ProposePlan(spec, store.PlanInput{Items: []store.PlanItemInput{{Ref: "idx", Title: "build the index"}, {Ref: "ui", Title: "search box"}}}); err != nil {
		t.Fatal(err)
	}
	st.AddSpec("payments", "Charge cards.")
	st.AddDecision("use sqlite fts5", store.DecisionOpts{Context: "need search"})
	m, err := newShell(st, nil, noHash, specsScreen{}, tasksScreen{})
	if err != nil {
		t.Fatal(err)
	}
	s := tuitest.New(m, 140, 40)
	t.Cleanup(s.Close)
	return s, st
}

func TestSpecsTreeGoesFromSpecToPlanToItems(t *testing.T) {
	s, _ := specsSession(t)
	shows(t, s, "spec #1 search  [approved v2]", "spec #2 payments  [draft v1]", "Decisions (1)",
		"Index titles and bodies.", "Earlier versions", "v1 search")
	s.Keys("right", "down")
	shows(t, s, "plan #1 v1  [draft]", "Items (the spec's first plan)")
	s.Keys("right", "down")
	shows(t, s, "idx  build the index  [low/hotl]", "Item idx of plan #1 (draft)")
}

func TestCompareASpecWithAnEarlierVersion(t *testing.T) {
	s, _ := specsSession(t)
	s.Keys("v", "enter")
	shows(t, s, "spec #1: v1 (-) against v2", "-Index titles.", "+Index titles and bodies.", " Rank by recency.")
	s.Keys("esc")
	shows(t, s, "Earlier versions")
	s.Keys("down", "v") // a draft spec never revised has nothing to compare
	shows(t, s, "spec #2 has only its current version")
}

func TestEditADraftPlansItemsBeforeApproval(t *testing.T) {
	s, st := specsSession(t)
	s.Keys("right", "down") // plan #1
	s.Keys("e", "enter")    // item idx
	s.Keys("enter")         // field: title
	s.Keys("build the fts5 index", "enter")
	shows(t, s, "plan #1 item idx: title set", "idx    build the fts5 index")

	s.Keys("e", "down", "enter") // item ui
	s.Keys("down", "enter")      // field: risk
	s.Keys("down", "down", "enter")
	shows(t, s, "plan #1 item ui: risk high")

	s.Keys("e", "down", "enter")                                    // item ui
	s.Keys("down", "down", "down", "down", "down", "down", "enter") // drop
	shows(t, s, "plan #1 item ui: dropped", "ui     search box  high/hotl  (dropped: no task)", "approving creates 1 task(s)")

	items, _ := st.PlanItems(1)
	if items[0].Title != "build the fts5 index" || items[1].Risk != "high" || !items[1].Dropped {
		t.Fatalf("items = %+v", items)
	}

	s.Keys("a", "y") // approve the plan: only the kept item becomes a task
	shows(t, s, "plan #1 approved: 1 task(s) created")
	s.Keys("right") // open the plan: its kept item is now a task
	shows(t, s, "task #1 build the fts5 index  [todo]")
	s.Keys("e")
	shows(t, s, "only a draft plan's items can be edited")
	s.Keys("down", "enter")
	shows(t, s, "[Tasks]", "#1 build the fts5 index")
}
