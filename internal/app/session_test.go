package app

import (
	"errors"
	"testing"

	"acline/internal/store"
)

// From another project's folder, a session for a task is scoped to the task's
// project, and its role is that project's role of the name.
func TestStartSessionUsesTheTasksProjectAndItsRole(t *testing.T) {
	st := openTestStore(t)
	st.Actor = store.Actor{Type: "human", ID: "owner"}
	dirA, dirB := t.TempDir(), t.TempDir()
	a, _ := st.AddProject("a", dirA, "")
	b, _ := st.AddProject("b", dirB, "")
	roleA, err := st.AddRole(a, "pairer", "both", false, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddRole(b, "pairer", "both", false, nil, ""); err != nil {
		t.Fatal(err)
	}
	task, _ := st.AddTask("t", "", "normal", store.TaskOpts{ProjectID: &a})
	t.Chdir(dirB)

	res, err := StartSession(st, StartSessionRequest{TaskID: &task, RoleArg: "pairer", AllowCwdFallback: true})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if res.ProjectID == nil || *res.ProjectID != a {
		t.Errorf("project = %v, want a (#%d)", res.ProjectID, a)
	}
	t.Chdir(dirA)
	sess, err := st.CurrentSession()
	if err != nil {
		t.Fatal(err)
	}
	if sess.ID != res.ID || sess.ProjectID.Int64 != a || sess.RoleID.Int64 != roleA {
		t.Errorf("session = id %d project %v role %v, want #%d in a as #%d", sess.ID, sess.ProjectID, sess.RoleID, res.ID, roleA)
	}
	if got, _ := st.GetTask(task); got.Status != "in_progress" {
		t.Errorf("task status = %q, want in_progress", got.Status)
	}
}

func TestStartSessionWithoutATaskUsesTheCurrentProject(t *testing.T) {
	st := openTestStore(t)
	st.Actor = store.Actor{Type: "human", ID: "owner"}
	dir := t.TempDir()
	pid, _ := st.AddProject("here", dir, "")
	t.Chdir(dir)

	res, err := StartSession(st, StartSessionRequest{AllowCwdFallback: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.ProjectID == nil || *res.ProjectID != pid {
		t.Errorf("project = %v, want #%d", res.ProjectID, pid)
	}
	if _, err := StartSession(st, StartSessionRequest{AllowCwdFallback: true}); !errors.Is(err, store.ErrSessionActive) {
		t.Errorf("second start: err = %v, want ErrSessionActive", err)
	}
}

func TestStartSessionRecallsTheTasksLessons(t *testing.T) {
	st := openTestStore(t)
	st.Actor = store.Actor{Type: "human", ID: "owner"}
	if _, err := st.AddMemory("store", "pitfall", "migrations lock the table"); err != nil {
		t.Fatal(err)
	}
	task, _ := st.AddTask("t", "", "normal", store.TaskOpts{Area: "store"})

	res, err := StartSession(st, StartSessionRequest{TaskID: &task})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Lessons) != 1 || res.Lessons[0].Body != "migrations lock the table" {
		t.Errorf("lessons = %+v, want the store pitfall", res.Lessons)
	}
}

func TestEndSessionCountsTheReviewQueue(t *testing.T) {
	st := openTestStore(t)
	st.Actor = store.Actor{Type: "agent", ID: "bot"}
	if _, err := st.AddMemory("", "", "agent lesson"); err != nil {
		t.Fatal(err)
	}
	if _, err := StartSession(st, StartSessionRequest{}); err != nil {
		t.Fatal(err)
	}

	res, err := EndSession(st, "done", store.SessionCost{TokensIn: 10}, "")
	if err != nil {
		t.Fatalf("EndSession: %v", err)
	}
	if res.Session == nil || res.PendingMemory != 1 {
		t.Errorf("res = %+v, want the session and 1 pending entry", res)
	}
	if _, err := EndSession(st, "", store.SessionCost{}, ""); !errors.Is(err, store.ErrNoActiveSession) {
		t.Errorf("second end: err = %v, want ErrNoActiveSession", err)
	}
}
