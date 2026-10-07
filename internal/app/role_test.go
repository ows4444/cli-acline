package app

import (
	"errors"
	"testing"

	"acline/internal/store"
)

func TestAssignRoleNamesThePreviousAndNextRole(t *testing.T) {
	st := openTestStore(t)
	st.Actor = store.Actor{Type: "human", ID: "owner"}
	id, _ := st.AddTask("t", "", "normal", store.TaskOpts{})

	res, err := AssignRole(st, AssignRoleRequest{TaskID: id, Role: "designer"})
	if err != nil {
		t.Fatalf("AssignRole: %v", err)
	}
	if res.Role.Name != "designer" || res.Previous != "unassigned" || res.Next == nil {
		t.Fatalf("res = role %q previous %q next %v", res.Role.Name, res.Previous, res.Next)
	}
	res, err = AssignRole(st, AssignRoleRequest{TaskID: id, Role: *res.Next})
	if err != nil {
		t.Fatalf("AssignRole next: %v", err)
	}
	if res.Previous != "designer" {
		t.Errorf("previous = %q, want designer", res.Previous)
	}
	task, _ := st.GetTask(id)
	if task.RoleID.Int64 != res.Role.ID {
		t.Errorf("role_id = %v, want %d", task.RoleID, res.Role.ID)
	}
}

// From another project's folder, the task's own project's role is assigned,
// not the folder project's role of the same name.
func TestAssignRoleUsesTheTasksProjectNotTheCurrentDirectory(t *testing.T) {
	st := openTestStore(t)
	st.Actor = store.Actor{Type: "human", ID: "owner"}
	dirA, dirB := t.TempDir(), t.TempDir()
	a, _ := st.AddProject("a", dirA, "")
	b, _ := st.AddProject("b", dirB, "")
	roleA, err := st.AddRole(a, "reviewer", "both", false, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddRole(b, "reviewer", "both", false, nil, ""); err != nil {
		t.Fatal(err)
	}
	id, _ := st.AddTask("t", "", "normal", store.TaskOpts{ProjectID: &a})
	t.Chdir(dirB)

	res, err := AssignRole(st, AssignRoleRequest{TaskID: id, Role: "reviewer"})
	if err != nil {
		t.Fatalf("AssignRole: %v", err)
	}
	if res.Role.ID != roleA {
		t.Errorf("assigned role #%d, want project a's #%d", res.Role.ID, roleA)
	}
}

func TestAssignRoleRefusesARoleOfAnotherProject(t *testing.T) {
	st := openTestStore(t)
	st.Actor = store.Actor{Type: "human", ID: "owner"}
	a, _ := st.AddProject("a", t.TempDir(), "")
	b, _ := st.AddProject("b", t.TempDir(), "")
	if _, err := st.AddRole(b, "release", "both", false, nil, ""); err != nil {
		t.Fatal(err)
	}
	id, _ := st.AddTask("t", "", "normal", store.TaskOpts{ProjectID: &a})

	_, err := AssignRole(st, AssignRoleRequest{TaskID: id, Role: "release", ProjectArg: "b"})
	if !errors.Is(err, ErrRoleOfAnotherProject) {
		t.Fatalf("err = %v, want ErrRoleOfAnotherProject", err)
	}
	if task, _ := st.GetTask(id); task.RoleID.Valid {
		t.Error("the role was assigned anyway")
	}
}

func TestAssignRoleRefusesAnUnknownRole(t *testing.T) {
	st := openTestStore(t)
	id, _ := st.AddTask("t", "", "normal", store.TaskOpts{})
	if _, err := AssignRole(st, AssignRoleRequest{TaskID: id, Role: "nope"}); err == nil {
		t.Fatal("expected an unknown role to be refused")
	}
}
