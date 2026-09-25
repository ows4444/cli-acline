package mcp

import (
	"context"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"acline/internal/store"
)

// refused reports whether a call was refused, either as a protocol error (what
// the policy middleware returns) or as a tool error result.
func refused(cs *sdkmcp.ClientSession, name string, args any) bool {
	res, err := cs.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: name, Arguments: args})
	return err != nil || res.IsError
}

func startPolicy(t *testing.T, st *store.Store, p store.Policy) {
	t.Helper()
	js, err := p.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.StartSession(nil, nil, nil, js); err != nil {
		t.Fatal(err)
	}
}

func TestSessionPolicyGatesMutatingMCPTools(t *testing.T) {
	cs, st := connectedTestServer(t)
	id, _ := st.AddTask("t", "", "normal", store.TaskOpts{})
	startPolicy(t, st, store.Policy{DenyTools: []string{"acline_task_done", "acline_memory_add"}})

	if !refused(cs, "acline_task_done", taskDoneArgs{ID: id, Force: true}) {
		t.Fatal("a denied tool ran")
	}
	if got, _ := st.GetTask(id); got.Status == "done" {
		t.Fatal("task completed despite the policy denial")
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM events WHERE type='policy_violation'`); n != 1 {
		t.Errorf("policy_violation events = %d, want 1", n)
	}
	// unrelated mutating tool and read-only tools still work
	callTool[taskAddOut](t, cs, "acline_task_add", taskAddArgs{Title: "other"})
	callTool[taskListOut](t, cs, "acline_task_list", taskListArgs{})
}

func TestAllowListPolicyBlocksWritesButNeverReads(t *testing.T) {
	cs, st := connectedTestServer(t)
	startPolicy(t, st, store.Policy{AllowTools: []string{"Read", "Edit"}}) // an agent file-tools policy
	if !refused(cs, "acline_task_add", taskAddArgs{Title: "x"}) {
		t.Fatal("a mutating tool outside the allow-list ran")
	}
	for _, name := range []string{"acline_task_list", "acline_dashboard", "acline_server_info", "acline_verify"} {
		if r := callToolRaw(t, cs, name, map[string]any{}); r.IsError {
			t.Errorf("read-only tool %s was blocked by an unrelated allow-list: %+v", name, r.Content)
		}
	}
}

func TestNoSessionMeansNoMCPPolicy(t *testing.T) {
	cs, _ := connectedTestServer(t)
	callTool[taskAddOut](t, cs, "acline_task_add", taskAddArgs{Title: "free"})
}

// Every tool the server registers must be classified: either read-only (listed)
// or, by omission, mutating. This catches a *new read-only* tool that was
// forgotten in readOnlyTools and would be wrongly gated.
func TestReadOnlyToolsAreAllRealTools(t *testing.T) {
	cs, _ := connectedTestServer(t)
	res, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, tool := range res.Tools {
		have[tool.Name] = true
	}
	for name := range readOnlyTools {
		if !have[name] {
			t.Errorf("readOnlyTools lists %q, which is not a registered tool", name)
		}
	}
}
