package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"acline/internal/store"
)

// connectedTestServer opens a fresh store, builds the acline MCP server
// around it, and connects a real MCP client to it over an in-memory
// transport (not a subprocess) — this exercises the actual MCP wire
// protocol (JSON-RPC framing, schema validation, tool dispatch), not just
// the Go functions underneath it.
func connectedTestServer(t *testing.T) (*sdkmcp.ClientSession, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	st.Actor = store.Actor{Type: "human", ID: "tester"}
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

func callTool[Out any](t *testing.T, cs *sdkmcp.ClientSession, name string, args any) Out {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	if res.IsError {
		t.Fatalf("CallTool(%s) returned an error result: %+v", name, res.Content)
	}
	data, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshaling structured content: %v", err)
	}
	var out Out
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshaling %s result into %T: %v (raw: %s)", name, out, err, data)
	}
	return out
}

func TestServerListsAllTools(t *testing.T) {
	cs, _ := connectedTestServer(t)
	want := map[string]bool{
		"acline_search": true, "acline_memory_list": true, "acline_memory_add": true, "acline_memory_decay": true,
		"acline_task_list": true, "acline_task_add": true, "acline_decision_list": true, "acline_decision_add": true,
		"acline_spec_list": true, "acline_spec_add": true, "acline_note_add": true, "acline_log": true,
		"acline_task_gate": true, "acline_task_done": true, "acline_approve": true, "acline_reject": true,
		"acline_check_record": true, "acline_check_list": true, "acline_memory_touch": true, "acline_memory_forget": true,
		"acline_decision_accept": true, "acline_decision_reject": true, "acline_decision_supersede": true,
		"acline_spec_approve": true, "acline_spec_revise": true,
		"acline_dep_add": true, "acline_dep_list": true, "acline_dep_verify": true,
		"acline_dashboard": true, "acline_metrics": true, "acline_verify": true, "acline_project_list": true,
	}
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, tool := range res.Tools {
		got[tool.Name] = true
	}
	for name := range want {
		if !got[name] {
			t.Errorf("expected tool %q to be registered, got tools: %v", name, got)
		}
	}
}

func TestMemoryAddThenListRoundTrips(t *testing.T) {
	cs, _ := connectedTestServer(t)

	addOut := callTool[memoryAddOut](t, cs, "acline_memory_add", memoryAddArgs{
		Area: "cli", Kind: "lesson", Body: "we picked postgres for durability concerns",
	})
	if addOut.ID == 0 {
		t.Fatal("expected a non-zero memory id")
	}
	if addOut.PendingReview {
		t.Error("expected a human-actor entry to not be pending review")
	}

	listOut := callTool[memoryListOut](t, cs, "acline_memory_list", memoryListArgs{Status: "approved"})
	found := false
	for _, m := range listOut.Entries {
		if m.ID == addOut.ID {
			found = true
			if m.Body != "we picked postgres for durability concerns" {
				t.Errorf("unexpected body: %q", m.Body)
			}
		}
	}
	if !found {
		t.Fatalf("expected memory #%d in the list, got %+v", addOut.ID, listOut.Entries)
	}
}

func TestMemoryAddRedactsSecrets(t *testing.T) {
	cs, _ := connectedTestServer(t)
	out := callTool[memoryAddOut](t, cs, "acline_memory_add", memoryAddArgs{
		Body: "rotate this key: AKIAIOSFODNN7EXAMPLE",
	})
	if !out.SecretsRedacted {
		t.Fatal("expected SecretsRedacted=true")
	}

	listOut := callTool[memoryListOut](t, cs, "acline_memory_list", memoryListArgs{})
	for _, m := range listOut.Entries {
		if m.ID == out.ID && (m.Body == "rotate this key: AKIAIOSFODNN7EXAMPLE" || len(m.Body) > 0 && m.Body[len(m.Body)-20:] == "AKIAIOSFODNN7EXAMPLE") {
			t.Fatalf("secret leaked into stored memory body: %q", m.Body)
		}
	}
}

func TestSearchFindsAddedMemory(t *testing.T) {
	cs, _ := connectedTestServer(t)
	callTool[memoryAddOut](t, cs, "acline_memory_add", memoryAddArgs{Body: "the widget cache uses LRU eviction"})

	out := callTool[searchOut](t, cs, "acline_search", searchArgs{Query: "widget"})
	if len(out.Hits) == 0 {
		t.Fatal("expected at least one hit for 'widget'")
	}
}

func TestSearchSemanticErrorsWithoutEmbedder(t *testing.T) {
	cs, _ := connectedTestServer(t)
	res, err := cs.CallTool(context.Background(), &sdkmcp.CallToolParams{
		Name: "acline_search", Arguments: searchArgs{Query: "why postgres", Semantic: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected an error result when semantic=true with no embedder configured")
	}
}

func TestTaskAddThenList(t *testing.T) {
	cs, _ := connectedTestServer(t)
	addOut := callTool[taskAddOut](t, cs, "acline_task_add", taskAddArgs{Title: "fix the flaky test", Area: "cli"})
	if addOut.ID == 0 {
		t.Fatal("expected a non-zero task id")
	}
	listOut := callTool[taskListOut](t, cs, "acline_task_list", taskListArgs{})
	found := false
	for _, task := range listOut.Tasks {
		if task.ID == addOut.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected task #%d in the list", addOut.ID)
	}
}

func TestDecisionAddThenList(t *testing.T) {
	cs, _ := connectedTestServer(t)
	addOut := callTool[decisionAddOut](t, cs, "acline_decision_add", decisionAddArgs{
		Title: "use postgres", Rationale: "durability concerns",
	})
	listOut := callTool[decisionListOut](t, cs, "acline_decision_list", decisionListArgs{})
	found := false
	for _, d := range listOut.Decisions {
		if d.ID == addOut.ID {
			found = true
			if d.Status != "proposed" {
				t.Errorf("expected a new decision to be status=proposed, got %q", d.Status)
			}
		}
	}
	if !found {
		t.Fatalf("expected decision #%d in the list", addOut.ID)
	}
}

func TestSpecAddThenList(t *testing.T) {
	cs, _ := connectedTestServer(t)
	addOut := callTool[specAddOut](t, cs, "acline_spec_add", specAddArgs{Title: "auth spec", Body: "..."})
	listOut := callTool[specListOut](t, cs, "acline_spec_list", specListArgs{})
	found := false
	for _, sp := range listOut.Specs {
		if sp.ID == addOut.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected spec #%d in the list", addOut.ID)
	}
}

func TestNoteAddAndLog(t *testing.T) {
	cs, _ := connectedTestServer(t)
	noteOut := callTool[noteAddOut](t, cs, "acline_note_add", noteAddArgs{Body: "TODO: revisit caching"})
	if noteOut.ID == 0 {
		t.Fatal("expected a non-zero note id")
	}
	logOut := callTool[logOut](t, cs, "acline_log", logArgs{Message: "fixed the bug", Type: "bug"})
	if logOut.ID == 0 {
		t.Fatal("expected a non-zero event id")
	}
}

func TestLogRejectsInvalidType(t *testing.T) {
	cs, _ := connectedTestServer(t)
	res, err := cs.CallTool(context.Background(), &sdkmcp.CallToolParams{
		Name: "acline_log", Arguments: logArgs{Message: "x", Type: "not-a-real-type"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected an error result for an invalid log type")
	}
}

func TestEventsResourceReturnsLoggedEvents(t *testing.T) {
	cs, st := connectedTestServer(t)
	if _, err := st.LogEvent(nil, nil, "note", "a test event"); err != nil {
		t.Fatal(err)
	}
	res, err := cs.ReadResource(context.Background(), &sdkmcp.ReadResourceParams{URI: eventsResourceURI})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Contents) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(res.Contents))
	}
	var events []eventOut
	if err := json.Unmarshal([]byte(res.Contents[0].Text), &events); err != nil {
		t.Fatalf("unmarshaling events resource: %v", err)
	}
	found := false
	for _, e := range events {
		if e.Message == "a test event" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the logged event in the resource, got %+v", events)
	}
}

func TestMemoryDecayViaMCP(t *testing.T) {
	cs, st := connectedTestServer(t)
	addOut := callTool[memoryAddOut](t, cs, "acline_memory_add", memoryAddArgs{Body: "an aging lesson"})

	if _, err := st.DB.Exec(`UPDATE memory SET created_at = ?, reviewed_at = ? WHERE id = ?`,
		"2020-01-01T00:00:00Z", "2020-01-01T00:00:00Z", addOut.ID); err != nil {
		t.Fatal(err)
	}

	out := callTool[memoryListOut](t, cs, "acline_memory_decay", memoryDecayArgs{Days: 90})
	found := false
	for _, m := range out.Entries {
		if m.ID == addOut.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected memory #%d to be a decay candidate", addOut.ID)
	}
}
