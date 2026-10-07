package app

import (
	"errors"
	"strings"
	"testing"

	"acline/internal/store"
)

func TestAddTaskCreatesTheTaskInTheNamedProject(t *testing.T) {
	st := openTestStore(t)
	st.Actor = store.Actor{Type: "human", ID: "owner"}
	pid, err := st.AddProject("demo", t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	parent, _ := st.AddTask("parent", "", "normal", store.TaskOpts{ProjectID: &pid})

	id, err := AddTask(st, AddTaskRequest{
		Title: "child", Description: "d", Priority: "high", Type: "bug", Risk: "medium",
		ParentID: &parent, ProjectArg: "demo",
	})
	if err != nil {
		t.Fatalf("AddTask: %v", err)
	}
	task, err := st.GetTask(id)
	if err != nil {
		t.Fatal(err)
	}
	if task.ProjectID.Int64 != pid || task.ParentID.Int64 != parent || task.Type.String != "bug" ||
		task.Risk != "medium" || task.Priority != "high" {
		t.Errorf("task = project %v parent %v type %q risk %q priority %q",
			task.ProjectID, task.ParentID, task.Type.String, task.Risk, task.Priority)
	}
}

func TestAddTaskRefusesABlankTitle(t *testing.T) {
	st := openTestStore(t)
	for _, title := range []string{"", "   "} {
		if _, err := AddTask(st, AddTaskRequest{Title: title}); !errors.Is(err, ErrTitleRequired) {
			t.Errorf("title %q: err = %v, want ErrTitleRequired", title, err)
		}
	}
}

func TestAddTaskReportsAMissingSpec(t *testing.T) {
	st := openTestStore(t)
	spec := int64(999)
	if _, err := AddTask(st, AddTaskRequest{Title: "t", SpecID: &spec}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestAddTaskLeavesTheTokenErrorUnwrapped(t *testing.T) {
	st := openTestStore(t)
	st.Actor = store.Actor{Type: "human", ID: "owner"}
	token, err := st.EnableApprovalToken()
	if err != nil {
		t.Fatal(err)
	}
	st.Actor = store.Actor{Type: "agent", ID: "bot"}

	if _, err := AddTask(st, AddTaskRequest{Title: "t", Autonomy: "auto"}); !errors.Is(err, store.ErrApprovalTokenRequired) {
		t.Fatalf("err = %v, want ErrApprovalTokenRequired", err)
	}
	if _, err := AddTask(st, AddTaskRequest{Title: "t", Autonomy: "auto", Token: token}); err != nil {
		t.Fatalf("with the token: %v", err)
	}
}

func TestUpdateTaskRefusesAnEmptyUpdate(t *testing.T) {
	st := openTestStore(t)
	id, _ := st.AddTask("t", "", "normal", store.TaskOpts{})
	if _, err := UpdateTask(st, id, store.TaskUpdate{}); !errors.Is(err, ErrNothingToUpdate) {
		t.Fatalf("err = %v, want ErrNothingToUpdate", err)
	}
}

func TestUpdateTaskRefusesAReasonWithoutBlocked(t *testing.T) {
	st := openTestStore(t)
	id, _ := st.AddTask("t", "", "normal", store.TaskOpts{})
	if _, err := UpdateTask(st, id, store.TaskUpdate{Status: "review", Reason: "why"}); !errors.Is(err, ErrReasonNeedsBlocked) {
		t.Fatalf("err = %v, want ErrReasonNeedsBlocked", err)
	}
}

func TestUpdateTaskLeavesTheTokenErrorUnwrapped(t *testing.T) {
	st := openTestStore(t)
	st.Actor = store.Actor{Type: "human", ID: "owner"}
	id, _ := st.AddTask("t", "", "normal", store.TaskOpts{Risk: "high"})
	if _, err := st.EnableApprovalToken(); err != nil {
		t.Fatal(err)
	}
	st.Actor = store.Actor{Type: "agent", ID: "bot"}
	if _, err := UpdateTask(st, id, store.TaskUpdate{Priority: "urgent", Risk: "low"}); !errors.Is(err, store.ErrApprovalTokenRequired) {
		t.Fatalf("err = %v, want ErrApprovalTokenRequired", err)
	}
}

func TestCompleteTaskIsBlockedByTheGate(t *testing.T) {
	st := openTestStore(t)
	st.Actor = store.Actor{Type: "human", ID: "owner"}
	id, _ := st.AddTask("t", "", "normal", store.TaskOpts{})

	_, err := CompleteTask(st, CompleteTaskRequest{TaskID: id, Hash: func(string) string { return "" }})
	var blocked *store.GateBlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("err = %v, want a GateBlockedError", err)
	}
	if task, _ := st.GetTask(id); task.Status == "done" {
		t.Error("a blocked task was marked done")
	}
}

func TestCompleteTaskForcedByAPersonIsRecorded(t *testing.T) {
	st := openTestStore(t)
	st.Actor = store.Actor{Type: "human", ID: "owner"}
	id, _ := st.AddTask("t", "", "normal", store.TaskOpts{})

	res, err := CompleteTask(st, CompleteTaskRequest{TaskID: id, Force: true, Hash: func(string) string { return "" }})
	if err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}
	if !res.Overridden {
		t.Error("Overridden = false, want the override reported")
	}
	if task, _ := st.GetTask(id); task.Status != "done" {
		t.Errorf("status = %q, want done", task.Status)
	}
}

func TestCompleteTaskRefusesAnAgentsForce(t *testing.T) {
	st := openTestStore(t)
	st.Actor = store.Actor{Type: "agent", ID: "bot"}
	id, _ := st.AddTask("t", "", "normal", store.TaskOpts{})

	_, err := CompleteTask(st, CompleteTaskRequest{TaskID: id, Force: true, Hash: func(string) string { return "" }})
	if !errors.Is(err, store.ErrAgentCannotOverrideGate) {
		t.Fatalf("err = %v, want ErrAgentCannotOverrideGate", err)
	}
}

// The gate sees the task's tree: a runner pass for an older tree is stale.
func TestCompleteTaskChecksTheCurrentTree(t *testing.T) {
	st := openTestStore(t)
	st.Actor = store.Actor{Type: "human", ID: "owner"}
	pid, err := st.AddProject("demo", t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	id, _ := st.AddTask("t", "", "normal", store.TaskOpts{ProjectID: &pid, Risk: "high"})
	if _, err := st.AddCheckWithMeta(id, nil, "test", "pass", "ok", "",
		store.CheckMeta{Source: store.CheckSourceRunner, TreeHash: "sha256:old"}); err != nil {
		t.Fatal(err)
	}
	_, err = CompleteTask(st, CompleteTaskRequest{TaskID: id, Hash: func(string) string { return "sha256:new" }})
	var blocked *store.GateBlockedError
	if !errors.As(err, &blocked) || !strings.Contains(strings.Join(blocked.Blockers, " "), "test") {
		t.Fatalf("err = %v, want the stale test pass to block", err)
	}
}

func TestCompleteTaskReportsAMissingTask(t *testing.T) {
	st := openTestStore(t)
	_, err := CompleteTask(st, CompleteTaskRequest{TaskID: 99, Hash: func(string) string { return "" }})
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
