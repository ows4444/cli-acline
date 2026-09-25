package mcp

import (
	"context"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"acline/internal/store"
)

func TestServerInfoReportsDBAndActor(t *testing.T) {
	cs, st := connectedTestServer(t)
	out := callTool[serverInfoOut](t, cs, "acline_server_info", serverInfoArgs{})
	if out.Version != Version {
		t.Errorf("version = %q, want %q", out.Version, Version)
	}
	if out.DBPath == "" || out.DBPath != st.Path {
		t.Errorf("db_path = %q, want %q", out.DBPath, st.Path)
	}
	if out.ActorType != st.Actor.Type || out.ActorID != st.Actor.ID {
		t.Errorf("actor = %s/%s, want %s/%s", out.ActorType, out.ActorID, st.Actor.Type, st.Actor.ID)
	}
}

func TestMemoryReviewToolsEnforceActor(t *testing.T) {
	cs, st := connectedTestServer(t)
	id, err := st.AddMemory("cli", "lesson", "pending entry")
	if err != nil {
		t.Fatal(err)
	}
	callTool[memoryIDOut](t, cs, "acline_memory_approve", memoryIDArgs{ID: id})
	entries, _ := st.ListMemory(store.MemoryFilter{Status: "approved"})
	if len(entries) != 1 {
		t.Fatalf("approved entries = %d, want 1", len(entries))
	}

	agentCS, agentSt := connectedTestServerWithActor(t, store.Actor{Type: "agent", ID: "claude-code"})
	aid, _ := agentSt.AddMemory("cli", "lesson", "agent entry")
	res, err := agentCS.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: "acline_memory_reject", Arguments: memoryIDArgs{ID: aid}})
	if err == nil && !res.IsError {
		t.Fatal("an agent-actor server must refuse memory review")
	}
}

func callToolRaw(t *testing.T, cs *sdkmcp.ClientSession, name string, args any) *sdkmcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	return res
}

func TestApprovalTokenIsPerCallAndNeverReadFromTheServerEnvironment(t *testing.T) {
	cs, st := connectedTestServer(t)
	// A token in the server's own environment must NOT authorize anything: any
	// client of that server would otherwise inherit the human's secret.
	token, err := st.EnableApprovalToken()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("ACLINE_APPROVAL_TOKEN", token)

	info := callTool[serverInfoOut](t, cs, "acline_server_info", serverInfoArgs{})
	if !info.ApprovalTokenEnabled {
		t.Error("server_info must tell clients a token is required")
	}

	taskID, _ := st.AddTask("t", "", "normal", store.TaskOpts{Risk: "high"})
	if r := callToolRaw(t, cs, "acline_approve", approveArgs{ID: taskID, By: "alice"}); !r.IsError {
		t.Fatal("approve without a token argument succeeded (env token must not count)")
	}
	if r := callToolRaw(t, cs, "acline_approve", approveArgs{ID: taskID, By: "alice", Token: "acl_wrong"}); !r.IsError {
		t.Fatal("approve with a wrong token succeeded")
	}
	if r := callToolRaw(t, cs, "acline_approve", approveArgs{ID: taskID, By: "alice", Token: token}); r.IsError {
		t.Fatalf("approve with the right token failed: %+v", r.Content)
	}

	// force
	t2, _ := st.AddTask("t2", "", "normal", store.TaskOpts{Risk: "high"})
	if r := callToolRaw(t, cs, "acline_task_done", taskDoneArgs{ID: t2, Force: true}); !r.IsError {
		t.Fatal("force without a token succeeded")
	}
	if r := callToolRaw(t, cs, "acline_task_done", taskDoneArgs{ID: t2, Force: true, Token: token}); r.IsError {
		t.Fatalf("force with token failed: %+v", r.Content)
	}

	// memory
	mem, _ := st.AddMemory("cli", "lesson", "x")
	if r := callToolRaw(t, cs, "acline_memory_approve", memoryReviewArgs{ID: mem}); !r.IsError {
		t.Fatal("memory approve without a token succeeded")
	}
	if r := callToolRaw(t, cs, "acline_memory_approve", memoryReviewArgs{ID: mem, Token: token}); r.IsError {
		t.Fatalf("memory approve with token failed: %+v", r.Content)
	}
}
