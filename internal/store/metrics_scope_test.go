package store

import "testing"

func TestMetricsAndPlansCanBeScopedToAProject(t *testing.T) {
	s := humanStore(t)
	a, err := s.AddProject("a", "", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.AddProject("b", "", "")
	if err != nil {
		t.Fatal(err)
	}
	inA, _ := s.AddTask("in a", "", "normal", TaskOpts{ProjectID: &a})
	inB, _ := s.AddTask("in b", "", "normal", TaskOpts{ProjectID: &b})
	for _, id := range []int64{inA, inB, inB} {
		if _, err := s.AddCheck(id, "test", "fail", ""); err != nil {
			t.Fatal(err)
		}
	}

	all, err := s.ComputeMetricsFor(nil)
	if err != nil {
		t.Fatal(err)
	}
	onlyA, err := s.ComputeMetricsFor(&a)
	if err != nil {
		t.Fatal(err)
	}
	if all.TasksTotal != 2 || all.ChecksFailed != 3 {
		t.Fatalf("unscoped: tasks=%d failed=%d", all.TasksTotal, all.ChecksFailed)
	}
	if onlyA.TasksTotal != 1 || onlyA.ChecksTotal != 1 || onlyA.ChecksFailed != 1 {
		t.Fatalf("project a: tasks=%d checks=%d failed=%d", onlyA.TasksTotal, onlyA.ChecksTotal, onlyA.ChecksFailed)
	}
	if onlyA.EventsByActor["human"] == 0 || onlyA.EventsByActor["human"] >= all.EventsByActor["human"] {
		t.Fatalf("project a events %v should be a strict subset of %v", onlyA.EventsByActor, all.EventsByActor)
	}

	specA, _ := s.AddSpec("a spec", "body", SpecOpts{ProjectID: &a})
	specB, _ := s.AddSpec("b spec", "body", SpecOpts{ProjectID: &b})
	for _, id := range []int64{specA, specB} {
		if err := s.setSpecStatus(id, "approved"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ProposePlan(id, samplePlan()); err != nil {
			t.Fatal(err)
		}
	}
	plans, err := s.ListProjectPlans(nil, "", &a)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 || plans[0].SpecID != specA {
		t.Fatalf("project a plans = %+v", plans)
	}
	if all, _ := s.ListPlans(nil, ""); len(all) != 2 {
		t.Fatalf("unscoped plans = %d, want 2", len(all))
	}
}
