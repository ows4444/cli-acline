package mcp

import (
	"context"
	"fmt"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"acline/internal/store"
)

// Tools carried no annotations, so a client could not tell a read from a
// write, and the policy middleware relied on a hand-kept list nobody checked.
func TestEveryToolIsAnnotatedFromOneClassification(t *testing.T) {
	cs, _ := connectedTestServer(t)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range res.Tools {
		names[tool.Name] = true
		a := tool.Annotations
		if a == nil || a.Title == "" || a.OpenWorldHint == nil {
			t.Errorf("%s: missing annotations: %+v", tool.Name, a)
			continue
		}
		if a.ReadOnlyHint != readOnlyTools[tool.Name] {
			t.Errorf("%s: readOnlyHint=%v, readOnlyTools says %v", tool.Name, a.ReadOnlyHint, readOnlyTools[tool.Name])
		}
		if !a.ReadOnlyHint && a.DestructiveHint == nil {
			t.Errorf("%s: a write tool without destructiveHint", tool.Name)
		}
	}
	for _, list := range []map[string]bool{readOnlyTools, destructiveTools, idempotentTools, openWorldTools} {
		for name := range list {
			if !names[name] {
				t.Errorf("classification lists %s, which the server does not register", name)
			}
		}
	}
	for name := range destructiveTools {
		if readOnlyTools[name] {
			t.Errorf("%s is both read-only and destructive", name)
		}
	}
}

// The audit-trail resource mixed every project's events; templates give a
// project's and a task's own trail.
func TestEventResourceTemplatesScopeTheTrail(t *testing.T) {
	cs, st := connectedTestServer(t)
	pa, _ := st.AddProject("alpha", "", "")
	pb, _ := st.AddProject("beta", "", "")
	ta, _ := st.AddTask("a", "", "normal", store.TaskOpts{ProjectID: &pa})
	tb, _ := st.AddTask("b", "", "normal", store.TaskOpts{ProjectID: &pb})
	st.LogTaskEvent(ta, "note", "alpha work")
	st.LogTaskEvent(tb, "note", "beta work")

	read := func(uri string) (string, error) {
		res, err := cs.ReadResource(context.Background(), &sdkmcp.ReadResourceParams{URI: uri})
		if err != nil {
			return "", err
		}
		return res.Contents[0].Text, nil
	}
	got, err := read("acline://projects/alpha/events")
	if err != nil || !strings.Contains(got, "alpha work") || strings.Contains(got, "beta work") {
		t.Fatalf("project trail = %q, %v", got, err)
	}
	got, err = read(fmt.Sprintf("acline://tasks/%d/events", tb))
	if err != nil || !strings.Contains(got, "beta work") || strings.Contains(got, "alpha work") {
		t.Fatalf("task trail = %q, %v", got, err)
	}
	for _, bad := range []string{"acline://projects/nope/events", "acline://tasks/999/events", "acline://tasks/x/events"} {
		if _, err := read(bad); err == nil {
			t.Errorf("%s: expected not found", bad)
		}
	}
}

// Every client, agents included, was offered all ~70 tools, including the
// decisions only a person may make.
func TestToolsetsExposeOnlyTheirTools(t *testing.T) {
	list := func(toolset string) map[string]bool {
		t.Helper()
		st, err := store.Open(t.TempDir() + "/t.db")
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		server, err := NewServerWithToolset(st, toolset)
		if err != nil {
			t.Fatal(err)
		}
		t1, t2 := sdkmcp.NewInMemoryTransports()
		if _, err := server.Connect(context.Background(), t1, nil); err != nil {
			t.Fatal(err)
		}
		cs, err := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "c", Version: "0"}, nil).Connect(context.Background(), t2, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer cs.Close()
		res, err := cs.ListTools(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		names := map[string]bool{}
		for _, tool := range res.Tools {
			names[tool.Name] = true
		}
		return names
	}
	all, capture, read := list(ToolsetAll), list(ToolsetCapture), list(ToolsetRead)
	for name := range decisionTools {
		if !all[name] {
			t.Errorf("decisionTools lists %s, which the server does not register", name)
		}
		if capture[name] || read[name] {
			t.Errorf("%s is a person's decision but capture/read offers it", name)
		}
	}
	for _, name := range []string{"acline_task_add", "acline_check_run", "acline_note_add", "acline_search"} {
		if !capture[name] {
			t.Errorf("capture is missing %s", name)
		}
	}
	for name := range read {
		if !readOnlyTools[name] {
			t.Errorf("read offers %s, which writes", name)
		}
	}
	if len(read) == 0 || len(capture) >= len(all) {
		t.Fatalf("sizes: read=%d capture=%d all=%d", len(read), len(capture), len(all))
	}
	if _, err := NewServerWithToolset(nil, "everything"); err == nil {
		t.Error("an unknown toolset was accepted")
	}
	if DefaultToolset(store.Actor{Type: "agent"}) != ToolsetCapture || DefaultToolset(store.Actor{Type: "human"}) != ToolsetAll {
		t.Error("default toolset by actor")
	}
}
