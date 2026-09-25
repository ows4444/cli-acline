package store

import (
	"errors"
	"strings"
	"testing"
)

func approvedSpec(t *testing.T, s *Store) int64 {
	t.Helper()
	id, err := s.AddSpec("Inventory", "Track stock levels.")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.setSpecStatus(id, "approved"); err != nil {
		t.Fatal(err)
	}
	return id
}

// a small graph: T2 depends on T1, T3 depends on T1 and T2, T3 is a child of T1.
func samplePlan() PlanInput {
	return PlanInput{
		Note: "first cut",
		Items: []PlanItemInput{
			{Ref: "T1", Title: "Schema", Area: "store", Type: "database", Risk: "medium", Milestone: "v1", Criteria: []string{"When a stock row is written, the system shall persist it"}},
			{Ref: "T2", Title: "API", Area: "cli", DependsOn: []string{"T1"}, Size: "M"},
			{Ref: "T3", Title: "UI", Autonomy: "hitl", Parent: "T1", DependsOn: []string{"T1", "T2"}, Milestone: "v1"},
		},
	}
}

func TestProposeRecordsADraftWithItemsEdgesAndCriteria(t *testing.T) {
	s := humanStore(t)
	spec := approvedSpec(t, s)
	id, err := s.ProposePlan(spec, samplePlan())
	if err != nil {
		t.Fatal(err)
	}
	p, _ := s.GetPlan(id)
	if p.Status != "draft" || p.Version != 1 || p.SpecID != spec || p.SpecVersion != 1 {
		t.Fatalf("plan = %+v", p)
	}
	items, _ := s.PlanItems(id)
	if len(items) != 3 || items[0].Ref != "T1" || items[2].Ref != "T3" {
		t.Fatalf("items = %+v", items)
	}
	if got := items[2].DependsOn; len(got) != 2 || got[0] != "T1" || got[1] != "T2" {
		t.Errorf("T3 depends_on = %v", got)
	}
	if len(items[0].Criteria) != 1 || items[0].Risk != "medium" || items[1].Autonomy != "hotl" || items[2].Autonomy != "hitl" {
		t.Errorf("defaults/criteria wrong: %+v", items)
	}
	// nothing became a task
	if n := countRows(t, s, `SELECT COUNT(*) FROM tasks`); n != 0 {
		t.Errorf("a proposal created %d task(s)", n)
	}
	if _, err := s.ProposePlan(spec, samplePlan()); !errors.Is(err, ErrPlanDraftExists) {
		t.Errorf("second draft for the same spec: %v", err)
	}
}

func countRows(t *testing.T, s *Store, q string) int {
	t.Helper()
	var n int
	if err := s.DB.QueryRow(q).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestProposalRulesAreEnforced(t *testing.T) {
	s := humanStore(t)
	spec := approvedSpec(t, s)
	draft, _ := s.AddSpec("draft one", "x")

	item := func(mut func(*PlanItemInput)) PlanInput {
		it := PlanItemInput{Ref: "T1", Title: "ok"}
		mut(&it)
		return PlanInput{Items: []PlanItemInput{it}}
	}
	twoCycle := PlanInput{Items: []PlanItemInput{
		{Ref: "A", Title: "a", DependsOn: []string{"B"}}, {Ref: "B", Title: "b", DependsOn: []string{"A"}}}}
	parentLoop := PlanInput{Items: []PlanItemInput{
		{Ref: "A", Title: "a", Parent: "B"}, {Ref: "B", Title: "b", Parent: "A"}}}
	tooMany := PlanInput{}
	for i := 0; i <= MaxPlanItems; i++ {
		tooMany.Items = append(tooMany.Items, PlanItemInput{Ref: "T" + string(rune('a'+i%26)) + string(rune('a'+i/26)), Title: "x"})
	}
	cases := []struct {
		name    string
		specID  int64
		in      PlanInput
		wantErr string
	}{
		{"empty plan", spec, PlanInput{}, "at least one item"},
		{"too many items", spec, tooMany, "capped at 30"},
		{"auto autonomy", spec, item(func(i *PlanItemInput) { i.Autonomy = "auto" }), `cannot grant autonomy "auto"`},
		{"bad autonomy", spec, item(func(i *PlanItemInput) { i.Autonomy = "yolo" }), "invalid autonomy"},
		{"bad risk", spec, item(func(i *PlanItemInput) { i.Risk = "extreme" }), "invalid risk"},
		{"bad type", spec, item(func(i *PlanItemInput) { i.Type = "nonsense" }), "invalid type"},
		{"bad size", spec, item(func(i *PlanItemInput) { i.Size = "XL" }), "invalid size"},
		{"empty title", spec, item(func(i *PlanItemInput) { i.Title = "  " }), "title must be"},
		{"long title", spec, item(func(i *PlanItemInput) { i.Title = strings.Repeat("x", 201) }), "title must be"},
		{"bad ref", spec, item(func(i *PlanItemInput) { i.Ref = "has space" }), "ref"},
		{"unknown dependency", spec, item(func(i *PlanItemInput) { i.DependsOn = []string{"ZZ"} }), "not in the plan"},
		{"self dependency", spec, item(func(i *PlanItemInput) { i.DependsOn = []string{"T1"} }), "depend on itself"},
		{"unknown parent", spec, item(func(i *PlanItemInput) { i.Parent = "ZZ" }), "parent"},
		{"empty criterion", spec, item(func(i *PlanItemInput) { i.Criteria = []string{""} }), "criterion 1 is empty"},
		{"dependency cycle", spec, twoCycle, "cycle"},
		{"parent loop", spec, parentLoop, "loop"},
		{"duplicate ref", spec, PlanInput{Items: []PlanItemInput{{Ref: "A", Title: "a"}, {Ref: "A", Title: "b"}}}, "duplicate ref"},
		{"draft spec", draft, item(func(*PlanItemInput) {}), "only an approved spec"},
		{"missing spec", 999, item(func(*PlanItemInput) {}), "not found"},
	}
	for _, c := range cases {
		_, err := s.ProposePlan(c.specID, c.in)
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: err = %v, want it to contain %q", c.name, err, c.wantErr)
		}
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM plans`); n != 0 {
		t.Errorf("rejected proposals left %d plan(s) behind", n)
	}
}

func TestParsePlanJSONRejectsUnknownFieldsAndTrailingData(t *testing.T) {
	good := `{"items":[{"ref":"T1","title":"x","depends_on":[]}]}`
	if _, err := ParsePlanJSON(strings.NewReader(good)); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		`{"items":[{"ref":"T1","title":"x","depend_on":["T0"]}]}`, // typo must not drop a dependency
		`{"items":[]} {"items":[]}`,
		`not json`,
	} {
		if _, err := ParsePlanJSON(strings.NewReader(bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestApproveCreatesEverythingAtomicallyAndTheGraphRoutes(t *testing.T) {
	s := humanStore(t)
	spec := approvedSpec(t, s)
	id, _ := s.ProposePlan(spec, samplePlan())

	created, err := s.ApprovePlan(id, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 3 {
		t.Fatalf("created = %+v", created)
	}
	t1, t2, t3 := created[0].TaskID, created[1].TaskID, created[2].TaskID
	task1, _ := s.GetTask(t1)
	task3, _ := s.GetTask(t3)
	if task1.Title != "Schema" || task1.Status != "todo" || task1.Risk != "medium" || task1.Autonomy != "hotl" ||
		!task1.SpecID.Valid || task1.SpecID.Int64 != spec || task1.Area.String != "store" || !task1.MilestoneID.Valid {
		t.Errorf("task 1 = %+v", task1)
	}
	if task3.Autonomy != "hitl" || !task3.ParentID.Valid || task3.ParentID.Int64 != t1 || task3.MilestoneID.Int64 != task1.MilestoneID.Int64 {
		t.Errorf("task 3 = %+v", task3)
	}
	if crit, _ := s.ListCriteria(t1); len(crit) != 1 || crit[0].Pattern.String != "event" {
		t.Errorf("criteria = %+v", crit)
	}
	if pre, _ := s.Prerequisites(t3); len(pre) != 2 {
		t.Errorf("T3 prerequisites = %+v", pre)
	}
	if ms, _ := s.ListMilestones("", nil); len(ms) != 1 || ms[0].Name != "v1" {
		t.Errorf("milestone should be created once and shared: %+v", ms)
	}
	var itemID int64
	s.DB.QueryRow(`SELECT plan_item_id FROM tasks WHERE id = ?`, t2).Scan(&itemID)
	if itemID == 0 {
		t.Error("task has no provenance")
	}
	if p, _ := s.GetPlan(id); p.Status != "approved" || !p.DecidedAt.Valid {
		t.Errorf("plan = %+v", p)
	}
	// and the created graph behaves: T2 and T3 wait, T1 is startable
	if r := routeOf(t, s, t1); r.Action != RouteStartWork {
		t.Errorf("T1 route = %+v", r)
	}
	if r := routeOf(t, s, t2); r.Action != RouteWaitDependency {
		t.Errorf("T2 route = %+v", r)
	}
	if _, err := s.ApprovePlan(id, ""); err == nil {
		t.Error("an approved plan was approved again")
	}
	if _, err := s.RevisePlan(id, samplePlan()); err == nil {
		t.Error("an approved plan was revised")
	}
}

func TestApprovalIsAllOrNothing(t *testing.T) {
	s := humanStore(t)
	spec := approvedSpec(t, s)
	in := samplePlan()
	id, _ := s.ProposePlan(spec, in)
	// Force a failure part-way: an item type the task insert will refuse, written
	// straight into the draft as a corrupted row could be.
	if _, err := s.DB.Exec(`UPDATE plan_items SET risk = 'bogus' WHERE plan_id = ? AND ref = 'T3'`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApprovePlan(id, ""); err == nil {
		t.Fatal("approval succeeded with a corrupt item")
	}
	for _, q := range []string{`SELECT COUNT(*) FROM tasks`, `SELECT COUNT(*) FROM task_links`, `SELECT COUNT(*) FROM task_criteria`, `SELECT COUNT(*) FROM milestones`} {
		if n := countRows(t, s, q); n != 0 {
			t.Errorf("%s = %d after a failed approval", q, n)
		}
	}
	if p, _ := s.GetPlan(id); p.Status != "draft" {
		t.Errorf("plan status = %s, want draft", p.Status)
	}
}

func TestAgentsMayProposeButNeverApproveEditOrReject(t *testing.T) {
	h := humanStore(t)
	spec := approvedSpec(t, h)
	// Same database, agent actor.
	a := *h
	a.Actor = Actor{Type: "agent", ID: "planner"}
	id, err := a.ProposePlan(spec, samplePlan())
	if err != nil {
		t.Fatalf("an agent may propose: %v", err)
	}
	if _, err := a.ApprovePlan(id, ""); !errors.Is(err, ErrAgentCannotDecidePlan) {
		t.Errorf("agent approve = %v", err)
	}
	title := "renamed"
	if err := a.EditPlanItem(id, "T1", PlanItemEdit{Title: &title}, ""); !errors.Is(err, ErrAgentCannotDecidePlan) {
		t.Errorf("agent edit = %v", err)
	}
	if err := a.RejectPlan(id, "no"); !errors.Is(err, ErrAgentCannotDecidePlan) {
		t.Errorf("agent reject = %v", err)
	}
	if n := countRows(t, h, `SELECT COUNT(*) FROM tasks`); n != 0 {
		t.Errorf("an agent caused %d task(s) to exist", n)
	}
}

func TestApprovalTokenIsEnforcedWhenEnabled(t *testing.T) {
	s := humanStore(t)
	spec := approvedSpec(t, s)
	tok, err := s.EnableApprovalToken()
	if err != nil {
		t.Fatal(err)
	}
	id, _ := s.ProposePlan(spec, samplePlan())
	if _, err := s.ApprovePlan(id, ""); !errors.Is(err, ErrApprovalTokenRequired) {
		t.Errorf("no token: %v", err)
	}
	if _, err := s.ApprovePlan(id, "wrong"); !errors.Is(err, ErrApprovalTokenInvalid) {
		t.Errorf("wrong token: %v", err)
	}
	if _, err := s.ApprovePlan(id, tok); err != nil {
		t.Errorf("right token: %v", err)
	}
}

func TestEditAndDropShapeWhatApprovalCreates(t *testing.T) {
	s := humanStore(t)
	spec := approvedSpec(t, s)
	id, _ := s.ProposePlan(spec, samplePlan())
	str := func(v string) *string { return &v }
	yes := true
	if err := s.EditPlanItem(id, "T2", PlanItemEdit{Title: str("API v2"), Risk: str("high"), Size: str("L"), Autonomy: str("hitl")}, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.EditPlanItem(id, "T2", PlanItemEdit{Autonomy: str("auto")}, ""); err == nil {
		t.Error("edit granted auto")
	}
	if err := s.EditPlanItem(id, "T2", PlanItemEdit{Risk: str("nope")}, ""); err == nil {
		t.Error("edit accepted an invalid risk")
	}
	if err := s.EditPlanItem(id, "T9", PlanItemEdit{Title: str("x")}, ""); err == nil {
		t.Error("edited a missing item")
	}
	if err := s.EditPlanItem(id, "T1", PlanItemEdit{Drop: &yes}, ""); err != nil { // drop the root
		t.Fatal(err)
	}
	created, err := s.ApprovePlan(id, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 2 || created[0].Ref != "T2" {
		t.Fatalf("created = %+v", created)
	}
	api, _ := s.GetTask(created[0].TaskID)
	ui, _ := s.GetTask(created[1].TaskID)
	if api.Title != "API v2" || api.Risk != "high" || api.Autonomy != "hitl" {
		t.Errorf("edit not applied: %+v", api)
	}
	if ui.ParentID.Valid { // its parent T1 was dropped
		t.Errorf("child of a dropped item kept a parent: %+v", ui)
	}
	if pre, _ := s.Prerequisites(api.ID); len(pre) != 0 {
		t.Errorf("edge to a dropped item survived: %+v", pre)
	}
	if pre, _ := s.Prerequisites(ui.ID); len(pre) != 1 || pre[0].ID != api.ID {
		t.Errorf("UI should wait only on the API: %+v", pre)
	}
	// dropping everything leaves nothing to approve
	id2spec, _ := s.AddSpec("other", "y")
	s.setSpecStatus(id2spec, "approved")
	p2, _ := s.ProposePlan(id2spec, PlanInput{Items: []PlanItemInput{{Ref: "A", Title: "a"}}})
	s.EditPlanItem(p2, "A", PlanItemEdit{Drop: &yes}, "")
	if _, err := s.ApprovePlan(p2, ""); err == nil || !strings.Contains(err.Error(), "every item was dropped") {
		t.Errorf("all-dropped approval = %v", err)
	}
}

func TestApprovalRefusesAStalePlan(t *testing.T) {
	s := humanStore(t)
	spec := approvedSpec(t, s)
	id, _ := s.ProposePlan(spec, samplePlan())
	if _, err := s.DB.Exec(`UPDATE specs SET version = version + 1 WHERE id = ?`, spec); err != nil { // as `spec revise` would
		t.Fatal(err)
	}
	if _, err := s.ApprovePlan(id, ""); !errors.Is(err, ErrPlanStale) {
		t.Fatalf("stale approve = %v", err)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM tasks`); n != 0 {
		t.Errorf("stale approval created %d task(s)", n)
	}
	// the spec is un-approved: planning and approving both refuse
	s.setSpecStatus(spec, "draft")
	if _, err := s.ApprovePlan(id, ""); err == nil || !strings.Contains(err.Error(), "only an approved spec") {
		t.Errorf("draft-spec approve = %v", err)
	}
}

func TestReviseSupersedesTheDraftAndRejectRecordsANote(t *testing.T) {
	s := humanStore(t)
	spec := approvedSpec(t, s)
	v1, _ := s.ProposePlan(spec, samplePlan())
	v2, err := s.RevisePlan(v1, PlanInput{Items: []PlanItemInput{{Ref: "X", Title: "only this"}}})
	if err != nil {
		t.Fatal(err)
	}
	old, _ := s.GetPlan(v1)
	cur, _ := s.GetPlan(v2)
	if old.Status != "superseded" || cur.Status != "draft" || cur.Version != 2 {
		t.Fatalf("old=%+v cur=%+v", old, cur)
	}
	if _, err := s.RevisePlan(v1, samplePlan()); err == nil {
		t.Error("revised a superseded plan")
	}
	if err := s.RejectPlan(v2, "too coarse"); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.GetPlan(v2); p.Status != "rejected" {
		t.Errorf("status = %s", p.Status)
	}
	notes, _ := s.ListNotes(NoteFilter{UnpromotedOnly: true})
	if len(notes) != 1 || !strings.Contains(notes[0].Body, "too coarse") {
		t.Errorf("notes = %+v", notes)
	}
	// once rejected a new proposal is allowed again, as version 3
	v3, err := s.ProposePlan(spec, samplePlan())
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := s.GetPlan(v3); p.Version != 3 {
		t.Errorf("version = %d", p.Version)
	}
	if plans, _ := s.ListPlans(&spec, "draft"); len(plans) != 1 || plans[0].ID != v3 {
		t.Errorf("drafts = %+v", plans)
	}
}

func TestPlanTextIsScrubbed(t *testing.T) {
	s := humanStore(t)
	spec := approvedSpec(t, s)
	secret := "sk-ant-api03-" + strings.Repeat("A", 40)
	id, err := s.ProposePlan(spec, PlanInput{Note: secret, Items: []PlanItemInput{{Ref: "A", Title: "use " + secret, Criteria: []string{"When " + secret + ", the system shall x"}}}})
	if err != nil {
		t.Fatal(err)
	}
	items, _ := s.PlanItems(id)
	p, _ := s.GetPlan(id)
	all := items[0].Title + strings.Join(items[0].Criteria, "") + p.Note.String
	if strings.Contains(all, secret) {
		t.Errorf("a secret was stored: %q", all)
	}
}

func TestSpecsAwaitingPlanAndDraftPlansTrackThePlanLifecycle(t *testing.T) {
	s := humanStore(t)
	spec := approvedSpec(t, s)
	draftSpec, _ := s.AddSpec("not approved yet", "x") // a draft spec is not "awaiting a plan"
	_ = draftSpec

	awaiting := func() int { l, _ := s.SpecsAwaitingPlan(nil); return len(l) }
	drafts := func() int { l, _ := s.DraftPlans(nil); return len(l) }
	if awaiting() != 1 || drafts() != 0 {
		t.Fatalf("before: awaiting=%d drafts=%d", awaiting(), drafts())
	}
	id, _ := s.ProposePlan(spec, samplePlan())
	if awaiting() != 0 || drafts() != 1 {
		t.Fatalf("after propose: awaiting=%d drafts=%d", awaiting(), drafts())
	}
	// Rejecting puts the spec back on the list: it still has nothing.
	s.RejectPlan(id, "")
	if awaiting() != 1 || drafts() != 0 {
		t.Fatalf("after reject: awaiting=%d drafts=%d", awaiting(), drafts())
	}
	id2, _ := s.ProposePlan(spec, samplePlan())
	if _, err := s.ApprovePlan(id2, ""); err != nil {
		t.Fatal(err)
	}
	// Approved: the spec has tasks now, so it is no longer waiting.
	if awaiting() != 0 || drafts() != 0 {
		t.Fatalf("after approve: awaiting=%d drafts=%d", awaiting(), drafts())
	}
}

func TestSpecWithHandMadeTasksIsNotAwaitingAPlan(t *testing.T) {
	s := humanStore(t)
	spec := approvedSpec(t, s)
	s.AddTask("by hand", "", "normal", TaskOpts{SpecID: &spec})
	if l, _ := s.SpecsAwaitingPlan(nil); len(l) != 0 {
		t.Errorf("a spec that already has tasks was reported: %+v", l)
	}
}

func TestPlanIsStaleOnlyForADraftWhoseSpecMoved(t *testing.T) {
	s := humanStore(t)
	spec := approvedSpec(t, s)
	id, _ := s.ProposePlan(spec, samplePlan())
	p, _ := s.GetPlan(id)
	if s.PlanIsStale(p) {
		t.Error("a fresh draft is stale")
	}
	s.DB.Exec(`UPDATE specs SET version = version + 1 WHERE id = ?`, spec)
	if !s.PlanIsStale(p) {
		t.Error("a draft on a revised spec is not stale")
	}
	s.RejectPlan(id, "")
	p, _ = s.GetPlan(id)
	if s.PlanIsStale(p) {
		t.Error("a rejected plan is reported stale")
	}
}
