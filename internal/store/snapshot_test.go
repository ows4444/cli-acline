package store

import (
	"bytes"
	"testing"
)

// TestSnapshotRoundTripCoversAllTables is a regression test:
// TestSnapshotRoundTrip (store_test.go) only ever exercised 5
// of the 16 tables snapshotTables lists (tasks, specs, events, checks,
// approvals) — a table added to snapshotTables without an accompanying
// export/import bug would have gone uncaught. This puts exactly one row in
// every table snapshotTables names, exports, imports into a fresh store, and
// confirms each one survives with its row count and a distinguishing field
// intact.
func TestSnapshotRoundTripCoversAllTables(t *testing.T) {
	src := humanStore(t)

	projectID, err := src.AddProject("demo", "/tmp/demo", "hotl")
	if err != nil {
		t.Fatal(err)
	}
	pID := &projectID

	// A project-specific role (the 7 global ones are seeded by migrations in
	// every store, so only this one needs to travel in the snapshot).
	if _, err := src.AddRole(projectID, "auditor", "human", true, nil, "project-specific approver"); err != nil {
		t.Fatal(err)
	}

	specID, err := src.AddSpec("spec title", "spec body")
	if err != nil {
		t.Fatal(err)
	}

	decisionID, err := src.AddDecision("use postgres", DecisionOpts{ProjectID: pID})
	if err != nil {
		t.Fatal(err)
	}

	milestoneID, err := src.AddMilestone("v1 launch", MilestoneOpts{ProjectID: pID})
	if err != nil {
		t.Fatal(err)
	}

	taskID, err := src.AddTask("snapshot task", "", "normal", TaskOpts{SpecID: &specID, ProjectID: pID})
	if err != nil {
		t.Fatal(err)
	}

	sessID, err := src.StartSession(&taskID, pID, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := src.EndSession("session done", SessionCost{}); err != nil {
		t.Fatal(err)
	}

	if _, err := src.LogEvent(&taskID, nil, "note", "distinguishing event message"); err != nil {
		t.Fatal(err)
	}

	if _, err := src.AddApproval(taskID, "code_review", "reviewer", "approved", ""); err != nil {
		t.Fatal(err)
	}

	if _, err := src.AddCheck(taskID, "test", "pass", "distinguishing check detail"); err != nil {
		t.Fatal(err)
	}

	depID, err := src.AddDependency(&taskID, pID, "npm", "left-pad", "1.3.0", false)
	if err != nil {
		t.Fatal(err)
	}

	evalID, err := src.AddEval(&taskID, pID, "refactor-suite", 0.9, 50, "")
	if err != nil {
		t.Fatal(err)
	}

	memID, err := src.AddMemory("cli", "lesson", "distinguishing memory body", MemoryOpts{ProjectID: pID})
	if err != nil {
		t.Fatal(err)
	}

	featID, err := src.AddFeature("dashboard", "live", FeatureOpts{ProjectID: pID})
	if err != nil {
		t.Fatal(err)
	}

	critID, _, err := src.AddCriterion(taskID, "The system shall do the thing")
	if err != nil {
		t.Fatal(err)
	}

	relatedTaskID, err := src.AddTask("related task", "", "normal", TaskOpts{})
	if err != nil {
		t.Fatal(err)
	}
	linkID, err := src.AddLink(taskID, relatedTaskID, "blocks")
	if err != nil {
		t.Fatal(err)
	}

	noteID, err := src.AddNote(pID, "distinguishing note body", "manual")
	if err != nil {
		t.Fatal(err)
	}

	// Revising the spec keeps its earlier text in spec_versions and logs one
	// spec_revised event.
	if _, err := src.ReviseSpec(specID, "revised spec body"); err != nil {
		t.Fatal(err)
	}

	// A plan (needs an approved spec); proposing it logs one event, and setting
	// the runner below logs another (events: 4 -> 6 below, plus the seal watermark).
	if err := src.setSpecStatus(specID, "approved"); err != nil {
		t.Fatal(err)
	}
	if _, err := src.ProposePlan(specID, PlanInput{Items: []PlanItemInput{
		{Ref: "A", Title: "first", Criteria: []string{"When X, the system shall Y"}},
		{Ref: "B", Title: "second", DependsOn: []string{"A"}},
	}}); err != nil {
		t.Fatal(err)
	}
	// Setting a runner also logs one global audit event.
	if err := src.SetCheckRunner(projectID, "sast", "semgrep --error .", ""); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := src.SnapshotJSON(&buf); err != nil {
		t.Fatal(err)
	}

	// Every event the source wrote (the audit trail grows as more changes are
	// recorded) must come across.
	var srcEvents int
	if err := src.DB.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&srcEvents); err != nil {
		t.Fatal(err)
	}

	dst := humanStore(t)
	counts, err := dst.LoadSnapshot(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}

	wantCounts := map[string]int{
		"projects": 1, "roles": 1, "specs": 1, "decisions": 1, "milestones": 1,
		"tasks": 2, "sessions": 1, "events": srcEvents, "approvals": 1, "checks": 1,
		"dependencies": 1, "evals": 1, "memory": 1, "features": 1,
		"task_criteria": 1, "task_links": 1, "notes": 1, "check_runners": 1,
		"plans": 1, "plan_items": 2, "plan_edges": 1, "plan_criteria": 1, "spec_versions": 1,
	}
	for table, want := range wantCounts {
		if counts[table] != want {
			t.Errorf("table %q: imported %d row(s), want %d (full counts: %+v)", table, counts[table], want, counts)
		}
	}
	for _, table := range snapshotTables {
		if _, ok := wantCounts[table]; !ok {
			t.Errorf("snapshotTables lists %q, but this test has no coverage for it — add a row and an expected count", table)
		}
	}

	if items, _ := dst.PlanItems(1); len(items) != 2 || len(items[1].DependsOn) != 1 || len(items[0].Criteria) != 1 {
		t.Errorf("plan not round-tripped: %+v", items)
	}
	if got, _ := dst.CheckRunnerCommand(&projectID, "sast"); got != "semgrep --error ." {
		t.Errorf("check runner not round-tripped: %q", got)
	}
	if p, err := dst.GetProjectByName("demo"); err != nil || p.Path.String != "/tmp/demo" {
		t.Errorf("project not round-tripped: %v, err=%v", p, err)
	}
	if sp, err := dst.GetSpec(specID); err != nil || sp.Title != "spec title" {
		t.Errorf("spec not round-tripped: %v, err=%v", sp, err)
	}
	if d, err := dst.GetDecision(decisionID); err != nil || d.Title != "use postgres" {
		t.Errorf("decision not round-tripped: %v, err=%v", d, err)
	}
	if m, err := dst.GetMilestone(milestoneID); err != nil || m.Name != "v1 launch" {
		t.Errorf("milestone not round-tripped: %v, err=%v", m, err)
	}
	if tk, err := dst.GetTask(taskID); err != nil || tk.Title != "snapshot task" {
		t.Errorf("task not round-tripped: %v, err=%v", tk, err)
	}
	sessions, err := dst.ListSessions(nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	foundSession := false
	for _, s := range sessions {
		if s.ID == sessID {
			foundSession = true
			if s.Summary.String != "session done" {
				t.Errorf("session summary not round-tripped: %+v", s)
			}
		}
	}
	if !foundSession {
		t.Errorf("session #%d not found after import", sessID)
	}
	events, err := dst.ListEvents(&taskID, 10)
	if err != nil {
		t.Fatal(err)
	}
	foundEvent := false
	for _, e := range events {
		if e.Message == "distinguishing event message" {
			foundEvent = true
		}
	}
	if !foundEvent {
		t.Error("event message not round-tripped")
	}
	checks, err := dst.ListChecks(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(checks) != 1 || checks[0].Detail.String != "distinguishing check detail" {
		t.Errorf("check not round-tripped: %+v", checks)
	}
	deps, err := dst.ListDependencies(pID, false)
	if err != nil {
		t.Fatal(err)
	}
	foundDep := false
	for _, d := range deps {
		if d.ID == depID && d.Name == "left-pad" {
			foundDep = true
		}
	}
	if !foundDep {
		t.Error("dependency not round-tripped")
	}
	evals, err := dst.ListEvals(pID, "refactor-suite", 10)
	if err != nil {
		t.Fatal(err)
	}
	foundEval := false
	for _, e := range evals {
		if e.ID == evalID {
			foundEval = true
		}
	}
	if !foundEval {
		t.Error("eval not round-tripped")
	}
	mem, err := dst.ListMemory(MemoryFilter{ProjectID: pID})
	if err != nil {
		t.Fatal(err)
	}
	foundMem := false
	for _, m := range mem {
		if m.ID == memID && m.Body == "distinguishing memory body" {
			foundMem = true
		}
	}
	if !foundMem {
		t.Error("memory not round-tripped")
	}
	feats, err := dst.ListFeatures("", pID)
	if err != nil {
		t.Fatal(err)
	}
	foundFeat := false
	for _, f := range feats {
		if f.ID == featID && f.Name == "dashboard" {
			foundFeat = true
		}
	}
	if !foundFeat {
		t.Error("feature not round-tripped")
	}
	criteria, err := dst.ListCriteria(taskID)
	if err != nil {
		t.Fatal(err)
	}
	foundCrit := false
	for _, c := range criteria {
		if c.ID == critID {
			foundCrit = true
		}
	}
	if !foundCrit {
		t.Error("task criterion not round-tripped")
	}
	links, err := dst.ListLinks(taskID)
	if err != nil {
		t.Fatal(err)
	}
	foundLink := false
	for _, l := range links {
		if l.ID == linkID && l.RelatedTaskID == relatedTaskID {
			foundLink = true
		}
	}
	if !foundLink {
		t.Error("task link not round-tripped")
	}
	notes, err := dst.ListNotes(NoteFilter{ProjectID: pID})
	if err != nil {
		t.Fatal(err)
	}
	foundNote := false
	for _, n := range notes {
		if n.ID == noteID && n.Body == "distinguishing note body" {
			foundNote = true
		}
	}
	if !foundNote {
		t.Error("note not round-tripped")
	}
}

// A table added to the schema but forgotten in snapshotTables would silently
// vanish from every backup (this is exactly how `roles` went missing).
func TestSnapshotCoversEverySchemaTable(t *testing.T) {
	s := humanStore(t)
	rows, err := s.DB.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name NOT LIKE 'search_index%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	listed := map[string]bool{}
	for _, n := range snapshotTables {
		listed[n] = true
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		switch name {
		case "embeddings": // derived; rebuilt by `acline reindex-embeddings`
			continue
		case "meta": // holds the approval-token hash: a snapshot must never carry, restore or reset the credential
			continue
		}
		if !listed[name] {
			t.Errorf("schema table %q is not in snapshotTables, so it would be missing from backups", name)
		}
	}
}

func TestSnapshotRoundTripKeepsProjectRoles(t *testing.T) {
	src := humanStore(t)
	pid, err := src.AddProject("demo", "/tmp/demo", "hotl")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := src.AddRole(pid, "auditor", "human", true, nil, ""); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := src.SnapshotJSON(&buf); err != nil {
		t.Fatal(err)
	}
	dst := humanStore(t)
	if _, err := dst.LoadSnapshot(&buf); err != nil {
		t.Fatal(err)
	}
	roles, err := dst.ListRoles(&pid)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range roles {
		if r.Name == "auditor" && r.CanApprove {
			found = true
		}
	}
	if !found {
		t.Errorf("project role lost across snapshot round trip: %+v", roles)
	}
}

// Importing another store's events into a store that has its own would fork
// the hash chain; the import must be refused, not committed.
func TestSnapshotImportRefusesToForkTheAuditChain(t *testing.T) {
	c, d := humanStore(t), humanStore(t)
	for i := 0; i < 3; i++ {
		if _, err := c.LogEvent(nil, nil, "note", "c event"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.LogEvent(nil, nil, "note", "d event"); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := c.SnapshotJSON(&buf); err != nil {
		t.Fatal(err)
	}
	if _, err := d.LoadSnapshot(&buf); err == nil {
		t.Fatal("import that forks the audit chain was accepted")
	}
	res, err := d.VerifyChain()
	if err != nil || !res.OK() {
		t.Fatalf("refused import must leave the chain intact: %+v, %v", res, err)
	}
	if res.Checked != 2 { // d's event and its hash_version marker
		t.Errorf("refused import leaked rows: %d events, want 2", res.Checked)
	}
}
