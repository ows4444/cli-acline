package store

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// forgedApprovalSnapshot is a snapshot that adds one code_review approval
// (by "alice") for task taskID and nothing else.
func forgedApprovalSnapshot(taskID string) string {
	return `{"version":2,"exported_at":"2026-01-01T00:00:00Z","tables":{"approvals":[` +
		`{"id":9001,"task_id":` + taskID + `,"kind":"code_review","approver":"alice","decision":"approved",` +
		`"note":"lgtm","actor_type":"human","actor_id":"alice","model":null,"role_id":null,"created_at":"2026-01-01T00:00:00Z"}]}}`
}

// An agent could satisfy a high-risk task's approval gate by importing a
// snapshot that carries a forged approval row, because the import merged rows
// into a live store with no privilege check.
func TestSnapshotImportRefusesAnAgentIntoANonEmptyStore(t *testing.T) {
	h := humanStore(t)
	id, err := h.AddTask("risky", "", "normal", TaskOpts{Risk: "high"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.AddCheck(id, "test", "pass", ""); err != nil {
		t.Fatal(err)
	}

	a := *h
	a.Actor = Actor{Type: "agent", ID: "planner"}
	_, err = a.LoadSnapshot(strings.NewReader(forgedApprovalSnapshot("1")))
	if !errors.Is(err, ErrAgentCannotImportSnapshot) {
		t.Fatalf("agent import into a non-empty store = %v, want ErrAgentCannotImportSnapshot", err)
	}
	if approvals, _ := h.ListApprovals(id); len(approvals) != 0 {
		t.Fatalf("a refused import still wrote %d approval(s)", len(approvals))
	}
	if g, _ := h.EvaluateGate(id); g.OK() {
		t.Fatal("gate satisfied by a forged approval")
	}
}

func TestSnapshotImportAllowsAnAgentIntoAnEmptyStore(t *testing.T) {
	src := humanStore(t)
	if _, err := src.AddTask("seed", "", "normal", TaskOpts{}); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := src.SnapshotJSON(&buf); err != nil {
		t.Fatal(err)
	}
	dst := agentStore(t) // `acline init` seeds a brand-new store, possibly from an agent's shell
	if _, err := dst.LoadSnapshot(&buf); err != nil {
		t.Fatalf("seeding an empty store as an agent = %v", err)
	}
	if tasks, _ := dst.ListTasks(TaskFilter{}); len(tasks) != 1 {
		t.Fatalf("seeded %d tasks, want 1", len(tasks))
	}
}

func TestSnapshotImportAllowsAHumanIntoANonEmptyStore(t *testing.T) {
	h := humanStore(t)
	if _, err := h.AddTask("existing", "", "normal", TaskOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.LoadSnapshot(strings.NewReader(forgedApprovalSnapshot("1"))); err != nil {
		t.Fatalf("human import = %v", err)
	}
}

func TestSnapshotImportNeedsTheTokenWhenOneIsEnabled(t *testing.T) {
	h := humanStore(t)
	if _, err := h.AddTask("existing", "", "normal", TaskOpts{}); err != nil {
		t.Fatal(err)
	}
	tok, err := h.EnableApprovalToken()
	if err != nil {
		t.Fatal(err)
	}
	snap := forgedApprovalSnapshot("1")

	if _, err := h.LoadSnapshotWithToken(strings.NewReader(snap), ""); !errors.Is(err, ErrApprovalTokenRequired) {
		t.Fatalf("no token = %v, want ErrApprovalTokenRequired", err)
	}
	if _, err := h.LoadSnapshotWithToken(strings.NewReader(snap), "wrong"); !errors.Is(err, ErrApprovalTokenInvalid) {
		t.Fatalf("wrong token = %v, want ErrApprovalTokenInvalid", err)
	}
	a := *h
	a.Actor = Actor{Type: "agent", ID: "planner"}
	if _, err := a.LoadSnapshotWithToken(strings.NewReader(snap), tok); err != nil {
		t.Fatalf("agent holding the token = %v", err)
	}
}

// "Empty" ignored projects, roles, runners, sessions and plans, so an agent
// could merge check runners or can_approve roles into a store that had those
// but no tasks yet.
func TestSnapshotImportTreatsConfigurationAsData(t *testing.T) {
	src := humanStore(t)
	if _, err := src.AddTask("seed", "", "normal", TaskOpts{}); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := src.SnapshotJSON(&buf); err != nil {
		t.Fatal(err)
	}
	h := humanStore(t)
	if _, err := h.DB.Exec(`INSERT INTO projects (name, path, autonomy_default, created_at) VALUES ('p', NULL, 'hotl', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := asAgent(h).LoadSnapshot(bytes.NewReader(buf.Bytes())); !errors.Is(err, ErrAgentCannotImportSnapshot) {
		t.Fatalf("agent import into a store with a project = %v, want ErrAgentCannotImportSnapshot", err)
	}
	// The roles every store is seeded with do not count.
	fresh := agentStore(t)
	if _, err := fresh.LoadSnapshot(bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("seeding a brand-new store = %v", err)
	}
}
