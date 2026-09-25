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
