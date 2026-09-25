package mcp

import (
	"context"
	"strings"
	"testing"

	"acline/internal/store"
)

func TestSetStatusToolMovesTasksButNeverToDone(t *testing.T) {
	cs, st := connectedTestServer(t)
	id, _ := st.AddTask("t", "", "normal", store.TaskOpts{Risk: "high"})

	callTool[taskSetStatusOut](t, cs, "acline_task_set_status", taskSetStatusArgs{ID: id, Status: "in_progress"})
	if got, _ := st.GetTask(id); got.Status != "in_progress" {
		t.Fatalf("status = %q", got.Status)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM events WHERE type='status_change' AND task_id=?`, id); n != 1 {
		t.Errorf("status_change events = %d, want 1", n)
	}

	// The gate must not be bypassable through this tool.
	if r := callToolRaw(t, cs, "acline_task_set_status", taskSetStatusArgs{ID: id, Status: "done"}); !r.IsError {
		t.Fatal("set_status accepted 'done', bypassing the completion gate")
	}
	if got, _ := st.GetTask(id); got.Status == "done" {
		t.Fatal("task ended up done")
	}
	if r := callToolRaw(t, cs, "acline_task_set_status", taskSetStatusArgs{ID: id, Status: "nonsense"}); !r.IsError {
		t.Fatal("invalid status accepted")
	}
}

func TestCriteriaToolsListAddCheckAndFeedTheGateWarning(t *testing.T) {
	cs, st := connectedTestServer(t)
	id, _ := st.AddTask("t", "", "normal", store.TaskOpts{})
	st.AddCheck(id, "test", "pass", "")

	add := callTool[criterionAddOut](t, cs, "acline_criteria_add", criterionAddArgs{TaskID: id, Text: "When the user saves, the system shall persist it"})
	if add.ID == 0 || add.Pattern == "" {
		t.Fatalf("add = %+v (EARS pattern should be detected)", add)
	}
	gate := callTool[taskGateOut](t, cs, "acline_task_gate", taskGateArgs{ID: id})
	if len(gate.Warnings) == 0 {
		t.Fatal("gate should warn about the unchecked criterion")
	}

	list := callTool[criteriaListOut](t, cs, "acline_criteria_list", criteriaListArgs{TaskID: id})
	if len(list.Criteria) != 1 || list.Criteria[0].Done {
		t.Fatalf("list = %+v", list)
	}
	callTool[criterionCheckOut](t, cs, "acline_criteria_check", criterionCheckArgs{ID: add.ID})
	if gate := callTool[taskGateOut](t, cs, "acline_task_gate", taskGateArgs{ID: id}); len(gate.Warnings) != 0 {
		t.Errorf("warning should clear once the criterion is checked: %v", gate.Warnings)
	}
	no := false
	if out := callTool[criterionCheckOut](t, cs, "acline_criteria_check", criterionCheckArgs{ID: add.ID, Done: &no}); out.Done {
		t.Error("done=false should reopen")
	}
}

func TestTaskLinkTool(t *testing.T) {
	cs, st := connectedTestServer(t)
	a, _ := st.AddTask("a", "", "normal", store.TaskOpts{})
	b, _ := st.AddTask("b", "", "normal", store.TaskOpts{})

	out := callTool[taskLinkOut](t, cs, "acline_task_link", taskLinkArgs{TaskID: a, Relation: "depends_on", RelatedTaskID: b})
	if out.ID == 0 {
		t.Fatal("no link id")
	}
	links, err := st.ListLinks(a)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 || links[0].RelatedTaskID != b || links[0].Relation != "depends_on" {
		t.Fatalf("links = %+v", links)
	}

	if r := callToolRaw(t, cs, "acline_task_link", taskLinkArgs{TaskID: a, Relation: "nonsense", RelatedTaskID: b}); !r.IsError {
		t.Fatal("invalid relation accepted")
	}
	if r := callToolRaw(t, cs, "acline_task_link", taskLinkArgs{TaskID: 999, Relation: "related", RelatedTaskID: b}); !r.IsError {
		t.Fatal("nonexistent task accepted")
	}
}

func TestTaskDeferTool(t *testing.T) {
	cs, st := connectedTestServer(t)
	id, _ := st.AddTask("t", "", "normal", store.TaskOpts{})

	if r := callToolRaw(t, cs, "acline_task_defer", taskDeferArgs{ID: id}); !r.IsError {
		t.Fatal("defer without a reason and without clear=true should be refused")
	}

	out := callTool[taskDeferOut](t, cs, "acline_task_defer", taskDeferArgs{ID: id, Reason: "waiting on design", Trigger: "after v2 ships"})
	if !out.Deferred {
		t.Fatalf("out = %+v", out)
	}
	got, _ := st.GetTask(id)
	if !got.Deferred || got.DeferredReason.String != "waiting on design" || got.RevisitTrigger.String != "after v2 ships" {
		t.Fatalf("task = %+v", got)
	}

	clear := callTool[taskDeferOut](t, cs, "acline_task_defer", taskDeferArgs{ID: id, Clear: true})
	if clear.Deferred {
		t.Fatalf("clear = %+v", clear)
	}
	got, _ = st.GetTask(id)
	if got.Deferred {
		t.Fatal("deferred flag should be cleared")
	}
}

func countRows(t *testing.T, st *store.Store, q string, args ...any) int {
	t.Helper()
	var n int
	if err := st.DB.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSessionLifecycleTools(t *testing.T) {
	cs, st := connectedTestServer(t)
	id, _ := st.AddTask("t", "", "normal", store.TaskOpts{})
	start := callTool[sessionStartOut](t, cs, "acline_session_start", sessionStartArgs{TaskID: &id, DenyTools: []string{"acline_memory_add"}})
	if start.ID == 0 {
		t.Fatal("no session id")
	}
	if got, _ := st.GetTask(id); got.Status != "in_progress" {
		t.Errorf("task status = %q", got.Status)
	}
	if !refused(cs, "acline_session_start", sessionStartArgs{}) {
		t.Error("second concurrent session accepted")
	}
	// the policy just started governs later mutating calls
	if !refused(cs, "acline_memory_add", memoryAddArgs{Body: "x"}) {
		t.Error("session policy not applied to the very next call")
	}
	end := callTool[sessionEndOut](t, cs, "acline_session_end", sessionEndArgs{Summary: "done"})
	if end.ID != start.ID {
		t.Errorf("ended #%d, started #%d", end.ID, start.ID)
	}
	callTool[memoryAddOut](t, cs, "acline_memory_add", memoryAddArgs{Body: "allowed again"})
}

func TestNoteListPromoteAndEvalRecordTools(t *testing.T) {
	cs, st := connectedTestServer(t)
	nid, _ := st.AddNote(nil, "prefer sqlite for local state", "manual")

	list := callTool[noteListOut](t, cs, "acline_note_list", noteListArgs{UnpromotedOnly: true})
	if len(list.Notes) != 1 || list.Notes[0].ID != nid {
		t.Fatalf("list = %+v", list)
	}
	out := callTool[notePromoteOut](t, cs, "acline_note_promote", notePromoteArgs{NoteID: nid, Kind: "decision", Rationale: "simplicity"})
	if out.ID == 0 || out.Kind != "decision" {
		t.Fatalf("promote = %+v", out)
	}
	if list := callTool[noteListOut](t, cs, "acline_note_list", noteListArgs{UnpromotedOnly: true}); len(list.Notes) != 0 {
		t.Error("promoted note still in the unpromoted queue")
	}
	if !refused(cs, "acline_note_promote", notePromoteArgs{NoteID: nid, Kind: "memory"}) {
		t.Error("double promotion accepted")
	}

	rec := callTool[evalRecordOut](t, cs, "acline_eval_record", evalRecordArgs{Suite: "refactor", PassRate: 0.9, SampleSize: 50})
	if rec.ID == 0 {
		t.Fatal("no eval id")
	}
	if !refused(cs, "acline_eval_record", evalRecordArgs{Suite: "refactor", PassRate: 1.5}) {
		t.Error("out-of-range pass rate accepted")
	}
}

func TestFeatureAndRoleTools(t *testing.T) {
	cs, st := connectedTestServer(t)
	pid, _ := st.AddProject("demo", "/tmp/demo", "hotl")

	f := callTool[idOut](t, cs, "acline_feature_add", featureAddArgs{Name: "dashboard", OwnerArea: "cli"})
	list := callTool[featureListOut](t, cs, "acline_feature_list", featureListArgs{})
	if len(list.Features) != 1 || list.Features[0].ID != f.ID || list.Features[0].Status != "live" {
		t.Fatalf("list = %+v", list)
	}
	callTool[idOut](t, cs, "acline_feature_set_status", featureSetStatusArgs{ID: f.ID, Status: "deprecated"})
	if got := callTool[featureListOut](t, cs, "acline_feature_list", featureListArgs{Status: "deprecated"}); len(got.Features) != 1 {
		t.Errorf("status filter = %+v", got)
	}
	if !refused(cs, "acline_feature_set_status", featureSetStatusArgs{ID: 999, Status: "removed"}) {
		t.Error("missing feature accepted")
	}

	callTool[idOut](t, cs, "acline_role_add", roleAddArgs{Project: "demo", Name: "auditor", Kind: "human"})
	tid, _ := st.AddTask("t", "", "normal", store.TaskOpts{ProjectID: &pid})
	as := callTool[taskAssignOut](t, cs, "acline_task_assign", taskAssignArgs{ID: tid, Role: "auditor"})
	if as.Role != "auditor" || as.Previous != "unassigned" {
		t.Errorf("assign = %+v", as)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM events WHERE type='role_assigned' AND task_id=?`, tid); n != 1 {
		t.Errorf("role_assigned events = %d", n)
	}
	if !refused(cs, "acline_role_add", roleAddArgs{Name: "x"}) {
		t.Error("role without a project accepted")
	}

	// can_approve role needs the token once enabled
	token, _ := st.EnableApprovalToken()
	if !refused(cs, "acline_role_add", roleAddArgs{Project: "demo", Name: "signer", CanApprove: true}) {
		t.Error("can_approve role created without the token")
	}
	callTool[idOut](t, cs, "acline_role_add", roleAddArgs{Project: "demo", Name: "signer", CanApprove: true, Token: token})
}

func TestSessionStartReturnsRelevantLessons(t *testing.T) {
	cs, st := connectedTestServer(t)
	id, _ := st.AddTask("t", "", "normal", store.TaskOpts{Area: "store"})
	st.AddMemory("store", "pitfall", "reviewed lesson")
	st.AddMemory("store", "pitfall", "unreviewed lesson", store.MemoryOpts{ForcePending: true})
	start := callTool[sessionStartOut](t, cs, "acline_session_start", sessionStartArgs{TaskID: &id})
	if len(start.Lessons) != 1 || start.Lessons[0].Body != "reviewed lesson" {
		t.Fatalf("lessons = %+v", start.Lessons)
	}
}

func TestTaskRouteTool(t *testing.T) {
	cs, st := connectedTestServer(t)
	if out := callTool[taskRouteOut](t, cs, "acline_task_route", taskRouteArgs{}); out.Found {
		t.Fatalf("empty store: %+v", out)
	}
	id, _ := st.AddTask("t", "", "normal", store.TaskOpts{})
	st.AddCriterion(id, "When X, the system shall Y")
	out := callTool[taskRouteOut](t, cs, "acline_task_route", taskRouteArgs{})
	if !out.Found || out.TaskID != id || out.Action != store.RouteStartWork || out.Role == nil || *out.Role != "developer" {
		t.Fatalf("pick = %+v", out)
	}
	byID := callTool[taskRouteOut](t, cs, "acline_task_route", taskRouteArgs{ID: &id})
	if byID.Action != store.RouteStartWork {
		t.Fatalf("by id = %+v", byID)
	}
}

func TestTaskBriefTool(t *testing.T) {
	cs, st := connectedTestServer(t)
	if out := callTool[taskBriefOut](t, cs, "acline_task_brief", taskBriefArgs{}); out.Found {
		t.Fatalf("empty store: %+v", out)
	}
	id, _ := st.AddTask("write it", "", "normal", store.TaskOpts{})
	st.AddCriterion(id, "When X, the system shall Y")
	out := callTool[taskBriefOut](t, cs, "acline_task_brief", taskBriefArgs{})
	if !out.Found || out.TaskID != id || out.Action != store.RouteStartWork || out.Role == nil || *out.Role != "developer" {
		t.Fatalf("pick = %+v", out)
	}
	if !strings.Contains(out.Markdown, "# Task #1: write it") || !strings.Contains(out.Markdown, "When X, the system shall Y") {
		t.Fatalf("markdown = %s", out.Markdown)
	}
	if r := callToolRaw(t, cs, "acline_task_brief", taskBriefArgs{ID: func() *int64 { n := int64(999); return &n }()}); !r.IsError {
		t.Error("unknown task should error")
	}
}

func TestPlanToolsProposeAndRead(t *testing.T) {
	cs, st := connectedTestServer(t)
	spec, _ := st.AddSpec("Inventory", "Track stock.")
	st.ApproveSpec(spec, "")

	// The tool surface: propose and read only.
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range res.Tools {
		if strings.HasPrefix(tool.Name, "acline_plan_") {
			switch tool.Name {
			case "acline_plan_propose", "acline_plan_list", "acline_plan_show",
				"acline_plan_approve", "acline_plan_edit_item", "acline_plan_reject":
			default:
				t.Errorf("unexpected plan tool %q: a new plan tool must be classified (read-only, propose, or a token-gated decision)", tool.Name)
			}
		}
	}

	out := callTool[planProposeOut](t, cs, "acline_plan_propose", planProposeArgs{
		SpecID: spec, Note: "first cut",
		Items: []planItemArgs{
			{Ref: "T1", Title: "Schema", Criteria: []string{"When X, the system shall Y"}},
			{Ref: "T2", Title: "API", DependsOn: []string{"T1"}, Autonomy: "hitl"},
		},
	})
	if out.ID == 0 || out.Status != "draft" || out.Items != 2 {
		t.Fatalf("propose = %+v", out)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM tasks`); n != 0 {
		t.Errorf("proposing created %d task(s)", n)
	}
	shown := callTool[planShowOut](t, cs, "acline_plan_show", planShowArgs{ID: out.ID})
	if shown.WouldCreate != 2 || shown.Plan.Status != "draft" || shown.Plan.Stale || len(shown.Items) != 2 ||
		len(shown.Items[1].DependsOn) != 1 || shown.Items[1].Autonomy != "hitl" || shown.Plan.ProposedBy == nil {
		t.Fatalf("show = %+v", shown)
	}
	listed := callTool[planListOut](t, cs, "acline_plan_list", planListArgs{SpecID: &spec, Status: "draft"})
	if len(listed.Plans) != 1 || listed.Plans[0].ID != out.ID {
		t.Fatalf("list = %+v", listed)
	}

	// Rules still apply through MCP: no auto, no cycles, no second draft.
	for name, args := range map[string]planProposeArgs{
		"auto":  {SpecID: spec, Items: []planItemArgs{{Ref: "A", Title: "a", Autonomy: "auto"}}},
		"draft": {SpecID: spec, Items: []planItemArgs{{Ref: "A", Title: "a"}}},
	} {
		if r := callToolRaw(t, cs, "acline_plan_propose", args); !r.IsError {
			t.Errorf("%s: proposal accepted", name)
		}
	}

	// A revised spec makes the draft stale, and the view says so.
	st.DB.Exec(`UPDATE specs SET version = version + 1 WHERE id = ?`, spec)
	if shown := callTool[planShowOut](t, cs, "acline_plan_show", planShowArgs{ID: out.ID}); !shown.Plan.Stale {
		t.Error("a plan on a revised spec was not reported stale")
	}
}

func TestPlanViewsAreNotGatedByASessionPolicyButProposingIs(t *testing.T) {
	cs, st := connectedTestServer(t)
	spec, _ := st.AddSpec("s", "b")
	st.ApproveSpec(spec, "")
	callTool[sessionStartOut](t, cs, "acline_session_start", sessionStartArgs{DenyTools: []string{"acline_plan_propose", "acline_plan_list", "acline_plan_show"}})
	if !refused(cs, "acline_plan_propose", planProposeArgs{SpecID: spec, Items: []planItemArgs{{Ref: "A", Title: "a"}}}) {
		t.Error("a denied propose went through")
	}
	if refused(cs, "acline_plan_list", planListArgs{}) {
		t.Error("a read-only view was blocked by the policy")
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM plans`); n != 0 {
		t.Errorf("a denied propose still created %d plan(s)", n)
	}
}
