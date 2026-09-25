package mcp

import (
	"context"
	"path/filepath"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"acline/internal/store"
)

// connectedTestServerWithActor is connectedTestServer with a caller-chosen
// actor, needed for the agent-cannot-self-approve rule below (the default
// test actor is human).
func connectedTestServerWithActor(t *testing.T, actor store.Actor) (*sdkmcp.ClientSession, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	st.Actor = actor
	t.Cleanup(func() { st.Close() })

	server := NewServer(st)
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "0.0.0"}, nil)

	ctx := context.Background()
	t1, t2 := sdkmcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, t1, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	cs, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs, st
}

func TestTaskGateBlockedWithoutAnyCheck(t *testing.T) {
	cs, st := connectedTestServer(t)
	taskID, err := st.AddTask("low-risk task", "", "normal", store.TaskOpts{Risk: "low"})
	if err != nil {
		t.Fatal(err)
	}
	out := callTool[taskGateOut](t, cs, "acline_task_gate", taskGateArgs{ID: taskID})
	if out.OK {
		t.Fatal("expected gate to be unsatisfied with no recorded check")
	}
	if len(out.Blockers) == 0 {
		t.Fatal("expected at least one blocker")
	}
}

func TestTaskDoneWithoutForceFailsOnUnsatisfiedGate(t *testing.T) {
	cs, st := connectedTestServer(t)
	taskID, err := st.AddTask("low-risk task", "", "normal", store.TaskOpts{Risk: "low"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := cs.CallTool(context.Background(), &sdkmcp.CallToolParams{
		Name: "acline_task_done", Arguments: taskDoneArgs{ID: taskID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected an error result for an unsatisfied gate without force")
	}

	task, err := st.GetTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status == "done" {
		t.Fatal("task must not be marked done when the gate blocks it")
	}
}

func TestTaskDoneWithForceOverridesGateAndRecordsIt(t *testing.T) {
	cs, st := connectedTestServer(t)
	taskID, err := st.AddTask("low-risk task", "", "normal", store.TaskOpts{Risk: "low"})
	if err != nil {
		t.Fatal(err)
	}
	out := callTool[taskDoneOut](t, cs, "acline_task_done", taskDoneArgs{ID: taskID, Force: true})
	if !out.Overridden {
		t.Fatal("expected Overridden=true")
	}

	task, err := st.GetTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != "done" {
		t.Fatalf("expected task status=done, got %q", task.Status)
	}

	approvals, err := st.ListApprovals(taskID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range approvals {
		if a.Kind == "override" && a.Decision == "overridden" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected the override to be recorded as an approval event, not silently applied")
	}
}

func TestTaskDoneSucceedsWithoutForceOnceGateSatisfied(t *testing.T) {
	cs, st := connectedTestServer(t)
	taskID, err := st.AddTask("low-risk task", "", "normal", store.TaskOpts{Risk: "low"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddCheck(taskID, "test", "pass", ""); err != nil {
		t.Fatal(err)
	}

	out := callTool[taskDoneOut](t, cs, "acline_task_done", taskDoneArgs{ID: taskID})
	if out.Overridden {
		t.Fatal("expected Overridden=false when the gate is already satisfied")
	}
	task, err := st.GetTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != "done" {
		t.Fatalf("expected task status=done, got %q", task.Status)
	}
}

func TestApproveIsRefusedForAnAgentEvenWhenItNamesAPerson(t *testing.T) {
	cs, st := connectedTestServerWithActor(t, store.Actor{Type: "agent", ID: "claude-code", Model: "claude-sonnet-5"})
	taskID, err := st.AddTask("high-risk task", "", "normal", store.TaskOpts{Risk: "high"})
	if err != nil {
		t.Fatal(err)
	}

	res, err := cs.CallTool(context.Background(), &sdkmcp.CallToolParams{
		Name: "acline_approve", Arguments: approveArgs{ID: taskID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected an agent approving with no 'by' to be rejected -- an agent cannot approve its own work")
	}

	// Naming a person is not proof: identity is self-declared until a token exists.
	res, err = cs.CallTool(context.Background(), &sdkmcp.CallToolParams{
		Name: "acline_approve", Arguments: approveArgs{ID: taskID, By: "a-human"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("an agent approving with by=<person> and no token must be refused")
	}
	if approvals, _ := st.ListApprovals(taskID); len(approvals) != 0 {
		t.Fatalf("a refused approval was recorded: %+v", approvals)
	}
}

func TestApproveDoesNotRequireByForHumanActor(t *testing.T) {
	cs, st := connectedTestServer(t) // human actor
	taskID, err := st.AddTask("high-risk task", "", "normal", store.TaskOpts{Risk: "high"})
	if err != nil {
		t.Fatal(err)
	}
	out := callTool[approveOut](t, cs, "acline_approve", approveArgs{ID: taskID})
	if out.ID == 0 {
		t.Fatal("expected a human actor to approve without 'by'")
	}
}

func TestHighRiskTaskDoneRequiresApprovalEvenWithPassingCheck(t *testing.T) {
	cs, st := connectedTestServer(t)
	taskID, err := st.AddTask("high-risk task", "", "normal", store.TaskOpts{Risk: "high"})
	if err != nil {
		t.Fatal(err)
	}
	// high risk needs a check acline ran, not one typed in by hand, about the
	// code as it is now
	if _, err := st.AddCheckWithMeta(taskID, nil, "test", "pass", "", "", store.CheckMeta{Source: store.CheckSourceRunner, TreeHash: treeForTask(st, taskID)}); err != nil {
		t.Fatal(err)
	}

	// Check alone isn't enough for a high-risk task -- still needs approval.
	res, err := cs.CallTool(context.Background(), &sdkmcp.CallToolParams{
		Name: "acline_task_done", Arguments: taskDoneArgs{ID: taskID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected a high-risk task to still block on missing approval")
	}

	callTool[approveOut](t, cs, "acline_approve", approveArgs{ID: taskID})
	out := callTool[taskDoneOut](t, cs, "acline_task_done", taskDoneArgs{ID: taskID})
	if out.Overridden {
		t.Fatal("expected Overridden=false once approved")
	}
}

func TestReject(t *testing.T) {
	cs, st := connectedTestServer(t)
	taskID, err := st.AddTask("a task", "", "normal", store.TaskOpts{Risk: "low"})
	if err != nil {
		t.Fatal(err)
	}
	out := callTool[rejectOut](t, cs, "acline_reject", rejectArgs{ID: taskID, Note: "needs rework"})
	if out.ID == 0 {
		t.Fatal("expected a non-zero approval id")
	}
	approvals, err := st.ListApprovals(taskID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range approvals {
		if a.Decision == "rejected" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected a rejected approval to be recorded")
	}
}

// Evidence a person is meant to supply cannot be supplied by the agent's own server.
func TestAgentServerCannotRecordHumanReviewOrVerifyADependency(t *testing.T) {
	cs, st := connectedTestServerWithActor(t, store.Actor{Type: "agent", ID: "claude-code", Model: "claude-sonnet-5"})
	taskID, err := st.AddTask("t", "", "normal", store.TaskOpts{})
	if err != nil {
		t.Fatal(err)
	}
	depID, err := st.AddDependency(nil, nil, "go", "example.com/x", "1.0.0", false)
	if err != nil {
		t.Fatal(err)
	}
	refused := func(name string, args any, wantCode string) {
		t.Helper()
		res, err := cs.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		if !res.IsError {
			t.Fatalf("%s: expected a refusal", name)
		}
		if code, _ := res.Meta["error_code"].(string); code != wantCode {
			t.Errorf("%s: error_code = %q, want %q", name, code, wantCode)
		}
	}
	refused("acline_check_record", checkRecordArgs{TaskID: taskID, Kind: "human_review", Status: "pass"}, "agent_cannot_record_human_review")
	refused("acline_dep_verify", depVerifyArgs{ID: depID}, "agent_cannot_verify_dependency")
	refused("acline_dep_add", depAddArgs{Ecosystem: "go", Name: "example.com/y", Verified: true}, "agent_cannot_verify_dependency")

	if g, _ := st.EvaluateGate(taskID); g.OK() {
		t.Fatal("gate satisfied")
	}
	// ordinary evidence and an unverified dependency are still fine
	callTool[checkRecordOut](t, cs, "acline_check_record", checkRecordArgs{TaskID: taskID, Kind: "test", Status: "pass"})
	callTool[depAddOut](t, cs, "acline_dep_add", depAddArgs{Ecosystem: "go", Name: "example.com/z"})
}
