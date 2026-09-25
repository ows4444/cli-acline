package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"acline/internal/store"
)

// stopFixture is a store with an active session on one task, and the working
// tree's fingerprint pinned to tree.
func stopFixture(t *testing.T, tree string) (*store.Store, int64) {
	t.Helper()
	t.Setenv("ACLINE_PROJECT", "")
	s := withTestStore(t)
	prev := stopTree
	stopTree = func() string { return tree }
	t.Cleanup(func() { stopTree = prev })
	taskID, err := s.AddTask("stop hook task", "", "normal", store.TaskOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartSession(&taskID, nil, nil, ""); err != nil {
		t.Fatal(err)
	}
	return s, taskID
}

func runStopFor(t *testing.T, payload string) string {
	t.Helper()
	var out bytes.Buffer
	if err := runStop(strings.NewReader(payload), &out, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func addRunnerCheck(t *testing.T, s *store.Store, taskID int64, kind, status, tree string) {
	t.Helper()
	meta := store.CheckMeta{Source: store.CheckSourceRunner, TreeHash: tree}
	if _, err := s.AddCheckWithMeta(taskID, nil, kind, status, "", "", meta); err != nil {
		t.Fatal(err)
	}
}

func TestStopBlocksWhenPassingChecksPredateTheLastEdit(t *testing.T) {
	s, taskID := stopFixture(t, "sha256:new")
	addRunnerCheck(t, s, taskID, "test", "pass", "sha256:old")
	addRunnerCheck(t, s, taskID, "lint", "pass", "sha256:old")

	out := runStopFor(t, `{"session_id":"s1"}`)
	var got struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.Decision != "block" {
		t.Fatalf("output = %q (%v), want a block", out, err)
	}
	if !strings.Contains(got.Reason, "[lint test]") || !strings.Contains(got.Reason, "acline check run") {
		t.Fatalf("reason = %q", got.Reason)
	}
}

func TestStopLetsTheTurnEnd(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		setup   func(t *testing.T, s *store.Store, taskID int64)
	}{
		{"a check passed against the current tree", `{}`, func(t *testing.T, s *store.Store, id int64) {
			addRunnerCheck(t, s, id, "lint", "pass", "sha256:old")
			addRunnerCheck(t, s, id, "test", "pass", "sha256:new")
		}},
		{"no checks yet", `{}`, func(*testing.T, *store.Store, int64) {}},
		{"only a hand-recorded pass", `{}`, func(t *testing.T, s *store.Store, id int64) {
			if _, err := s.AddCheck(id, "eval", "pass", ""); err != nil {
				t.Fatal(err)
			}
		}},
		{"the newest result is a failure", `{}`, func(t *testing.T, s *store.Store, id int64) {
			addRunnerCheck(t, s, id, "test", "pass", "sha256:old")
			addRunnerCheck(t, s, id, "test", "fail", "sha256:old")
		}},
		{"already continuing because of a Stop hook", `{"stop_hook_active":true}`, func(t *testing.T, s *store.Store, id int64) {
			addRunnerCheck(t, s, id, "test", "pass", "sha256:old")
		}},
		{"the task is cancelled", `{}`, func(t *testing.T, s *store.Store, id int64) {
			addRunnerCheck(t, s, id, "test", "pass", "sha256:old")
			if err := s.SetTaskStatus(id, "cancelled"); err != nil {
				t.Fatal(err)
			}
		}},
		{"no active session", `{}`, func(t *testing.T, s *store.Store, id int64) {
			addRunnerCheck(t, s, id, "test", "pass", "sha256:old")
			if _, err := s.EndSession("done", store.SessionCost{}); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, taskID := stopFixture(t, "sha256:new")
			c.setup(t, s, taskID)
			if out := runStopFor(t, c.payload); out != "" {
				t.Fatalf("blocked: %q", out)
			}
		})
	}
}

func TestStopIgnoresAnotherProjectsSession(t *testing.T) {
	s, _ := stopFixture(t, "sha256:new")
	if _, err := s.EndSession("", store.SessionCost{}); err != nil {
		t.Fatal(err)
	}
	projectID, err := s.AddProject("elsewhere", t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := s.AddTask("other project's task", "", "normal", store.TaskOpts{ProjectID: &projectID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartSession(&taskID, &projectID, nil, ""); err != nil {
		t.Fatal(err)
	}
	addRunnerCheck(t, s, taskID, "test", "pass", "sha256:old")
	if out := runStopFor(t, `{}`); out != "" {
		t.Fatalf("blocked on another project's session: %q", out)
	}
}

func TestStopFailsOpenWhenTheTreeHashIsSlow(t *testing.T) {
	s, taskID := stopFixture(t, "sha256:new")
	addRunnerCheck(t, s, taskID, "test", "pass", "sha256:old")
	stopTree = func() string { time.Sleep(time.Second); return "sha256:new" }
	prev := stopTreeTimeout
	stopTreeTimeout = 10 * time.Millisecond
	t.Cleanup(func() { stopTreeTimeout = prev })
	if out := runStopFor(t, `{}`); out != "" {
		t.Fatalf("blocked without a tree hash: %q", out)
	}
}

func TestStopRecordsTheTurnsMemoryLogLines(t *testing.T) {
	stopFixture(t, "sha256:new")
	calls := fakeSelf(t, func([]string) (string, error) { return "", nil })
	transcript := writeTranscript(t, assistantEntry("MEMORY_LOG: stop captures this"))
	runStopFor(t, `{"session_id":"s1","transcript_path":"`+transcript+`"}`)
	if len(*calls) != 1 || (*calls)[0].args[2] != "stop captures this" {
		t.Fatalf("calls = %+v", *calls)
	}
}
