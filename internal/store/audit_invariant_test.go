package store

import (
	"reflect"
	"strings"
	"testing"
)

// Every change to the store leaves an audit event, written in the same
// transaction as the change, so the trail can neither miss a change nor record
// one that did not happen. Before this, task creation, role assignment,
// dependency verification, evals, memory, sessions, milestones, features,
// snapshot imports and token changes wrote no event, or wrote it afterwards in
// a separate transaction from the adapter.
//
// TestEveryStoreMethodIsClassified fails when an exported method is added
// without saying whether it changes the store; TestEveryChangeWritesItsEvent
// runs each one that does and checks for its event.

// mutation runs one store method against a fixture and names the event it must write.
type mutation struct {
	event string
	run   func(f *auditFixture) error
}

type auditFixture struct {
	t                                  *testing.T
	s                                  *Store
	project, task, other, spec, plan   int64
	decision, milestone, feature, memo int64
	note, dep, criterion, role         int64
}

func newAuditFixture(t *testing.T) *auditFixture {
	t.Helper()
	s := humanStore(t)
	f := &auditFixture{t: t, s: s}
	must := func(id int64, err error) int64 {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	f.project = must(s.AddProject("p", "", ""))
	f.spec = must(s.AddSpec("spec", "body", SpecOpts{ProjectID: &f.project}))
	if err := s.ApproveSpec(f.spec, ""); err != nil {
		t.Fatal(err)
	}
	f.task = must(s.AddTask("task", "", "normal", TaskOpts{ProjectID: &f.project}))
	f.other = must(s.AddTask("other", "", "normal", TaskOpts{ProjectID: &f.project}))
	f.decision = must(s.AddDecision("decision", DecisionOpts{ProjectID: &f.project}))
	f.milestone = must(s.AddMilestone("m1", MilestoneOpts{ProjectID: &f.project}))
	f.feature = must(s.AddFeature("feat", "", FeatureOpts{ProjectID: &f.project}))
	f.memo = must(s.AddMemory("cli", "lesson", "a lesson", MemoryOpts{ProjectID: &f.project}))
	f.note = must(s.AddNote(&f.project, "a note", ""))
	f.dep = must(s.AddDependency(&f.task, &f.project, "go", "example.com/x", "v1", false))
	var err error
	f.criterion, _, err = s.AddCriterion(f.task, "The system shall work")
	if err != nil {
		t.Fatal(err)
	}
	f.role = must(s.AddRole(f.project, "reviewer", "both", false, nil, ""))
	f.plan = must(s.ProposePlan(f.spec, PlanInput{Items: []PlanItemInput{{Ref: "a", Title: "first"}}}))
	return f
}

var auditedMutations = map[string]mutation{
	"AcceptDecision": {"decision_recorded", func(f *auditFixture) error { return f.s.AcceptDecision(f.decision, "") }},
	"AddApproval": {"approval", func(f *auditFixture) error {
		_, err := f.s.AddApproval(f.task, "code_review", "bob", "approved", "")
		return err
	}},
	"AddApprovalWithRole": {"approval", func(f *auditFixture) error {
		_, err := f.s.AddApprovalWithRole(f.task, nil, "code_review", "bob", "rejected", "no")
		return err
	}},
	"AddCheck": {"check", func(f *auditFixture) error { _, err := f.s.AddCheck(f.task, "lint", "pass", ""); return err }},
	"AddCheckWithRole": {"check", func(f *auditFixture) error {
		_, err := f.s.AddCheckWithRole(f.task, nil, "lint", "pass", "")
		return err
	}},
	"AddCheckWithRoleAndToken": {"check", func(f *auditFixture) error {
		_, err := f.s.AddCheckWithRoleAndToken(f.task, nil, "lint", "pass", "", "")
		return err
	}},
	"AddCheckWithMeta": {"check", func(f *auditFixture) error {
		_, err := f.s.AddCheckWithMeta(f.task, nil, "test", "pass", "", "", CheckMeta{Source: CheckSourceRunner, TreeHash: "sha256:x"})
		return err
	}},
	"AddCriterion": {"criterion_added", func(f *auditFixture) error { _, _, err := f.s.AddCriterion(f.task, "The system shall X"); return err }},
	"AddDecision":  {"decision_recorded", func(f *auditFixture) error { _, err := f.s.AddDecision("d2", DecisionOpts{}); return err }},
	"AddDependency": {"dependency_added", func(f *auditFixture) error {
		_, err := f.s.AddDependency(&f.task, nil, "npm", "left-pad", "1.0.0", false)
		return err
	}},
	"AddDependencyWithToken": {"dependency_added", func(f *auditFixture) error {
		_, err := f.s.AddDependencyWithToken(nil, &f.project, "npm", "right-pad", "1.0.0", true, "")
		return err
	}},
	"AddEval":    {"eval_recorded", func(f *auditFixture) error { _, err := f.s.AddEval(&f.task, nil, "suite", 0.9, 10, ""); return err }},
	"AddFeature": {"feature_recorded", func(f *auditFixture) error { _, err := f.s.AddFeature("f2", "", FeatureOpts{}); return err }},
	"AddLink":    {"link_added", func(f *auditFixture) error { _, err := f.s.AddLink(f.task, f.other, "related"); return err }},
	"AddMemory":  {"memory_recorded", func(f *auditFixture) error { _, err := f.s.AddMemory("", "pitfall", "p"); return err }},
	"AddMilestone": {"milestone_recorded", func(f *auditFixture) error {
		_, err := f.s.AddMilestone("m2", MilestoneOpts{})
		return err
	}},
	"AddNote":         {"note_recorded", func(f *auditFixture) error { _, err := f.s.AddNote(nil, "n", ""); return err }},
	"AddNoteWithRole": {"note_recorded", func(f *auditFixture) error { _, err := f.s.AddNoteWithRole(nil, nil, "n", ""); return err }},
	"AddProject":      {"project_added", func(f *auditFixture) error { _, err := f.s.AddProject("q", "", ""); return err }},
	"AddProjectWithToken": {"project_added", func(f *auditFixture) error {
		_, err := f.s.AddProjectWithToken("r", "", "", "")
		return err
	}},
	"AddProjectWithDefaults": {"project_added", func(f *auditFixture) error {
		_, err := f.s.AddProjectWithDefaults("u", "", "hitl", "high", "")
		return err
	}},
	"AddRole": {"role_added", func(f *auditFixture) error { _, err := f.s.AddRole(f.project, "x", "", false, nil, ""); return err }},
	"AddRoleWithToken": {"role_added", func(f *auditFixture) error {
		_, err := f.s.AddRoleWithToken(f.project, "y", "human", true, nil, "", "")
		return err
	}},
	"AddSpec":      {"spec_recorded", func(f *auditFixture) error { _, err := f.s.AddSpec("s2", ""); return err }},
	"AddTask":      {"task_created", func(f *auditFixture) error { _, err := f.s.AddTask("t2", "", "", TaskOpts{}); return err }},
	"ApprovePlan":  {"plan_approved", func(f *auditFixture) error { _, err := f.s.ApprovePlan(f.plan, ""); return err }},
	"ApproveSpec":  {"spec_recorded", func(f *auditFixture) error { return f.s.ApproveSpec(f.spec, "") }},
	"AssignRole":   {"role_assigned", func(f *auditFixture) error { _, err := f.s.AssignRole(f.task, &f.role); return err }},
	"BeginSession": {"session_started", func(f *auditFixture) error { _, err := f.s.BeginSession(SessionStart{TaskID: &f.task}); return err }},
	"StartSession": {"session_started", func(f *auditFixture) error { _, err := f.s.StartSession(nil, nil, nil, ""); return err }},
	"EndSession": {"session_ended", func(f *auditFixture) error {
		if _, err := f.s.BeginSession(SessionStart{}); err != nil {
			return err
		}
		_, err := f.s.EndSession("done", SessionCost{})
		return err
	}},
	"EndSessionWithToken": {"session_ended", func(f *auditFixture) error {
		if _, err := f.s.BeginSession(SessionStart{}); err != nil {
			return err
		}
		_, err := f.s.EndSessionWithToken("done", SessionCost{}, "")
		return err
	}},
	"EndLaunchedSession": {"session_ended", func(f *auditFixture) error {
		id, err := f.s.BeginSession(SessionStart{})
		if err != nil {
			return err
		}
		_, err = f.s.EndLaunchedSession(id, "done", SessionCost{})
		return err
	}},
	"CompleteTask": {"status_change", func(f *auditFixture) error {
		f.s.AddCheckWithMeta(f.task, nil, "test", "pass", "", "", CheckMeta{Source: CheckSourceRunner})
		_, err := f.s.CompleteTask(f.task, false, "")
		return err
	}},
	"CompleteTaskForTree": {"status_change", func(f *auditFixture) error {
		f.s.AddCheckWithMeta(f.other, nil, "test", "pass", "", "", CheckMeta{Source: CheckSourceRunner, TreeHash: "sha256:x"})
		_, err := f.s.CompleteTaskForTree(f.other, false, "", "sha256:x")
		return err
	}},
	"DeferTask": {"task_deferred", func(f *auditFixture) error { return f.s.DeferTask(f.task, true, "later", "") }},
	"EditPlanItem": {"plan_edited", func(f *auditFixture) error {
		title := "renamed"
		return f.s.EditPlanItem(f.plan, "a", PlanItemEdit{Title: &title}, "")
	}},
	"EnableApprovalToken": {"approval_token", func(f *auditFixture) error { _, err := f.s.EnableApprovalToken(); return err }},
	"RotateApprovalToken": {"approval_token", func(f *auditFixture) error {
		tok, err := f.s.EnableApprovalToken()
		if err != nil {
			return err
		}
		_, err = f.s.RotateApprovalToken(tok)
		return err
	}},
	"DisableApprovalToken": {"approval_token", func(f *auditFixture) error {
		tok, err := f.s.EnableApprovalToken()
		if err != nil {
			return err
		}
		return f.s.DisableApprovalToken(tok)
	}},
	"SealHead": {"head_sealed", func(f *auditFixture) error {
		tok, err := f.s.EnableApprovalToken()
		if err != nil {
			return err
		}
		_, err = f.s.SealHead(tok)
		return err
	}},
	"LoadSnapshot":          {"snapshot_imported", func(f *auditFixture) error { return loadOwnSnapshot(f, false) }},
	"LoadSnapshotWithToken": {"snapshot_imported", func(f *auditFixture) error { return loadOwnSnapshot(f, true) }},
	"LogEvent":              {"bug", func(f *auditFixture) error { _, err := f.s.LogEvent(nil, nil, "bug", "x"); return err }},
	"LogEventWithRole":      {"bug", func(f *auditFixture) error { _, err := f.s.LogEventWithRole(nil, nil, nil, "bug", "x"); return err }},
	"LogEventGlobal":        {"note", func(f *auditFixture) error { f.s.LogEventGlobal("note", "x"); return nil }},
	"LogTaskEvent":          {"note", func(f *auditFixture) error { f.s.LogTaskEvent(f.task, "note", "x"); return nil }},
	"MarkPromoted":          {"note_promoted", func(f *auditFixture) error { return f.s.MarkPromoted(f.note, "decision", f.decision) }},
	"PromoteAutonomy": {"autonomy_promoted", func(f *auditFixture) error {
		if _, err := f.s.AddEval(&f.task, nil, "suite", 0.99, 10, ""); err != nil {
			return err
		}
		return f.s.PromoteAutonomy(f.task, "auto", "suite", 0)
	}},
	"PromoteNote": {"note_promoted", func(f *auditFixture) error {
		_, err := f.s.PromoteNote(f.note, "memory", PromoteOpts{MemoryKind: "lesson"})
		return err
	}},
	"ProposePlan": {"plan_proposed", func(f *auditFixture) error {
		if err := f.s.RejectPlan(f.plan, ""); err != nil {
			return err
		}
		_, err := f.s.ProposePlan(f.spec, PlanInput{Items: []PlanItemInput{{Ref: "b", Title: "second"}}})
		return err
	}},
	"RevisePlan": {"plan_proposed", func(f *auditFixture) error {
		_, err := f.s.RevisePlan(f.plan, PlanInput{Items: []PlanItemInput{{Ref: "c", Title: "third"}}})
		return err
	}},
	"RejectPlan": {"plan_rejected", func(f *auditFixture) error { return f.s.RejectPlan(f.plan, "not now") }},
	"RecordApproval": {"approval", func(f *auditFixture) error {
		_, err := f.s.RecordApproval(ApprovalRequest{TaskID: f.task, Decision: "approved"})
		return err
	}},
	"RejectDecision": {"decision_recorded", func(f *auditFixture) error { return f.s.RejectDecision(f.decision, "") }},
	"SupersedeDecision": {"decision_recorded", func(f *auditFixture) error {
		next, err := f.s.AddDecision("next", DecisionOpts{})
		if err != nil {
			return err
		}
		return f.s.SupersedeDecision(f.decision, next, "")
	}},
	"DeprecateDecision": {"decision_recorded", func(f *auditFixture) error {
		if err := f.s.AcceptDecision(f.decision, ""); err != nil {
			return err
		}
		return f.s.DeprecateDecision(f.decision, "")
	}},
	"SupersedeSpec": {"spec_recorded", func(f *auditFixture) error {
		next, err := f.s.AddSpec("next", "")
		if err != nil {
			return err
		}
		return f.s.SupersedeSpec(f.spec, next, "")
	}},
	"Reseal":       {"legacy_attested", func(f *auditFixture) error { _, err := f.s.Reseal(""); return err }},
	"ReviewMemory": {"memory_reviewed", func(f *auditFixture) error { return f.s.ReviewMemory(f.memo, false, "") }},
	"ReviseSpec":   {"spec_revised", func(f *auditFixture) error { _, err := f.s.ReviseSpec(f.spec, "new body"); return err }},
	"SetCheckRunner": {"check_runner_set", func(f *auditFixture) error {
		return f.s.SetCheckRunner(f.project, "test", "go test ./...", "")
	}},
	"UnsetCheckRunner": {"check_runner_set", func(f *auditFixture) error {
		if err := f.s.SetCheckRunner(f.project, "lint", "go vet ./...", ""); err != nil {
			return err
		}
		return f.s.UnsetCheckRunner(f.project, "lint", "")
	}},
	"SetCriterionDone":        {"criterion_checked", func(f *auditFixture) error { return f.s.SetCriterionDone(f.criterion, true) }},
	"SetFeatureStatus":        {"feature_status_change", func(f *auditFixture) error { return f.s.SetFeatureStatus(f.feature, "deprecated") }},
	"SetMemoryStale":          {"memory_forgotten", func(f *auditFixture) error { return f.s.SetMemoryStale(f.memo, true) }},
	"SetMemoryStaleWithToken": {"memory_restored", func(f *auditFixture) error { return f.s.SetMemoryStaleWithToken(f.memo, false, "") }},
	"SetMilestoneStatus":      {"milestone_status_change", func(f *auditFixture) error { return f.s.SetMilestoneStatus(f.milestone, "active") }},
	"SetMilestoneTarget":      {"milestone_target_change", func(f *auditFixture) error { return f.s.SetMilestoneTarget(f.milestone, "2027-01-01") }},
	"SetTaskMilestone":        {"milestone_change", func(f *auditFixture) error { return f.s.SetTaskMilestone(f.task, &f.milestone) }},
	"SetTaskStatus":           {"status_change", func(f *auditFixture) error { return f.s.SetTaskStatus(f.task, "review") }},
	"SetTaskStatusWithReason": {"status_change", func(f *auditFixture) error { return f.s.SetTaskStatusWithReason(f.task, "blocked", "why") }},
	"TouchMemory":             {"memory_reconfirmed", func(f *auditFixture) error { return f.s.TouchMemory(f.memo) }},
	"TouchMemoryWithToken":    {"memory_reconfirmed", func(f *auditFixture) error { return f.s.TouchMemoryWithToken(f.memo, "") }},
	"UpdateMilestone":         {"milestone_status_change", func(f *auditFixture) error { return f.s.UpdateMilestone(f.milestone, "active", "") }},
	"UpdateTask": {"status_change", func(f *auditFixture) error {
		_, err := f.s.UpdateTask(f.task, TaskUpdate{Status: "review"})
		return err
	}},
	"UpdateTaskArea":          {"task_updated", func(f *auditFixture) error { return f.s.UpdateTaskArea(f.task, "cli") }},
	"UpdateTaskType":          {"task_updated", func(f *auditFixture) error { return f.s.UpdateTaskType(f.task, "bug") }},
	"UpdateTaskPriority":      {"task_updated", func(f *auditFixture) error { return f.s.UpdateTaskPriority(f.task, "high") }},
	"UpdateTaskRisk":          {"risk_changed", func(f *auditFixture) error { return f.s.UpdateTaskRisk(f.task, "high") }},
	"UpdateTaskRiskWithToken": {"risk_changed", func(f *auditFixture) error { return f.s.UpdateTaskRiskWithToken(f.task, "medium", "") }},
	"UpdateTaskAutonomy":      {"autonomy_changed", func(f *auditFixture) error { return f.s.UpdateTaskAutonomy(f.task, "hitl") }},
	"UpdateTaskAutonomyWithToken": {"autonomy_changed", func(f *auditFixture) error {
		return f.s.UpdateTaskAutonomyWithToken(f.task, "hitl", "")
	}},
	"VerifyDependency":          {"dependency_verified", func(f *auditFixture) error { return f.s.VerifyDependency(f.dep) }},
	"VerifyDependencyWithToken": {"dependency_verified", func(f *auditFixture) error { return f.s.VerifyDependencyWithToken(f.dep, "") }},
}

// loadOwnSnapshot exports the fixture's store and imports it back (a no-op merge
// by primary key that still has to be recorded).
func loadOwnSnapshot(f *auditFixture, withToken bool) error {
	var b strings.Builder
	if err := f.s.SnapshotJSON(&b); err != nil {
		return err
	}
	if withToken {
		_, err := f.s.LoadSnapshotWithToken(strings.NewReader(b.String()), "")
		return err
	}
	_, err := f.s.LoadSnapshot(strings.NewReader(b.String()))
	return err
}

// readOnly and exempt methods change nothing that needs an audit event.
var readOnlyStoreMethods = strings.Fields(`ApprovalTokenEnabled AuthorizeAdHocCheckCommand ChainHead CheckAnchor
	CheckApprovalToken CheckHeadSeal CheckRunnerCommand ComputeMetrics ComputeMetricsFor CountDecayCandidates
	CountDecayCandidatesIn CountDraftedMemory CountDraftedMemoryIn CountEventsAfter CountPendingMemory
	CountPendingMemoryIn CountSessionEventsAfter CurrentSession DecayCandidates DraftPlans EmbeddingsEnabled
	EvaluateGate EvaluateGateForTree ExportJSONL GetDecision GetMilestone GetMilestoneProgress GetNote GetPlan
	GetProjectByName GetRole GetRoleByName GetSpec GetTask SpecImplemented LatestEval LatestEventID ListApprovals
	ListCheckRunners ListChecks ListCriteria ListDecisions ListDependencies ListEvals ListEvents ListFeatures
	ListLinks ListMemory ListMilestones ListNotes ListPlans ListProjectPlans ListProjects ListRoles
	ListSessions ListSpecVersions ListSpecs ListTasks MilestoneTasks NextRoleHint NextTask OpenPrerequisites
	PlanIsStale PlanItems Prerequisites QueryEvents RecallForTask RenderContext RenderContextLimited
	ResolveCurrentProject ResolveProjectForPath ResolveRole RouteTask RowsMissingEmbeddings Search
	SemanticSearch SemanticSearchQuery SimilarMemory SnapshotJSON SpecsAwaitingPlan StaleSessions
	TasksForSpec VerifyChain VerifyRecords`)

var exemptStoreMethods = map[string]string{
	"Close":            "closes the connection; changes no data",
	"ReindexEmbedding": "rewrites a derived search vector, rebuilt from rows that are themselves audited",
	"CheckPolicy":      "a check, not a change; a denial is recorded as policy_violation",
}

func TestEveryStoreMethodIsClassified(t *testing.T) {
	known := map[string]bool{}
	for _, m := range readOnlyStoreMethods {
		known[m] = true
	}
	for m := range exemptStoreMethods {
		known[m] = true
	}
	for m := range auditedMutations {
		known[m] = true
	}
	typ := reflect.TypeOf(&Store{})
	for i := 0; i < typ.NumMethod(); i++ {
		if name := typ.Method(i).Name; !known[name] {
			t.Errorf("Store.%s is not classified: add it to auditedMutations (with the event it writes), readOnlyStoreMethods, or exemptStoreMethods (with why)", name)
		}
	}
	for name := range known {
		if _, ok := typ.MethodByName(name); !ok {
			t.Errorf("%s is classified but Store has no such method", name)
		}
	}
}

func TestEveryChangeWritesItsEvent(t *testing.T) {
	for name, m := range auditedMutations {
		t.Run(name, func(t *testing.T) {
			f := newAuditFixture(t)
			before, err := f.s.LatestEventID()
			if err != nil {
				t.Fatal(err)
			}
			if err := m.run(f); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if n, _ := f.s.CountEventsAfter(before, m.event); n == 0 {
				t.Errorf("%s wrote no %q event", name, m.event)
			}
			if c, err := f.s.VerifyChain(); err != nil || !c.OK() {
				t.Errorf("%s: chain %+v, %v", name, c, err)
			}
		})
	}
}
