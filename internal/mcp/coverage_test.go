package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"acline/internal/store"
	"acline/internal/worktree"
)

func TestCheckRecordThenList(t *testing.T) {
	cs, st := connectedTestServer(t)
	taskID, err := st.AddTask("a task", "", "normal", store.TaskOpts{Risk: "low"})
	if err != nil {
		t.Fatal(err)
	}

	recordOut := callTool[checkRecordOut](t, cs, "acline_check_record", checkRecordArgs{
		TaskID: taskID, Kind: "test", Status: "pass", Detail: "12/12 passed",
	})
	if recordOut.ID == 0 {
		t.Fatal("expected a non-zero check id")
	}

	listOut := callTool[checkListOut](t, cs, "acline_check_list", checkListArgs{TaskID: taskID})
	if len(listOut.Checks) != 1 {
		t.Fatalf("expected 1 check, got %d", len(listOut.Checks))
	}
	if listOut.Checks[0].Status != "pass" || listOut.Checks[0].Kind != "test" {
		t.Errorf("unexpected check: %+v", listOut.Checks[0])
	}

	// The gate should now be satisfied by this check for a low-risk task.
	gateOut := callTool[taskGateOut](t, cs, "acline_task_gate", taskGateArgs{ID: taskID})
	if !gateOut.OK {
		t.Fatalf("expected gate satisfied after a passing check, got blockers: %v", gateOut.Blockers)
	}
}

func TestMemoryTouchAndForget(t *testing.T) {
	cs, st := connectedTestServer(t)
	addOut := callTool[memoryAddOut](t, cs, "acline_memory_add", memoryAddArgs{Body: "a lesson"})

	touchOut := callTool[memoryIDOut](t, cs, "acline_memory_touch", memoryIDArgs{ID: addOut.ID})
	if touchOut.ID != addOut.ID {
		t.Fatalf("expected touch to echo back id %d, got %d", addOut.ID, touchOut.ID)
	}

	forgetOut := callTool[memoryIDOut](t, cs, "acline_memory_forget", memoryIDArgs{ID: addOut.ID})
	if forgetOut.ID != addOut.ID {
		t.Fatalf("expected forget to echo back id %d, got %d", addOut.ID, forgetOut.ID)
	}

	// Forgotten entries are excluded from the default (non-stale) list.
	listOut := callTool[memoryListOut](t, cs, "acline_memory_list", memoryListArgs{})
	for _, m := range listOut.Entries {
		if m.ID == addOut.ID {
			t.Fatalf("expected forgotten memory #%d to be excluded from the default list", addOut.ID)
		}
	}

	entry, err := st.ListMemory(store.MemoryFilter{IncludeStale: true})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range entry {
		if m.ID == addOut.ID {
			found = true
			if !m.Stale {
				t.Error("expected the memory row to be marked stale, not deleted")
			}
		}
	}
	if !found {
		t.Fatal("expected the forgotten memory row to still exist (never deleted)")
	}
}

func TestDecisionAcceptRejectSupersede(t *testing.T) {
	cs, st := connectedTestServer(t)
	oldOut := callTool[decisionAddOut](t, cs, "acline_decision_add", decisionAddArgs{Title: "use mysql"})
	newOut := callTool[decisionAddOut](t, cs, "acline_decision_add", decisionAddArgs{Title: "use postgres instead"})

	// Accepting is a person's decision: refused with no approval token enabled,
	// same as spec approval (see requireApprovalTokenEnabled in decision_spec.go).
	if !refused(cs, "acline_decision_accept", decisionAcceptArgs{ID: newOut.ID}) {
		t.Fatal("accept went through with no approval token enabled")
	}
	tok, err := st.EnableApprovalToken()
	if err != nil {
		t.Fatal(err)
	}
	acceptOut := callTool[idOut](t, cs, "acline_decision_accept", decisionAcceptArgs{ID: newOut.ID, Token: tok})
	if acceptOut.ID != newOut.ID {
		t.Fatalf("expected accept to echo id %d, got %d", newOut.ID, acceptOut.ID)
	}

	supersedeOut := callTool[decisionSupersedeOut](t, cs, "acline_decision_supersede", decisionSupersedeArgs{
		OldID: oldOut.ID, NewID: newOut.ID,
	})
	if supersedeOut.OldID != oldOut.ID || supersedeOut.NewID != newOut.ID {
		t.Fatalf("unexpected supersede result: %+v", supersedeOut)
	}

	listOut := callTool[decisionListOut](t, cs, "acline_decision_list", decisionListArgs{})
	var oldDecision, newDecision *decisionOut
	for i := range listOut.Decisions {
		switch listOut.Decisions[i].ID {
		case oldOut.ID:
			oldDecision = &listOut.Decisions[i]
		case newOut.ID:
			newDecision = &listOut.Decisions[i]
		}
	}
	if oldDecision == nil || oldDecision.Status != "superseded" {
		t.Fatalf("expected old decision to be status=superseded, got %+v", oldDecision)
	}
	if newDecision == nil || newDecision.Status != "accepted" {
		t.Fatalf("expected new decision to remain status=accepted, got %+v", newDecision)
	}

	// A third decision, rejected.
	thirdOut := callTool[decisionAddOut](t, cs, "acline_decision_add", decisionAddArgs{Title: "use sqlite"})
	rejectOut := callTool[idOut](t, cs, "acline_decision_reject", idArgs(thirdOut))
	if rejectOut.ID != thirdOut.ID {
		t.Fatalf("expected reject to echo id %d, got %d", thirdOut.ID, rejectOut.ID)
	}

	// Retiring an accepted decision is gated like accepting it.
	if !refused(cs, "acline_decision_reject", decisionRetireArgs{ID: newOut.ID}) {
		t.Fatal("an accepted decision was rejected without the approval token")
	}
	if !refused(cs, "acline_decision_supersede", decisionSupersedeArgs{OldID: newOut.ID, NewID: thirdOut.ID}) {
		t.Fatal("an accepted decision was superseded without the approval token")
	}
	callTool[idOut](t, cs, "acline_decision_reject", decisionRetireArgs{ID: newOut.ID, Token: tok})
}

func TestSpecApproveAndRevise(t *testing.T) {
	cs, st := connectedTestServer(t)
	addOut := callTool[specAddOut](t, cs, "acline_spec_add", specAddArgs{Title: "auth spec", Body: "v1 body"})

	// Approving is a person's decision: refused with no approval token enabled.
	if !refused(cs, "acline_spec_approve", specApproveArgs{ID: addOut.ID}) {
		t.Fatal("approve went through with no approval token enabled")
	}
	tok, err := st.EnableApprovalToken()
	if err != nil {
		t.Fatal(err)
	}
	approveOut := callTool[idOut](t, cs, "acline_spec_approve", specApproveArgs{ID: addOut.ID, Token: tok})
	if approveOut.ID != addOut.ID {
		t.Fatalf("expected approve to echo id %d, got %d", addOut.ID, approveOut.ID)
	}

	reviseOut := callTool[specReviseOut](t, cs, "acline_spec_revise", specReviseArgs{ID: addOut.ID, Body: "v2 body"})
	if reviseOut.Version != 2 {
		t.Fatalf("expected version 2 after one revision, got %d", reviseOut.Version)
	}

	listOut := callTool[specListOut](t, cs, "acline_spec_list", specListArgs{})
	found := false
	for _, sp := range listOut.Specs {
		if sp.ID == addOut.ID {
			found = true
			if sp.Body == nil || *sp.Body != "v2 body" {
				t.Errorf("expected revised body, got %+v", sp.Body)
			}
			if sp.Version != 2 {
				t.Errorf("expected version 2, got %d", sp.Version)
			}
		}
	}
	if !found {
		t.Fatalf("expected spec #%d in the list", addOut.ID)
	}
}

func TestDepAddListVerify(t *testing.T) {
	cs, _ := connectedTestServer(t)
	addOut := callTool[depAddOut](t, cs, "acline_dep_add", depAddArgs{
		Ecosystem: "npm", Name: "left-pad", Version: "1.3.0",
	})
	if addOut.ID == 0 {
		t.Fatal("expected a non-zero dependency id")
	}
	if addOut.Verified {
		t.Fatal("expected unverified by default")
	}

	unverifiedOut := callTool[depListOut](t, cs, "acline_dep_list", depListArgs{UnverifiedOnly: true})
	found := false
	for _, d := range unverifiedOut.Dependencies {
		if d.ID == addOut.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("expected the new dependency in the unverified-only list")
	}

	callTool[depVerifyOut](t, cs, "acline_dep_verify", idArgs{ID: addOut.ID})

	unverifiedAfter := callTool[depListOut](t, cs, "acline_dep_list", depListArgs{UnverifiedOnly: true})
	for _, d := range unverifiedAfter.Dependencies {
		if d.ID == addOut.ID {
			t.Fatal("expected the dependency to drop out of the unverified-only list after verify")
		}
	}
}

func TestDashboardMetricsVerifyProjectList(t *testing.T) {
	cs, st := connectedTestServer(t)
	taskID, err := st.AddTask("urgent task", "", "urgent", store.TaskOpts{Risk: "high"})
	if err != nil {
		t.Fatal(err)
	}

	dashOut := callTool[dashboardOut](t, cs, "acline_dashboard", dashboardArgs{})
	if dashOut.TasksTotal < 1 {
		t.Fatalf("expected at least 1 task, got %d", dashOut.TasksTotal)
	}
	found := false
	for _, tk := range dashOut.TasksNeedingAttention {
		if tk.ID == taskID {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected urgent/high-risk task #%d to appear in tasks_needing_attention", taskID)
	}

	metricsOut := callTool[metricsOut](t, cs, "acline_metrics", metricsArgs{})
	if metricsOut.TasksTotal < 1 {
		t.Fatalf("expected metrics to report at least 1 task, got %d", metricsOut.TasksTotal)
	}

	verifyOut := callTool[verifyOut](t, cs, "acline_verify", verifyArgs{})
	if !verifyOut.OK {
		t.Fatalf("expected a fresh test store's audit chain to verify OK, got: %+v", verifyOut)
	}

	if _, err := st.AddProject("demo", "/tmp/demo", "hotl"); err != nil {
		t.Fatal(err)
	}
	projListOut := callTool[projectListOut](t, cs, "acline_project_list", projectListArgs{})
	foundProject := false
	for _, p := range projListOut.Projects {
		if p.Name == "demo" {
			foundProject = true
		}
	}
	if !foundProject {
		t.Fatalf("expected registered project %q in acline_project_list output, got: %+v", "demo", projListOut.Projects)
	}
}

func TestTaskListPagination(t *testing.T) {
	cs, st := connectedTestServer(t)
	for i := 0; i < 5; i++ {
		if _, err := st.AddTask(fmt.Sprintf("task %d", i), "", "normal", store.TaskOpts{Risk: "low"}); err != nil {
			t.Fatal(err)
		}
	}

	page := callTool[taskListOut](t, cs, "acline_task_list", taskListArgs{Limit: 2})
	if len(page.Tasks) != 2 {
		t.Fatalf("expected 2 tasks with limit=2, got %d", len(page.Tasks))
	}
	if !page.Truncated {
		t.Fatal("expected truncated=true when more rows exist beyond the limit")
	}

	rest := callTool[taskListOut](t, cs, "acline_task_list", taskListArgs{Limit: 2, Offset: 2})
	if len(rest.Tasks) != 2 {
		t.Fatalf("expected 2 tasks with limit=2 offset=2, got %d", len(rest.Tasks))
	}
	if rest.Tasks[0].ID == page.Tasks[0].ID {
		t.Fatal("expected offset to skip past the first page")
	}

	all := callTool[taskListOut](t, cs, "acline_task_list", taskListArgs{})
	if len(all.Tasks) != 5 || all.Truncated {
		t.Fatalf("expected all 5 tasks untruncated with default limit, got %d (truncated=%v)", len(all.Tasks), all.Truncated)
	}
}

func TestDecisionAndSpecAddRedactSecrets(t *testing.T) {
	cs, _ := connectedTestServer(t)

	decOut := callTool[decisionAddOut](t, cs, "acline_decision_add", decisionAddArgs{
		Title:     "rotate creds",
		Rationale: "found leaked key AKIAIOSFODNN7EXAMPLE in the old config",
	})
	decListOut := callTool[decisionListOut](t, cs, "acline_decision_list", decisionListArgs{})
	for _, d := range decListOut.Decisions {
		if d.ID == decOut.ID && d.Rationale != nil && containsSecret(*d.Rationale) {
			t.Fatalf("secret leaked into stored decision rationale: %q", *d.Rationale)
		}
	}

	specOut := callTool[specAddOut](t, cs, "acline_spec_add", specAddArgs{
		Title: "auth spec",
		Body:  "connect via postgres://appuser:hunter2pass@db.internal:5432/prod",
	})
	specsAfterAdd := callTool[specListOut](t, cs, "acline_spec_list", specListArgs{})
	for _, sp := range specsAfterAdd.Specs {
		if sp.ID == specOut.ID && sp.Body != nil && containsSecret(*sp.Body) {
			t.Fatalf("secret leaked into stored spec body: %q", *sp.Body)
		}
	}

	revised := callTool[specReviseOut](t, cs, "acline_spec_revise", specReviseArgs{
		ID:   specOut.ID,
		Body: "rotate this key: AKIAIOSFODNN7EXAMPLE",
	})
	specsAfterRevise := callTool[specListOut](t, cs, "acline_spec_list", specListArgs{})
	for _, sp := range specsAfterRevise.Specs {
		if sp.ID == revised.ID && sp.Body != nil && containsSecret(*sp.Body) {
			t.Fatalf("secret leaked into revised spec body: %q", *sp.Body)
		}
	}
}

func containsSecret(s string) bool {
	return strings.Contains(s, "AKIAIOSFODNN7EXAMPLE") || strings.Contains(s, "hunter2pass")
}

func TestNoteAddAndLogValidation(t *testing.T) {
	cs, _ := connectedTestServer(t)

	if res, err := cs.CallTool(context.Background(), &sdkmcp.CallToolParams{
		Name: "acline_note_add", Arguments: noteAddArgs{Body: ""},
	}); err != nil || !res.IsError {
		t.Fatalf("expected an error for an empty note body, err=%v res=%+v", err, res)
	}

	if res, err := cs.CallTool(context.Background(), &sdkmcp.CallToolParams{
		Name: "acline_log", Arguments: logArgs{Message: ""},
	}); err != nil || !res.IsError {
		t.Fatalf("expected an error for an empty log message, err=%v res=%+v", err, res)
	}

	if res, err := cs.CallTool(context.Background(), &sdkmcp.CallToolParams{
		Name: "acline_note_add", Arguments: noteAddArgs{Body: "x", Project: "does-not-exist"},
	}); err != nil || !res.IsError {
		t.Fatalf("expected an error for an unknown project name, err=%v res=%+v", err, res)
	}

	if res, err := cs.CallTool(context.Background(), &sdkmcp.CallToolParams{
		Name: "acline_log", Arguments: map[string]any{"message": "x"},
	}); err != nil || !res.IsError {
		t.Fatalf("expected an error for a log call with no type, err=%v res=%+v", err, res)
	}

	logOut := callTool[logOut](t, cs, "acline_log", logArgs{Message: "rotate this key: AKIAIOSFODNN7EXAMPLE", Type: "blocker"})
	if logOut.ID == 0 {
		t.Fatal("expected a non-zero event id")
	}
}

func TestDepAddRejectsUnknownTask(t *testing.T) {
	cs, _ := connectedTestServer(t)
	badTaskID := int64(999999)
	res, err := cs.CallTool(context.Background(), &sdkmcp.CallToolParams{
		Name:      "acline_dep_add",
		Arguments: depAddArgs{Ecosystem: "npm", Name: "left-pad", TaskID: &badTaskID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected an error for a nonexistent task id")
	}
}

func TestCheckRunRecordsRealOutcomeAndTakesNoCommand(t *testing.T) {
	cs, st := connectedTestServer(t)
	id, _ := st.AddTask("t", "", "normal", store.TaskOpts{})
	// This package's directory has no go.mod, so there is no default runner:
	// the honest result is skipped, and it must be recorded as such.
	out := callTool[checkRunOut](t, cs, "acline_check_run", checkRunArgs{TaskID: id, Kind: "sast"})
	if out.ID == 0 || out.Status != "skipped" {
		t.Fatalf("run = %+v", out)
	}
	checks, _ := st.ListChecks(id)
	if len(checks) != 1 || checks[0].Kind != "sast" || checks[0].Status != "skipped" {
		t.Fatalf("recorded = %+v", checks)
	}
	if r := callToolRaw(t, cs, "acline_check_run", checkRunArgs{TaskID: id, Kind: "human_review"}); !r.IsError {
		t.Error("a kind with no runner was accepted")
	}
}

func TestCheckRunUsesTheProjectsConfiguredRunner(t *testing.T) {
	cs, st := connectedTestServer(t)
	pid, _ := st.AddProject("web", "", "")
	if err := st.SetCheckRunner(pid, "sast", "echo custom-runner-output", ""); err != nil {
		t.Fatal(err)
	}
	id, _ := st.AddTask("t", "", "normal", store.TaskOpts{ProjectID: &pid})
	out := callTool[checkRunOut](t, cs, "acline_check_run", checkRunArgs{TaskID: id, Kind: "sast"})
	if out.Status != "pass" || !strings.Contains(out.Detail, "custom-runner-output") {
		t.Fatalf("run = %+v", out)
	}
	// A kind the project has no runner for still falls back to the (here absent) default.
	if out := callTool[checkRunOut](t, cs, "acline_check_run", checkRunArgs{TaskID: id, Kind: "sca"}); out.Status != "skipped" {
		t.Fatalf("sca = %+v", out)
	}
}

// acline_check_run used to run the tool in the server's working directory but
// seal the result with the task's project tree, so another checkout's tests
// could stand in for this one's.
func TestCheckRunRunsInTheTasksProjectNotTheServersCwd(t *testing.T) {
	cs, st := connectedTestServer(t)
	projectDir, elsewhere := t.TempDir(), t.TempDir()
	t.Chdir(elsewhere)
	pid, _ := st.AddProject("web", projectDir, "")
	if err := st.SetCheckRunner(pid, "test", "touch ran-here", ""); err != nil {
		t.Fatal(err)
	}
	id, _ := st.AddTask("t", "", "normal", store.TaskOpts{ProjectID: &pid})

	if out := callTool[checkRunOut](t, cs, "acline_check_run", checkRunArgs{TaskID: id, Kind: "test"}); out.Status != "pass" {
		t.Fatalf("run = %+v", out)
	}
	if _, err := os.Stat(filepath.Join(projectDir, "ran-here")); err != nil {
		t.Fatalf("the runner did not run in the task's project: %v", err)
	}
	if _, err := os.Stat(filepath.Join(elsewhere, "ran-here")); err == nil {
		t.Fatal("the runner ran in the server's working directory")
	}
	checks, _ := st.ListChecks(id)
	if want := worktree.Hash(projectDir); len(checks) != 1 || checks[0].TreeHash.String != want {
		t.Fatalf("sealed tree = %+v, want the project's %q", checks, want)
	}
}
