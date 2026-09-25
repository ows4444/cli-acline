package mcp

import (
	"context"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"acline/internal/store"
)

// TestTaskAddAndApproveResolveRole is the MCP mirror of the CLI's --role
// flag (acline spec #2, phase 5): acline_task_add resolves role by name,
// and acline_approve's role is checked by EvaluateGate the same way the
// CLI's is once a project has its own can_approve role -- an unknown role
// name is refused rather than silently ignored.
func TestTaskAddAndApproveResolveRole(t *testing.T) {
	cs, st := connectedTestServer(t)

	if _, err := st.AddProject("demo", "/tmp/mcp-role-demo", "hotl"); err != nil {
		t.Fatal(err)
	}

	taskOut := callTool[taskAddOut](t, cs, "acline_task_add", taskAddArgs{
		Title: "verify it", Risk: "high", Project: "demo", Role: "qa",
	})
	task, err := st.GetTask(taskOut.ID)
	if err != nil {
		t.Fatal(err)
	}
	qaRole, err := st.GetRoleByName(nil, "qa")
	if err != nil {
		t.Fatal(err)
	}
	if !task.RoleID.Valid || task.RoleID.Int64 != qaRole.ID {
		t.Fatalf("expected task.role_id = qa (%d), got %+v", qaRole.ID, task.RoleID)
	}

	if res, err := cs.CallTool(context.Background(), &sdkmcp.CallToolParams{
		Name: "acline_task_add", Arguments: taskAddArgs{Title: "x", Role: "not-a-real-role"},
	}); err != nil || !res.IsError {
		t.Fatalf("expected an unknown role to be refused, err=%v res=%+v", err, res)
	}

	// high risk needs a check acline ran, not one typed in by hand
	if _, err := st.AddCheckWithMeta(taskOut.ID, nil, "test", "pass", "", "", store.CheckMeta{Source: store.CheckSourceRunner}); err != nil {
		t.Fatal(err)
	}

	// No project-scoped can_approve role yet: a qa-role approval must still
	// satisfy the gate, exactly like before roles existed.
	callTool[approveOut](t, cs, "acline_approve", approveArgs{ID: taskOut.ID, By: "alice", Role: "qa", Project: "demo"})
	gate, err := st.EvaluateGate(taskOut.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !gate.OK() {
		t.Fatalf("expected the gate to pass with no can_approve role configured, got blockers: %v", gate.Blockers)
	}

	// Project opts in: now a qa-role approval on a *new* task must not be
	// enough, and a can_approve role must be.
	if _, err := st.AddRole(func() int64 { p, _ := st.GetProjectByName("demo"); return p.ID }(), "release-manager", "human", true, nil, ""); err != nil {
		t.Fatal(err)
	}
	taskOut2 := callTool[taskAddOut](t, cs, "acline_task_add", taskAddArgs{Title: "verify it 2", Risk: "high", Project: "demo"})
	if _, err := st.AddCheckWithMeta(taskOut2.ID, nil, "test", "pass", "", "", store.CheckMeta{Source: store.CheckSourceRunner}); err != nil {
		t.Fatal(err)
	}
	callTool[approveOut](t, cs, "acline_approve", approveArgs{ID: taskOut2.ID, By: "alice", Role: "qa", Project: "demo"})
	gate2, err := st.EvaluateGate(taskOut2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gate2.OK() {
		t.Fatal("expected a qa-role approval to be insufficient once the project has a can_approve role")
	}

	callTool[approveOut](t, cs, "acline_approve", approveArgs{ID: taskOut2.ID, By: "alice", Role: "release-manager", Project: "demo"})
	gate3, err := st.EvaluateGate(taskOut2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !gate3.OK() {
		t.Fatalf("expected a release-manager-role approval to satisfy the gate, got blockers: %v", gate3.Blockers)
	}
}
