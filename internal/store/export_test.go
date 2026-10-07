package store

import (
	"bytes"
	"encoding/json"
	"testing"
)

// `acline export` is "the full audit trail": every table a snapshot backs up.
func TestExportCoversEverySnapshotTable(t *testing.T) {
	exported := map[string]bool{}
	for _, e := range exportTables {
		exported[e.table] = true
	}
	for _, table := range snapshotTables {
		if !exported[table] {
			t.Errorf("table %q is backed up by snapshots but missing from exportTables", table)
		}
	}
}

func exportKinds(t *testing.T, s *Store, since string) map[string]int {
	t.Helper()
	var buf bytes.Buffer
	if _, err := s.ExportJSONL(&buf, since); err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	dec := json.NewDecoder(&buf)
	for dec.More() {
		var r ExportRecord
		if err := dec.Decode(&r); err != nil {
			t.Fatal(err)
		}
		kinds[r.Kind]++
	}
	return kinds
}

func TestExportIncludesPlansAndFiltersThemBySince(t *testing.T) {
	s := humanStore(t)
	spec := approvedSpec(t, s)
	if _, err := s.ProposePlan(spec, samplePlan()); err != nil {
		t.Fatal(err)
	}
	all := exportKinds(t, s, "")
	for _, kind := range []string{"role", "plan", "plan_item", "plan_edge", "plan_criterion"} {
		if all[kind] == 0 {
			t.Errorf("export has no %s records: %v", kind, all)
		}
	}
	later := exportKinds(t, s, "2999-01-01")
	if len(later) != 0 {
		t.Fatalf("export --since the future = %v, want nothing", later)
	}
}
