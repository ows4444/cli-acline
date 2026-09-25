package app

import (
	"testing"

	"acline/internal/store"
)

func TestDashboardAssemblesTasksNeedingAttention(t *testing.T) {
	st := openTestStore(t)
	projectID, err := st.AddProject("demo", t.TempDir(), "hotl")
	if err != nil {
		t.Fatalf("add project: %v", err)
	}

	quietID, err := st.AddTask("quiet task", "", "normal", store.TaskOpts{ProjectID: &projectID, Risk: "low"})
	if err != nil {
		t.Fatalf("add task: %v", err)
	}
	urgentID, err := st.AddTask("urgent task", "", "normal", store.TaskOpts{ProjectID: &projectID, Risk: "critical"})
	if err != nil {
		t.Fatalf("add task: %v", err)
	}
	_ = quietID

	d, err := Dashboard(st, &projectID)
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}
	if len(d.Tasks) != 2 {
		t.Fatalf("got %d tasks, want 2", len(d.Tasks))
	}
	if len(d.TasksNeedingAttention) != 1 || d.TasksNeedingAttention[0].ID != urgentID {
		t.Fatalf("got %+v, want exactly the critical-risk task", d.TasksNeedingAttention)
	}
	if d.Session != nil {
		t.Fatalf("got session %+v, want nil (none started)", d.Session)
	}
}

func TestDashboardUnscopedIgnoresProjectFilter(t *testing.T) {
	st := openTestStore(t)
	projectID, err := st.AddProject("demo", t.TempDir(), "hotl")
	if err != nil {
		t.Fatalf("add project: %v", err)
	}
	if _, err := st.AddTask("scoped task", "", "normal", store.TaskOpts{ProjectID: &projectID, Risk: "low"}); err != nil {
		t.Fatalf("add task: %v", err)
	}

	d, err := Dashboard(st, nil)
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}
	if len(d.Tasks) != 1 {
		t.Fatalf("got %d tasks, want 1 (unscoped should still see it)", len(d.Tasks))
	}
}

func TestDashboardReportsSpecsAwaitingAPlanAndDraftPlans(t *testing.T) {
	s := openTestStore(t)
	spec, _ := s.AddSpec("Inventory", "b")
	s.ApproveSpec(spec, "")
	d, err := Dashboard(s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.SpecsAwaitingPlan) != 1 || len(d.DraftPlans) != 0 {
		t.Fatalf("awaiting=%d drafts=%d", len(d.SpecsAwaitingPlan), len(d.DraftPlans))
	}
	if _, err := s.ProposePlan(spec, store.PlanInput{Items: []store.PlanItemInput{{Ref: "A", Title: "a"}}}); err != nil {
		t.Fatal(err)
	}
	d, _ = Dashboard(s, nil)
	if len(d.SpecsAwaitingPlan) != 0 || len(d.DraftPlans) != 1 {
		t.Fatalf("after propose: awaiting=%d drafts=%d", len(d.SpecsAwaitingPlan), len(d.DraftPlans))
	}
}

func TestDashboardReportsWhetherTheApprovalTokenIsEnabled(t *testing.T) {
	st := openTestStore(t)
	d, err := Dashboard(st, nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.ApprovalTokenEnabled {
		t.Fatal("a fresh store has no approval token")
	}
	if _, err := st.EnableApprovalToken(); err != nil {
		t.Fatal(err)
	}
	if d, _ = Dashboard(st, nil); !d.ApprovalTokenEnabled {
		t.Fatal("dashboard should report the token once enabled")
	}
}

func TestDashboardMemoryNudgesAreScopedToTheProject(t *testing.T) {
	st := openTestStore(t)
	a, _ := st.AddProject("a", t.TempDir(), "hotl")
	b, _ := st.AddProject("b", t.TempDir(), "hotl")
	agent := *st
	agent.Actor = store.Actor{Type: "agent", ID: "x"} // agent-written memory lands pending
	agent.AddMemory("x", "lesson", "for b", store.MemoryOpts{ProjectID: &b})

	da, err := Dashboard(st, &a)
	if err != nil {
		t.Fatal(err)
	}
	if da.PendingMemoryCount != 0 {
		t.Errorf("project a's dashboard shows %d pending entries that belong to project b", da.PendingMemoryCount)
	}
	if db, _ := Dashboard(st, &b); db.PendingMemoryCount != 1 {
		t.Errorf("project b's dashboard = %d pending, want 1", db.PendingMemoryCount)
	}
	if all, _ := Dashboard(st, nil); all.PendingMemoryCount != 1 {
		t.Errorf("unscoped dashboard = %d pending, want 1", all.PendingMemoryCount)
	}
}
