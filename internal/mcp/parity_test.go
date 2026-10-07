package mcp

import (
	"testing"

	"acline/internal/store"
)

func TestTaskUpdateChangesFieldsAndAgentsCannotLoosen(t *testing.T) {
	cs, st := connectedTestServerWithActor(t, store.Actor{Type: "agent", ID: "claude"})
	id, err := st.AddTask("t", "", "normal", store.TaskOpts{Risk: "medium"})
	if err != nil {
		t.Fatal(err)
	}

	out := callTool[taskUpdateOut](t, cs, "acline_task_update", taskUpdateArgs{ID: id, Risk: "high", Priority: "urgent", Area: "cli"})
	if len(out.Changed) != 3 {
		t.Fatalf("changed = %v, want risk, priority and area", out.Changed)
	}
	task, err := st.GetTask(id)
	if err != nil {
		t.Fatal(err)
	}
	if task.Risk != "high" || task.Priority != "urgent" || task.Area.String != "cli" {
		t.Fatalf("task after update: risk=%s priority=%s area=%s", task.Risk, task.Priority, task.Area.String)
	}

	// Lowering risk is a person's decision; the refusal stops the update before priority.
	r := callToolRaw(t, cs, "acline_task_update", taskUpdateArgs{ID: id, Risk: "low", Priority: "low"})
	if !r.IsError || errorCode(t, r) != "agent_cannot_loosen_task" {
		t.Fatalf("lowering risk as an agent: IsError=%v code=%q, want agent_cannot_loosen_task", r.IsError, errorCode(t, r))
	}
	if task, _ = st.GetTask(id); task.Risk != "high" || task.Priority != "urgent" {
		t.Fatalf("a refused update changed the task: risk=%s priority=%s", task.Risk, task.Priority)
	}

	if r := callToolRaw(t, cs, "acline_task_update", taskUpdateArgs{ID: id}); !r.IsError {
		t.Fatal("an update with no fields should be an error")
	}
}

func TestCheckRunnerListShowsAProjectsRunners(t *testing.T) {
	cs, st := connectedTestServer(t)
	pid, err := st.AddProject("p", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetCheckRunner(pid, "test", "npm test", ""); err != nil {
		t.Fatal(err)
	}
	out := callTool[checkRunnerListOut](t, cs, "acline_check_runner_list", checkRunnerListArgs{Project: "p"})
	if len(out.Runners) != 1 || out.Runners[0].Kind != "test" || out.Runners[0].Command != "npm test" {
		t.Fatalf("runners = %+v", out.Runners)
	}
	if r := callToolRaw(t, cs, "acline_check_runner_list", checkRunnerListArgs{}); !r.IsError {
		t.Fatal("a runner list without a project should be an error")
	}
}

func TestSpecShowIncludesEarlierVersions(t *testing.T) {
	cs, st := connectedTestServer(t)
	id, err := st.AddSpec("s", "first")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ReviseSpec(id, "second"); err != nil {
		t.Fatal(err)
	}
	out := callTool[specShowOut](t, cs, "acline_spec_show", specShowArgs{ID: id})
	if out.Spec.Body == nil || *out.Spec.Body != "second" || out.Spec.Version != 2 {
		t.Fatalf("spec = %+v", out.Spec)
	}
	if len(out.Versions) != 1 || out.Versions[0].Body != "first" || out.Versions[0].Version != 1 {
		t.Fatalf("versions = %+v", out.Versions)
	}
}

func TestDecisionShowReturnsOneDecision(t *testing.T) {
	cs, st := connectedTestServer(t)
	id, err := st.AddDecision("use sqlite", store.DecisionOpts{Rationale: "one file"})
	if err != nil {
		t.Fatal(err)
	}
	out := callTool[decisionOut](t, cs, "acline_decision_show", decisionShowArgs{ID: id})
	if out.ID != id || out.Title != "use sqlite" || out.Rationale == nil || *out.Rationale != "one file" {
		t.Fatalf("decision = %+v", out)
	}
	if r := callToolRaw(t, cs, "acline_decision_show", decisionShowArgs{ID: id + 100}); !r.IsError || errorCode(t, r) != "not_found" {
		t.Fatalf("unknown decision: IsError=%v code=%q, want not_found", r.IsError, errorCode(t, r))
	}
}

func TestTaskAddTakesTypeMilestoneParentAndToken(t *testing.T) {
	cs, st := connectedTestServerWithActor(t, store.Actor{Type: "agent", ID: "claude"})
	ms, err := st.AddMilestone("v1", store.MilestoneOpts{})
	if err != nil {
		t.Fatal(err)
	}
	parent := callTool[taskAddOut](t, cs, "acline_task_add", taskAddArgs{Title: "epic"})
	child := callTool[taskAddOut](t, cs, "acline_task_add", taskAddArgs{
		Title: "part", Type: "bug", MilestoneID: &ms, ParentID: &parent.ID,
	})
	task, err := st.GetTask(child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Type.String != "bug" || task.MilestoneID.Int64 != ms || task.ParentID.Int64 != parent.ID {
		t.Fatalf("task: type=%v milestone=%v parent=%v", task.Type, task.MilestoneID, task.ParentID)
	}

	// The CLI's errors, through the same use case.
	missing := int64(999)
	for name, args := range map[string]taskAddArgs{
		"blank title":       {Title: " "},
		"invalid type":      {Title: "t", Type: "chore"},
		"missing milestone": {Title: "t", MilestoneID: &missing},
		"missing parent":    {Title: "t", ParentID: &missing},
	} {
		if r := callToolRaw(t, cs, "acline_task_add", args); !r.IsError {
			t.Errorf("%s: task added", name)
		}
	}

	// autonomy auto is a person's choice; an agent needs the token.
	if r := callToolRaw(t, cs, "acline_task_add", taskAddArgs{Title: "t", Autonomy: "auto"}); !r.IsError || errorCode(t, r) != "agent_cannot_loosen_task" {
		t.Fatalf("auto without the token: IsError=%v code=%q, want agent_cannot_loosen_task", r.IsError, errorCode(t, r))
	}
	tok, err := st.EnableApprovalToken()
	if err != nil {
		t.Fatal(err)
	}
	auto := callTool[taskAddOut](t, cs, "acline_task_add", taskAddArgs{Title: "t", Autonomy: "auto", Token: tok})
	if task, _ := st.GetTask(auto.ID); task.Autonomy != "auto" {
		t.Fatalf("autonomy = %s, want auto", task.Autonomy)
	}
}

func TestPlanReviseReplacesADraft(t *testing.T) {
	cs, st := connectedTestServer(t)
	spec, _ := st.AddSpec("Inventory", "Track stock.")
	st.ApproveSpec(spec, "")
	first := callTool[planProposeOut](t, cs, "acline_plan_propose", planProposeArgs{
		SpecID: spec, Items: []planItemArgs{{Ref: "T1", Title: "Schema"}},
	})

	out := callTool[planReviseOut](t, cs, "acline_plan_revise", planReviseArgs{
		ID: first.ID, Note: "split the API out",
		Items: []planItemArgs{{Ref: "T1", Title: "Schema"}, {Ref: "T2", Title: "API", DependsOn: []string{"T1"}}},
	})
	if out.ID == first.ID || out.Supersedes != first.ID || out.Status != "draft" || out.Items != 2 {
		t.Fatalf("revise = %+v", out)
	}
	if old, _ := st.GetPlan(first.ID); old.Status != "superseded" {
		t.Errorf("revised draft is %s, want superseded", old.Status)
	}
	if p, _ := st.GetPlan(out.ID); p.Version != 2 {
		t.Errorf("revision version = %d, want 2", p.Version)
	}

	// The superseded draft cannot be revised again, and the plan rules hold.
	for name, args := range map[string]planReviseArgs{
		"superseded": {ID: first.ID, Items: []planItemArgs{{Ref: "A", Title: "a"}}},
		"auto":       {ID: out.ID, Items: []planItemArgs{{Ref: "A", Title: "a", Autonomy: "auto"}}},
		"cycle":      {ID: out.ID, Items: []planItemArgs{{Ref: "A", Title: "a", DependsOn: []string{"B"}}, {Ref: "B", Title: "b", DependsOn: []string{"A"}}}},
		"empty":      {ID: out.ID},
	} {
		if r := callToolRaw(t, cs, "acline_plan_revise", args); !r.IsError {
			t.Errorf("%s: revision accepted", name)
		}
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM tasks`); n != 0 {
		t.Errorf("revising created %d task(s)", n)
	}
}
