package cmd

import (
	"strings"
	"testing"

	"acline/internal/store"
)

// Only denials reached the audit trail; a reviewer could not see what an
// agent changed during a session.
func TestPostToolUseRecordsWritesAndCommandsOnTheSession(t *testing.T) {
	s := withTestStore(t)
	t.Chdir(t.TempDir())
	task, _ := s.AddTask("t", "", "normal", store.TaskOpts{})
	sid, err := s.BeginSession(store.SessionStart{TaskID: &task})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		`{"tool_name":"Edit","tool_input":{"file_path":"/p/main.go"}}`,
		`{"tool_name":"Bash","tool_input":{"command":"go test ./...\n  -run X"}}`,
		`{"tool_name":"Read","tool_input":{"file_path":"/p/main.go"}}`,
		`{"tool_name":"Bash","tool_input":{"command":"curl -H 'Authorization: Bearer ghp_abcdefghijklmnopqrstuvwxyz0123456789' x"}}`,
		`not json`,
	} {
		recordToolUse(s, strings.NewReader(p))
	}
	events, _ := s.QueryEvents(store.EventFilter{Limit: 50})
	var got []string
	for _, e := range events {
		if e.Type == "tool_used" {
			if !e.SessionID.Valid || e.SessionID.Int64 != sid || !e.TaskID.Valid || e.TaskID.Int64 != task {
				t.Errorf("event not on the session and task: %+v", e)
			}
			got = append(got, e.Message)
		}
	}
	all := strings.Join(got, "\n")
	if len(got) != 3 || !strings.Contains(all, "Edit /p/main.go") || !strings.Contains(all, "go test ./... -run X") {
		t.Fatalf("tool_used events = %q", got)
	}
	if strings.Contains(all, "ghp_abcdefghijklmnopqrstuvwxyz0123456789") {
		t.Fatal("a secret in a command reached the audit trail")
	}
}

func TestPostToolUseRecordsNothingWithoutASession(t *testing.T) {
	s := withTestStore(t)
	t.Chdir(t.TempDir())
	recordToolUse(s, strings.NewReader(`{"tool_name":"Edit","tool_input":{"file_path":"/p/x"}}`))
	if events, _ := s.QueryEvents(store.EventFilter{Limit: 10}); len(events) != 0 {
		t.Fatalf("events without a session: %+v", events)
	}
}
