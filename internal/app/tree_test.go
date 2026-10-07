package app

import (
	"os"
	"path/filepath"
	"testing"

	"acline/internal/store"
)

func TestTaskDirIsTheProjectRootWhereverItIsAskedFrom(t *testing.T) {
	st := openTestStore(t)
	st.Actor = store.Actor{Type: "human", ID: "owner"}
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	pid, err := st.AddProject("demo", root, "")
	if err != nil {
		t.Fatal(err)
	}
	withProject, _ := st.AddTask("in demo", "", "normal", store.TaskOpts{ProjectID: &pid})
	loose, _ := st.AddTask("no project", "", "normal", store.TaskOpts{})
	t.Chdir(sub)

	if got, err := TaskDir(st, withProject); err != nil || store.RealPath(got) != store.RealPath(root) {
		t.Errorf("TaskDir(task in demo) from a subfolder = %q, %v; want the project root %q", got, err, root)
	}
	if got, _ := TaskDir(st, loose); store.RealPath(got) != store.RealPath(sub) {
		t.Errorf("TaskDir(task with no project) = %q, want the working directory %q", got, sub)
	}

	// A registered path that no longer exists falls back to the working directory.
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sub, 0o755); err != nil { // keep a working directory to fall back to
		t.Fatal(err)
	}
	if got, err := TaskDir(st, withProject); err != nil || got == "" {
		t.Errorf("TaskDir with a missing project path = %q, %v", got, err)
	}
}

func TestGateTreeSaysUnavailableWhenTheDirCannotBeHashed(t *testing.T) {
	st := openTestStore(t)
	id, _ := st.AddTask("t", "", "normal", store.TaskOpts{})
	t.Chdir(t.TempDir())
	if got := GateTree(st, id, func(string) string { return "" }); got != store.TreeUnavailable {
		t.Errorf("GateTree with an unhashable dir = %q, want %q", got, store.TreeUnavailable)
	}
	if got := TaskTree(st, id, func(string) string { return "" }); got != "" {
		t.Errorf("TaskTree with an unhashable dir = %q, want \"\"", got)
	}
	if got := GateTree(st, id, func(string) string { return "sha256:x" }); got != "sha256:x" {
		t.Errorf("GateTree = %q", got)
	}
}
