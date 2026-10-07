package app

import (
	"errors"
	"testing"

	"acline/internal/store"
)

func TestSplitPackage(t *testing.T) {
	for spec, want := range map[string][2]string{
		"react":               {"react", ""},
		"react@18.2.0":        {"react", "18.2.0"},
		"@scope/pkg":          {"@scope/pkg", ""},
		"@scope/pkg@1.0.0":    {"@scope/pkg", "1.0.0"},
		"golang.org/x/mod@v1": {"golang.org/x/mod", "v1"},
	} {
		if name, version := SplitPackage(spec); name != want[0] || version != want[1] {
			t.Errorf("SplitPackage(%q) = %q, %q; want %q, %q", spec, name, version, want[0], want[1])
		}
	}
}

func TestAddDependencySplitsTheVersionAndTakesTheTasksProject(t *testing.T) {
	st := openTestStore(t)
	st.Actor = store.Actor{Type: "human", ID: "owner"}
	dirA, dirB := t.TempDir(), t.TempDir()
	a, _ := st.AddProject("a", dirA, "")
	if _, err := st.AddProject("b", dirB, ""); err != nil {
		t.Fatal(err)
	}
	task, _ := st.AddTask("t", "", "normal", store.TaskOpts{ProjectID: &a})
	t.Chdir(dirB)

	id, name, version, err := AddDependency(st, AddDependencyRequest{
		Ecosystem: "npm", Name: "@scope/pkg@1.0.0", TaskID: &task, AllowCwdFallback: true,
	})
	if err != nil {
		t.Fatalf("AddDependency: %v", err)
	}
	if name != "@scope/pkg" || version != "1.0.0" {
		t.Errorf("recorded %q@%q", name, version)
	}
	deps, _ := st.ListDependencies(nil, false)
	if len(deps) != 1 || deps[0].ID != id {
		t.Fatalf("deps = %+v", deps)
	}
	if d := deps[0]; d.ProjectID.Int64 != a || d.Name != "@scope/pkg" || d.Version.String != "1.0.0" || d.Verified {
		t.Errorf("dep = project %v name %q version %q verified %v", d.ProjectID, d.Name, d.Version.String, d.Verified)
	}
}

func TestAddDependencyKeepsAnExplicitVersion(t *testing.T) {
	st := openTestStore(t)
	_, name, version, err := AddDependency(st, AddDependencyRequest{Ecosystem: "npm", Name: "pkg", Version: "2.0.0"})
	if err != nil || name != "pkg" || version != "2.0.0" {
		t.Fatalf("got %q@%q, %v", name, version, err)
	}
}

func TestAddDependencyRefusesAMissingEcosystemOrName(t *testing.T) {
	st := openTestStore(t)
	for _, req := range []AddDependencyRequest{{Name: "pkg"}, {Ecosystem: "npm"}, {Ecosystem: "npm", Name: "  "}} {
		if _, _, _, err := AddDependency(st, req); !errors.Is(err, ErrDependencyIncomplete) {
			t.Errorf("%+v: err = %v, want ErrDependencyIncomplete", req, err)
		}
	}
}

func TestAddDependencyRefusesAMissingTask(t *testing.T) {
	st := openTestStore(t)
	missing := int64(99)
	if _, _, _, err := AddDependency(st, AddDependencyRequest{Ecosystem: "npm", Name: "pkg", TaskID: &missing}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestVerifyDependencyNeedsAPerson(t *testing.T) {
	st := openTestStore(t)
	st.Actor = store.Actor{Type: "agent", ID: "bot"}
	id, _, _, err := AddDependency(st, AddDependencyRequest{Ecosystem: "npm", Name: "pkg"})
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyDependency(st, id, ""); !errors.Is(err, store.ErrAgentCannotVerifyDependency) {
		t.Fatalf("agent: err = %v, want ErrAgentCannotVerifyDependency", err)
	}
	st.Actor = store.Actor{Type: "human", ID: "owner"}
	if err := VerifyDependency(st, id, ""); err != nil {
		t.Fatalf("person: %v", err)
	}
}
