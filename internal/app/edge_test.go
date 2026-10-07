package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"acline/internal/store"
)

func noTree(string) string { return "" }

func eventCount(t *testing.T, st *store.Store) (n int) {
	t.Helper()
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// scoped is every use case that takes a project or a role by name: fn calls
// it with the given names on task #1 (which exists).
var scoped = []struct {
	name     string
	hasRole  bool
	fn       func(st *store.Store, project, role string) error
	fallback bool // an empty project is allowed to mean "none"
}{
	{"AddSpec", false, func(st *store.Store, p, _ string) error {
		_, err := AddSpec(st, AddSpecRequest{Title: "s", Body: "b", ProjectArg: p})
		return err
	}, true},
	{"AddDecision", false, func(st *store.Store, p, _ string) error {
		_, err := AddDecision(st, AddDecisionRequest{Title: "d", ProjectArg: p})
		return err
	}, true},
	{"AddMemory", false, func(st *store.Store, p, _ string) error {
		_, err := AddMemory(st, AddMemoryRequest{Area: "cli", Kind: "lesson", Body: "b", ProjectArg: p})
		return err
	}, true},
	{"AddNote", true, func(st *store.Store, p, r string) error {
		_, err := AddNote(st, AddNoteRequest{Body: "b", ProjectArg: p, RoleArg: r})
		return err
	}, true},
	{"Log", true, func(st *store.Store, p, r string) error {
		_, err := Log(st, LogRequest{Type: "note", Message: "m", ProjectArg: p, RoleArg: r})
		return err
	}, true},
	{"AddTask", true, func(st *store.Store, p, r string) error {
		_, err := AddTask(st, AddTaskRequest{Title: "t", ProjectArg: p, RoleArg: r})
		return err
	}, true},
	{"StartSession", true, func(st *store.Store, p, r string) error {
		_, err := StartSession(st, StartSessionRequest{ProjectArg: p, RoleArg: r})
		return err
	}, true},
	{"AddRole", false, func(st *store.Store, p, _ string) error {
		_, err := AddRole(st, AddRoleRequest{Name: "r", Kind: "agent", ProjectArg: p})
		return err
	}, false},
	{"AddFeature", false, func(st *store.Store, p, _ string) error {
		_, err := AddFeature(st, AddFeatureRequest{Name: "f", ProjectArg: p})
		return err
	}, true},
	{"RecordEval", false, func(st *store.Store, p, _ string) error {
		_, err := RecordEval(st, RecordEvalRequest{Suite: "s", PassRate: 1, SampleSize: 10, ProjectArg: p})
		return err
	}, true},
	{"AddDependency", false, func(st *store.Store, p, _ string) error {
		_, _, _, err := AddDependency(st, AddDependencyRequest{Ecosystem: "go", Name: "x@1", ProjectArg: p})
		return err
	}, true},
	{"RecordCheck", true, func(st *store.Store, p, r string) error {
		_, err := RecordCheck(st, RecordCheckRequest{TaskID: 1, Kind: "test", Status: "fail", ProjectArg: p, RoleArg: r, Hash: noTree})
		return err
	}, true},
	{"RunCheck", true, func(st *store.Store, p, r string) error {
		_, _, err := RunCheck(context.Background(), st, RunCheckRequest{TaskID: 1, Kind: "lint", Command: "true", ProjectArg: p, RoleArg: r, Hash: noTree})
		return err
	}, true},
	{"Approve", true, func(st *store.Store, p, r string) error {
		_, err := Approve(st, ApproveRequest{TaskID: 1, ProjectArg: p, RoleArg: r})
		return err
	}, true},
	{"Reject", true, func(st *store.Store, p, r string) error {
		_, err := Reject(st, RejectRequest{TaskID: 1, ProjectArg: p, RoleArg: r})
		return err
	}, true},
}

func scopedStore(t *testing.T) *store.Store {
	t.Helper()
	st := humanTestStore(t)
	if _, err := st.AddTask("the task", "", "normal", store.TaskOpts{}); err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir()) // no ambient project
	return st
}

// A project name that is not registered is an error everywhere, never a
// silent fall back to "no project", and nothing is written.
func TestEveryUseCaseRefusesAnUnknownProject(t *testing.T) {
	for _, c := range scoped {
		t.Run(c.name, func(t *testing.T) {
			st := scopedStore(t)
			before := eventCount(t, st)
			err := c.fn(st, "no-such-project", "")
			if err == nil {
				t.Fatal("an unregistered project name was accepted")
			}
			if !strings.Contains(err.Error(), "no-such-project") {
				t.Errorf("the error does not name the project: %v", err)
			}
			if n := eventCount(t, st); n != before {
				t.Errorf("%d event(s) written by a refused call", n-before)
			}
		})
	}
}

func TestEveryUseCaseRefusesAnUnknownRole(t *testing.T) {
	for _, c := range scoped {
		if !c.hasRole {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			st := scopedStore(t)
			before := eventCount(t, st)
			err := c.fn(st, "", "no-such-role")
			if err == nil {
				t.Fatal("an unknown role was accepted")
			}
			if !strings.Contains(err.Error(), "no-such-role") {
				t.Errorf("the error does not name the role: %v", err)
			}
			if n := eventCount(t, st); n != before {
				t.Errorf("%d event(s) written by a refused call", n-before)
			}
		})
	}
}

// A store that cannot be read is an error from every use case, not a zero
// value that looks like "nothing there".
func TestEveryUseCaseReportsAStoreItCannotRead(t *testing.T) {
	calls := map[string]func(st *store.Store) error{
		"Dashboard": func(st *store.Store) error { _, err := Dashboard(st, nil); return err },
		"Queue":     func(st *store.Store) error { _, err := Queue(st, nil, noTree); return err },
		"PlanDiff":  func(st *store.Store) error { _, _, err := PlanDiff(st, 1); return err },
		"ReviseSpec": func(st *store.Store) error {
			_, err := ReviseSpec(st, 1, "body")
			return err
		},
		"UpdateTask": func(st *store.Store) error {
			_, err := UpdateTask(st, 1, store.TaskUpdate{Status: "in_progress"})
			return err
		},
		"CompleteTask": func(st *store.Store) error {
			_, err := CompleteTask(st, CompleteTaskRequest{TaskID: 1, Hash: noTree})
			return err
		},
		"EndSession": func(st *store.Store) error {
			_, err := EndSession(st, "", store.SessionCost{}, "")
			return err
		},
		"AssignRole": func(st *store.Store) error {
			_, err := AssignRole(st, AssignRoleRequest{TaskID: 1, Role: "dev"})
			return err
		},
		"VerifyDependency": func(st *store.Store) error { return VerifyDependency(st, 1, "") },
		"TaskDir":          func(st *store.Store) error { _, err := TaskDir(st, 1); return err },
	}
	for _, c := range scoped {
		calls[c.name] = func(st *store.Store) error { return c.fn(st, "", "") }
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			st := scopedStore(t)
			st.Close()
			if err := call(st); err == nil {
				t.Fatal("no error from a closed store")
			}
		})
	}
}

// Dashboard and Queue read many tables; a failure in any one must surface,
// not leave that section looking empty.
func TestDashboardAndQueueReportAFailedReadOfAnyTable(t *testing.T) {
	for _, table := range []string{"tasks", "specs", "decisions", "memory", "plans", "sessions", "dependencies", "meta", "checks", "approvals"} {
		t.Run(table, func(t *testing.T) {
			st := scopedStore(t)
			// High risk, with a pass: the gate reads its checks and approvals.
			if _, err := st.AddTask("risky", "", "normal", store.TaskOpts{Risk: "high"}); err != nil {
				t.Fatal(err)
			}
			if _, err := st.AddCheckWithMeta(2, nil, "test", "pass", "", "", store.CheckMeta{Source: store.CheckSourceRunner, TreeHash: "sha256:t"}); err != nil {
				t.Fatal(err)
			}
			if _, err := st.DB.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
				t.Fatal(err)
			}
			if _, err := st.DB.Exec(`DROP TABLE "` + table + `"`); err != nil {
				t.Skipf("cannot drop %s: %v", table, err)
			}
			_, dErr := Dashboard(st, nil)
			_, qErr := Queue(st, nil, func(string) string { return "sha256:t" })
			if dErr == nil && qErr == nil {
				t.Fatalf("neither Dashboard nor Queue noticed %s is unreadable", table)
			}
			t.Logf("dashboard: %v; queue: %v", dErr != nil, qErr != nil)
		})
	}
}

func TestApproveRecordsTheTreeItWasGivenFor(t *testing.T) {
	st := scopedStore(t)
	id, err := Approve(st, ApproveRequest{TaskID: 1, Kind: "code_review", Note: "read it", Tree: "sha256:now"})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := st.ListApprovals(1)
	if len(a) != 1 || a[0].ID != id || a[0].Decision != "approved" || a[0].TreeHash.String != "sha256:now" || a[0].Note.String != "read it" {
		t.Fatalf("approvals = %+v", a)
	}
}

// Each adapter words this refusal itself, so it must arrive unwrapped.
func TestApproveLeavesTheAgentErrorUnwrapped(t *testing.T) {
	st := scopedStore(t)
	st.Actor = store.Actor{Type: "agent", ID: "claude-code"}
	if _, err := Approve(st, ApproveRequest{TaskID: 1}); !errors.Is(err, store.ErrAgentCannotApprove) {
		t.Fatalf("err = %v, want ErrAgentCannotApprove", err)
	}
	if a, _ := st.ListApprovals(1); len(a) != 0 {
		t.Fatalf("an agent's approval was recorded: %+v", a)
	}
}

func TestRejectIsACodeReviewRejectionAndNeedsNoToken(t *testing.T) {
	st := scopedStore(t)
	if _, err := st.EnableApprovalToken(); err != nil {
		t.Fatal(err)
	}
	if _, err := Reject(st, RejectRequest{TaskID: 1, Note: "loops on Safari"}); err != nil {
		t.Fatalf("a rejection asked for more than it needs: %v", err)
	}
	a, _ := st.ListApprovals(1)
	if len(a) != 1 || a[0].Decision != "rejected" || a[0].Kind != "code_review" || a[0].Note.String != "loops on Safari" {
		t.Fatalf("approvals = %+v", a)
	}
}

func TestApproveAndRejectReportAMissingTask(t *testing.T) {
	st := scopedStore(t)
	if _, err := Approve(st, ApproveRequest{TaskID: 99}); err == nil {
		t.Error("approved a task that does not exist")
	}
	if _, err := Reject(st, RejectRequest{TaskID: 99}); err == nil {
		t.Error("rejected a task that does not exist")
	}
}

// The directory a gate is about comes from the store. If the store cannot be
// read, the answer is "could not check", not the caller's working directory.
func TestTaskDirDoesNotFallBackToTheWorkingDirectoryWhenTheStoreFails(t *testing.T) {
	st := humanTestStore(t)
	pid, err := st.AddProject("demo", t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	id, _ := st.AddTask("t", "", "normal", store.TaskOpts{ProjectID: &pid})
	t.Chdir(t.TempDir())
	if _, err := st.DB.Exec(`PRAGMA foreign_keys = OFF; DROP TABLE projects`); err != nil {
		t.Fatal(err)
	}
	if dir, err := TaskDir(st, id); err == nil {
		t.Fatalf("TaskDir = %q with the project list unreadable, want an error", dir)
	}
	hashed := ""
	hash := func(dir string) string { hashed = dir; return "sha256:cwd" }
	if got := GateTree(st, id, hash); got != store.TreeUnavailable {
		t.Errorf("GateTree = %q, want %q", got, store.TreeUnavailable)
	}
	if got := TaskTree(st, id, hash); got != "" {
		t.Errorf("TaskTree = %q, want unknown", got)
	}
	if hashed != "" {
		t.Errorf("the working directory %q was fingerprinted as the task's tree", hashed)
	}
	if ProjectDir(st, id) != "" {
		t.Error("ProjectDir named a directory it could not look up")
	}
}
