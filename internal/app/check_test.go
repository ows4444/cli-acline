package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"acline/internal/store"
)

func TestRecordCheckIsManualAndBoundToTheTasksTree(t *testing.T) {
	st := openTestStore(t)
	st.Actor = store.Actor{Type: "human", ID: "owner"}
	pid, err := st.AddProject("demo", t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	id, _ := st.AddTask("t", "", "normal", store.TaskOpts{ProjectID: &pid})

	cid, err := RecordCheck(st, RecordCheckRequest{
		TaskID: id, Kind: "lint", Status: "pass", Detail: "clean",
		Hash: func(string) string { return "sha256:tree" },
	})
	if err != nil {
		t.Fatalf("RecordCheck: %v", err)
	}
	checks, _ := st.ListChecks(id)
	if len(checks) != 1 || checks[0].ID != cid {
		t.Fatalf("checks = %+v, want one with id %d", checks, cid)
	}
	c := checks[0]
	if c.Source != store.CheckSourceManual || c.TreeHash.String != "sha256:tree" || c.Detail.String != "clean" {
		t.Errorf("check = source %q tree %q detail %q", c.Source, c.TreeHash.String, c.Detail.String)
	}
}

func TestRecordCheckRefusesAMissingTask(t *testing.T) {
	st := openTestStore(t)
	if _, err := RecordCheck(st, RecordCheckRequest{
		TaskID: 999, Kind: "lint", Status: "pass", Hash: func(string) string { return "" },
	}); err == nil {
		t.Fatal("expected an error for a task that does not exist")
	}
}

func TestRecordCheckLeavesTheAgentHumanReviewErrorUnwrapped(t *testing.T) {
	st := openTestStore(t)
	st.Actor = store.Actor{Type: "agent", ID: "bot"}
	id, _ := st.AddTask("t", "", "normal", store.TaskOpts{})
	t.Chdir(t.TempDir())

	_, err := RecordCheck(st, RecordCheckRequest{
		TaskID: id, Kind: "human_review", Status: "pass", Hash: func(string) string { return "" },
	})
	if !errors.Is(err, store.ErrAgentCannotRecordHumanReview) {
		t.Fatalf("err = %v, want ErrAgentCannotRecordHumanReview", err)
	}
}

func TestRunCheckRunsInTheProjectRootAndRecordsRunnerEvidence(t *testing.T) {
	st := openTestStore(t)
	st.Actor = store.Actor{Type: "human", ID: "owner"}
	root := t.TempDir()
	pid, err := st.AddProject("demo", root, "")
	if err != nil {
		t.Fatal(err)
	}
	id, _ := st.AddTask("t", "", "normal", store.TaskOpts{ProjectID: &pid})
	t.Chdir(t.TempDir()) // somewhere else: the tool must still run in root

	var hashed string
	cid, res, err := RunCheck(context.Background(), st, RunCheckRequest{
		TaskID: id, Kind: "lint", Command: "touch ran-here",
		Hash: func(dir string) string { hashed = dir; return "sha256:tree" },
	})
	if err != nil {
		t.Fatalf("RunCheck: %v", err)
	}
	if res.Status != "pass" {
		t.Fatalf("status = %q (%s), want pass", res.Status, res.Detail)
	}
	if _, err := os.Stat(filepath.Join(root, "ran-here")); err != nil {
		t.Errorf("the command did not run in the project root: %v", err)
	}
	if store.RealPath(hashed) != store.RealPath(root) {
		t.Errorf("hashed %q, want the project root %q", hashed, root)
	}
	checks, _ := st.ListChecks(id)
	if len(checks) != 1 || checks[0].ID != cid || checks[0].Source != store.CheckSourceRunner || checks[0].TreeHash.String != "sha256:tree" {
		t.Fatalf("checks = %+v", checks)
	}
}

func TestRunCheckRefusesAnAgentsCommandBeforeRunningIt(t *testing.T) {
	st := openTestStore(t)
	st.Actor = store.Actor{Type: "agent", ID: "bot"}
	id, _ := st.AddTask("t", "", "normal", store.TaskOpts{})
	dir := t.TempDir()
	t.Chdir(dir)

	_, _, err := RunCheck(context.Background(), st, RunCheckRequest{
		TaskID: id, Kind: "lint", Command: "touch ran-here", Hash: func(string) string { return "" },
	})
	if !errors.Is(err, store.ErrAgentCannotChooseCheckCommand) {
		t.Fatalf("err = %v, want ErrAgentCannotChooseCheckCommand", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ran-here")); !os.IsNotExist(err) {
		t.Error("a refused agent's command ran")
	}
	if checks, _ := st.ListChecks(id); len(checks) != 0 {
		t.Errorf("a refused run recorded %d check(s)", len(checks))
	}
}

func TestRunCheckSaysWhyAResultIsNotBoundToATree(t *testing.T) {
	st := openTestStore(t)
	st.Actor = store.Actor{Type: "human", ID: "owner"}
	id, _ := st.AddTask("t", "", "normal", store.TaskOpts{})
	t.Chdir(t.TempDir())

	if _, _, err := RunCheck(context.Background(), st, RunCheckRequest{
		TaskID: id, Kind: "lint", Command: "true", Hash: func(string) string { return "" },
	}); err != nil {
		t.Fatal(err)
	}
	checks, _ := st.ListChecks(id)
	if len(checks) != 1 || !strings.HasSuffix(checks[0].Detail.String, store.UnboundTreeNote) {
		t.Fatalf("detail = %q, want it to end with the unbound-tree note", checks[0].Detail.String)
	}
}
