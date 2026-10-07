package app

import (
	"strings"
	"testing"

	"acline/internal/store"
)

func TestPlanDiffComparesWithTheSpecsPreviousPlan(t *testing.T) {
	st := openTestStore(t)
	st.Actor = store.Actor{Type: "human", ID: "ana"}
	spec, _ := st.AddSpec("payments", "body")
	if err := st.ApproveSpec(spec, ""); err != nil {
		t.Fatal(err)
	}
	v1, err := st.ProposePlan(spec, store.PlanInput{Items: []store.PlanItemInput{
		{Ref: "a", Title: "schema"}, {Ref: "b", Title: "api"}, {Ref: "c", Title: "ui"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, first, err := PlanDiff(st, v1); err != nil || len(first) != 3 || first[0].Change != "added" {
		t.Fatalf("a first plan is all added: %+v, %v", first, err)
	}
	v2, err := st.RevisePlan(v1, store.PlanInput{Items: []store.PlanItemInput{
		{Ref: "a", Title: "schema"}, {Ref: "b", Title: "api v2", Risk: "high"}, {Ref: "d", Title: "docs"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	prev, changes, err := PlanDiff(st, v2)
	if err != nil {
		t.Fatal(err)
	}
	if prev == nil || prev.ID != v1 {
		t.Fatalf("previous = %+v, want plan #%d", prev, v1)
	}
	got := map[string]string{}
	for _, c := range changes {
		got[c.Ref] = c.Change + " " + strings.Join(c.Fields, ",")
	}
	want := map[string]string{"a": "unchanged ", "b": "changed title,risk", "d": "added ", "c": "removed "}
	for ref, w := range want {
		if got[ref] != w {
			t.Errorf("item %s: %q, want %q (all: %v)", ref, got[ref], w, got)
		}
	}
}
