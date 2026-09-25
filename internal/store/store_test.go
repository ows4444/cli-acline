package store

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func newTestStore(t *testing.T, actor Actor) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	s.Actor = actor
	t.Cleanup(func() { s.Close() })
	return s
}

func humanStore(t *testing.T) *Store {
	return newTestStore(t, Actor{Type: "human", ID: "tester"})
}

func agentStore(t *testing.T) *Store {
	return newTestStore(t, Actor{Type: "agent", ID: "test-agent", Model: "claude-sonnet-5"})
}

func TestDetectEARSPattern(t *testing.T) {
	cases := []struct {
		text string
		want string
	}{
		{"When the user submits, the form shall validate input", "event"},
		{"While uploading, the client shall show progress", "state"},
		{"If the token expires, then the api shall return 401", "unwanted"},
		{"Where premium is enabled, the app shall show reports", "optional"},
		{"The system shall export all records as JSONL", "ubiquitous"},
		{"export works", ""},
		{"make it fast", ""},
	}
	for _, c := range cases {
		if got := DetectEARSPattern(c.text); got != c.want {
			t.Errorf("DetectEARSPattern(%q) = %q, want %q", c.text, got, c.want)
		}
	}
}

func TestGateBlocksWithoutVerification(t *testing.T) {
	s := humanStore(t)
	id, err := s.AddTask("low risk task", "", "normal", TaskOpts{})
	if err != nil {
		t.Fatal(err)
	}

	gate, err := s.EvaluateGate(id)
	if err != nil {
		t.Fatal(err)
	}
	if gate.OK() {
		t.Fatal("gate should block a task with no recorded verification")
	}
	if !strings.Contains(strings.Join(gate.Blockers, " "), "no verification recorded") {
		t.Errorf("expected a verification blocker, got %v", gate.Blockers)
	}

	// A passing check satisfies a low-risk, hotl task.
	if _, err := s.AddCheck(id, "test", "pass", ""); err != nil {
		t.Fatal(err)
	}
	gate, err = s.EvaluateGate(id)
	if err != nil {
		t.Fatal(err)
	}
	if !gate.OK() {
		t.Errorf("gate should pass once verification is recorded, blockers: %v", gate.Blockers)
	}
}

func TestGateBlocksOnFailingCheck(t *testing.T) {
	s := humanStore(t)
	id, _ := s.AddTask("task", "", "normal", TaskOpts{})
	if _, err := s.AddCheck(id, "test", "pass", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddCheck(id, "sast", "fail", "1 finding"); err != nil {
		t.Fatal(err)
	}

	gate, err := s.EvaluateGate(id)
	if err != nil {
		t.Fatal(err)
	}
	if gate.OK() {
		t.Fatal("gate should block while a check is failing")
	}
	if !strings.Contains(strings.Join(gate.Blockers, " "), "failing") {
		t.Errorf("expected a failing-check blocker, got %v", gate.Blockers)
	}
}

func TestGateRequiresApprovalForHighRisk(t *testing.T) {
	for _, opts := range []TaskOpts{
		{Risk: "high"},
		{Risk: "critical"},
		{Autonomy: "hitl"},
	} {
		s := humanStore(t)
		id, err := s.AddTask("sensitive", "", "normal", opts)
		if err != nil {
			t.Fatal(err)
		}
		runnerCheck(t, s, id, "test", "pass", "") // high risk needs a check acline ran

		gate, err := s.EvaluateGate(id)
		if err != nil {
			t.Fatal(err)
		}
		if gate.OK() {
			t.Fatalf("gate should require approval for %+v", opts)
		}

		if _, err := s.AddApproval(id, "code_review", "reviewer", "approved", ""); err != nil {
			t.Fatal(err)
		}
		gate, err = s.EvaluateGate(id)
		if err != nil {
			t.Fatal(err)
		}
		if !gate.OK() {
			t.Errorf("gate should pass after approval for %+v, blockers: %v", opts, gate.Blockers)
		}
	}
}

func TestGateRejectionDoesNotCountAsApproval(t *testing.T) {
	s := humanStore(t)
	id, _ := s.AddTask("sensitive", "", "normal", TaskOpts{Risk: "high"})
	s.AddCheck(id, "test", "pass", "")
	if _, err := s.AddApproval(id, "code_review", "reviewer", "rejected", "needs work"); err != nil {
		t.Fatal(err)
	}

	gate, err := s.EvaluateGate(id)
	if err != nil {
		t.Fatal(err)
	}
	if gate.OK() {
		t.Error("a rejection must not satisfy the approval requirement")
	}
}

func TestUncheckedCriteriaWarnButDoNotBlock(t *testing.T) {
	s := humanStore(t)
	id, _ := s.AddTask("task", "", "normal", TaskOpts{})
	s.AddCheck(id, "test", "pass", "")
	if _, _, err := s.AddCriterion(id, "The system shall do the thing"); err != nil {
		t.Fatal(err)
	}

	gate, err := s.EvaluateGate(id)
	if err != nil {
		t.Fatal(err)
	}
	if !gate.OK() {
		t.Errorf("unchecked criteria should not block, blockers: %v", gate.Blockers)
	}
	if len(gate.Warnings) == 0 {
		t.Error("unchecked criteria should produce a warning")
	}
}

func TestEventsAreAppendOnly(t *testing.T) {
	s := humanStore(t)
	id, _ := s.AddTask("task", "", "normal", TaskOpts{})
	if _, err := s.LogEvent(&id, nil, "note", "original"); err != nil {
		t.Fatal(err)
	}

	if _, err := s.DB.Exec(`UPDATE events SET message = 'tampered' WHERE id = 1`); err == nil {
		t.Error("expected UPDATE on events to be rejected")
	}
	if _, err := s.DB.Exec(`DELETE FROM events WHERE id = 1`); err == nil {
		t.Error("expected DELETE on events to be rejected")
	}

	events, err := s.ListEvents(&id, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Message != "original" {
		t.Errorf("event log was altered: %+v", events)
	}
}

func TestApprovalsAndChecksAreAppendOnly(t *testing.T) {
	s := humanStore(t)
	id, _ := s.AddTask("task", "", "normal", TaskOpts{})
	s.AddApproval(id, "code_review", "reviewer", "approved", "")
	s.AddCheck(id, "test", "pass", "")

	if _, err := s.DB.Exec(`UPDATE approvals SET decision = 'rejected' WHERE id = 1`); err == nil {
		t.Error("expected UPDATE on approvals to be rejected")
	}
	if _, err := s.DB.Exec(`UPDATE checks SET status = 'fail' WHERE id = 1`); err == nil {
		t.Error("expected UPDATE on checks to be rejected")
	}
}

func TestEventsCarryActorAttribution(t *testing.T) {
	s := agentStore(t)
	id, _ := s.AddTask("agent task", "", "normal", TaskOpts{})
	if _, err := s.LogEvent(&id, nil, "note", "did a thing"); err != nil {
		t.Fatal(err)
	}

	events, err := s.ListEvents(&id, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	e := events[0]
	if e.ActorType.String != "agent" || e.ActorID.String != "test-agent" || e.Model.String != "claude-sonnet-5" {
		t.Errorf("attribution missing: type=%q id=%q model=%q", e.ActorType.String, e.ActorID.String, e.Model.String)
	}

	task, err := s.GetTask(id)
	if err != nil {
		t.Fatal(err)
	}
	if task.ActorType.String != "agent" {
		t.Errorf("task actor_type = %q, want agent", task.ActorType.String)
	}
}

func TestAgentMemoryNeedsReviewHumanMemoryDoesNot(t *testing.T) {
	agent := agentStore(t)
	if _, err := agent.AddMemory("storage", "pitfall", "agent-written"); err != nil {
		t.Fatal(err)
	}
	pending, err := agent.CountPendingMemory()
	if err != nil {
		t.Fatal(err)
	}
	if pending != 1 {
		t.Errorf("agent memory should be pending review, got %d pending", pending)
	}

	human := humanStore(t)
	if _, err := human.AddMemory("storage", "pitfall", "human-written"); err != nil {
		t.Fatal(err)
	}
	pending, err = human.CountPendingMemory()
	if err != nil {
		t.Fatal(err)
	}
	if pending != 0 {
		t.Errorf("human memory should be auto-approved, got %d pending", pending)
	}
}

func TestOperationsOnMissingIDsError(t *testing.T) {
	s := humanStore(t)
	if err := updateTaskStatus(s.DB, 999, "done"); err == nil {
		t.Error("expected error updating a non-existent task")
	}
	if err := s.DeferTask(999, true, "reason", ""); err == nil {
		t.Error("expected error deferring a non-existent task")
	}
	if _, err := s.GetTask(999); err == nil {
		t.Error("expected error getting a non-existent task")
	}
	if err := s.setSpecStatus(999, "approved"); err == nil {
		t.Error("expected error updating a non-existent spec")
	}
	if _, _, err := s.AddCriterion(999, "The system shall x"); err == nil {
		t.Error("expected error adding a criterion to a non-existent task")
	}
}

func TestInvalidEnumsRejected(t *testing.T) {
	s := humanStore(t)
	if _, err := s.AddTask("t", "", "nonsense", TaskOpts{}); err == nil {
		t.Error("expected invalid priority to be rejected")
	}
	if _, err := s.AddTask("t", "", "normal", TaskOpts{Risk: "nonsense"}); err == nil {
		t.Error("expected invalid risk to be rejected")
	}
	if _, err := s.AddTask("t", "", "normal", TaskOpts{Autonomy: "nonsense"}); err == nil {
		t.Error("expected invalid autonomy to be rejected")
	}
	id, _ := s.AddTask("t", "", "normal", TaskOpts{})
	if _, err := s.AddCheck(id, "nonsense", "pass", ""); err == nil {
		t.Error("expected invalid check kind to be rejected")
	}
	if _, err := s.AddCheck(id, "test", "nonsense", ""); err == nil {
		t.Error("expected invalid check status to be rejected")
	}
	if _, err := s.AddApproval(id, "code_review", "r", "nonsense", ""); err == nil {
		t.Error("expected invalid approval decision to be rejected")
	}
}

func TestReworkMetric(t *testing.T) {
	s := humanStore(t)
	id, _ := s.AddTask("task", "", "normal", TaskOpts{})
	updateTaskStatus(s.DB, id, "done")
	s.LogEvent(&id, nil, "status_change", "status -> done")

	m, err := s.ComputeMetrics()
	if err != nil {
		t.Fatal(err)
	}
	if m.Reworked != 0 {
		t.Errorf("a task still done is not rework, got %d", m.Reworked)
	}

	// Reopening it makes it rework.
	updateTaskStatus(s.DB, id, "in_progress")
	m, err = s.ComputeMetrics()
	if err != nil {
		t.Fatal(err)
	}
	if m.Reworked != 1 {
		t.Errorf("expected 1 reworked task, got %d", m.Reworked)
	}
}

func TestExportIncludesProvenance(t *testing.T) {
	s := agentStore(t)
	id, _ := s.AddTask("exported task", "", "normal", TaskOpts{})
	s.LogEvent(&id, nil, "note", "something happened")

	var buf bytes.Buffer
	n, err := s.ExportJSONL(&buf, "")
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("export produced no records")
	}
	out := buf.String()
	if !strings.Contains(out, `"actor_id":"test-agent"`) {
		t.Error("export is missing actor attribution")
	}
	if !strings.Contains(out, `"kind":"task"`) || !strings.Contains(out, `"kind":"event"`) {
		t.Error("export is missing expected record kinds")
	}
	// Text must not be emitted as base64-encoded bytes.
	if !strings.Contains(out, "exported task") {
		t.Error("export did not render text columns as strings")
	}
}

func TestHashChainVerifies(t *testing.T) {
	s := humanStore(t)
	id, _ := s.AddTask("task", "", "normal", TaskOpts{})
	for i := 0; i < 5; i++ {
		if _, err := s.LogEvent(&id, nil, "note", "event"); err != nil {
			t.Fatal(err)
		}
	}

	r, err := s.VerifyChain()
	if err != nil {
		t.Fatal(err)
	}
	if !r.OK() {
		t.Errorf("fresh chain should verify, got failure at #%d: %s", r.BadID, r.Reason)
	}
	if r.Checked == 0 {
		t.Error("expected events to be checked")
	}
}

// Tampering has to go around the triggers to be interesting, so this drops
// them first — exactly what someone editing the database directly would do.
func TestHashChainDetectsTampering(t *testing.T) {
	s := humanStore(t)
	id, _ := s.AddTask("task", "", "normal", TaskOpts{})
	for i := 0; i < 3; i++ {
		s.LogEvent(&id, nil, "note", "original")
	}

	if _, err := s.DB.Exec(`DROP TRIGGER events_no_update`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE events SET message = 'tampered' WHERE id = 2`); err != nil {
		t.Fatal(err)
	}

	r, err := s.VerifyChain()
	if err != nil {
		t.Fatal(err)
	}
	if r.OK() {
		t.Fatal("expected tampering to be detected")
	}
	if r.BadID != 2 {
		t.Errorf("expected failure at event #2, got #%d (%s)", r.BadID, r.Reason)
	}
}

func TestHashChainDetectsDeletion(t *testing.T) {
	s := humanStore(t)
	id, _ := s.AddTask("task", "", "normal", TaskOpts{})
	for i := 0; i < 4; i++ {
		s.LogEvent(&id, nil, "note", "original")
	}

	if _, err := s.DB.Exec(`DROP TRIGGER events_no_delete`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`DELETE FROM events WHERE id = 2`); err != nil {
		t.Fatal(err)
	}

	r, err := s.VerifyChain()
	if err != nil {
		t.Fatal(err)
	}
	if r.OK() {
		t.Fatal("expected a removed event to break the chain")
	}
	if r.BadID != 3 {
		t.Errorf("expected the break to surface at event #3, got #%d", r.BadID)
	}
}

func TestPromotionRequiresPassingEval(t *testing.T) {
	s := humanStore(t)
	id, _ := s.AddTask("task", "", "normal", TaskOpts{Autonomy: "hitl"})

	// No eval at all.
	if err := s.PromoteAutonomy(id, "auto", "refactor-suite", 0); err == nil {
		t.Error("expected promotion without any eval to fail")
	}

	// Eval below threshold.
	if _, err := s.AddEval(&id, nil, "refactor-suite", 0.72, 50, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.PromoteAutonomy(id, "auto", "refactor-suite", 0); err == nil {
		t.Error("expected promotion below threshold to fail")
	}
	task, _ := s.GetTask(id)
	if task.Autonomy != "hitl" {
		t.Errorf("autonomy changed despite failed promotion: %s", task.Autonomy)
	}

	// Eval above threshold.
	if _, err := s.AddEval(&id, nil, "refactor-suite", 0.96, 50, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.PromoteAutonomy(id, "auto", "refactor-suite", 0); err != nil {
		t.Fatalf("expected promotion to succeed: %v", err)
	}
	task, _ = s.GetTask(id)
	if task.Autonomy != "auto" {
		t.Errorf("autonomy = %s, want auto", task.Autonomy)
	}
}

func TestEvalRejectsOutOfRangePassRate(t *testing.T) {
	s := humanStore(t)
	if _, err := s.AddEval(nil, nil, "suite", 1.5, 0, ""); err == nil {
		t.Error("expected pass rate above 1 to be rejected")
	}
	if _, err := s.AddEval(nil, nil, "suite", -0.1, 0, ""); err == nil {
		t.Error("expected negative pass rate to be rejected")
	}
	if _, err := s.AddEval(nil, nil, "", 0.9, 0, ""); err == nil {
		t.Error("expected empty suite name to be rejected")
	}
}

func TestRenderContextIncludesApprovedContentOnly(t *testing.T) {
	s := humanStore(t)

	s.AddMemory("storage", "constraint", "never rewrite the audit log")

	// Same database, agent actor: this entry stays pending review.
	human := s.Actor
	s.Actor = Actor{Type: "agent", ID: "a"}
	s.AddMemory("storage", "lesson", "unreviewed agent claim")
	s.Actor = human

	specID, _ := s.AddSpec("Shipped spec", "The system shall do the thing.")
	s.setSpecStatus(specID, "approved")
	draftID, _ := s.AddSpec("Draft spec", "not ready")
	_ = draftID

	dID, _ := s.AddDecision("Use SQLite", DecisionOpts{Decision: "SQLite it is", Rationale: "local, zero-dep"})
	s.setDecisionStatus(dID, "accepted")
	rejected, _ := s.AddDecision("Use Postgres", DecisionOpts{})
	s.setDecisionStatus(rejected, "rejected")

	var buf bytes.Buffer
	if err := s.RenderContext(&buf, "Test Context", nil); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	for _, want := range []string{"Test Context", "never rewrite the audit log", "Shipped spec", "Use SQLite"} {
		if !strings.Contains(out, want) {
			t.Errorf("context is missing %q", want)
		}
	}
	for _, unwanted := range []string{"unreviewed agent claim", "Draft spec", "Use Postgres"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("context should not include unapproved content %q", unwanted)
		}
	}
}

func TestSessionCostRecorded(t *testing.T) {
	s := humanStore(t)
	if _, err := s.StartSession(nil, nil, nil, "read-only"); err != nil {
		t.Fatal(err)
	}
	sess, err := s.EndSession("done", SessionCost{TokensIn: 1200, TokensOut: 340, CostUSD: 0.42})
	if err != nil {
		t.Fatal(err)
	}
	if sess.TokensIn != 1200 || sess.TokensOut != 340 || sess.CostUSD != 0.42 {
		t.Errorf("cost not recorded: %+v", sess)
	}

	m, err := s.ComputeMetrics()
	if err != nil {
		t.Fatal(err)
	}
	if m.TokensIn != 1200 || m.CostUSD != 0.42 {
		t.Errorf("metrics missing cost: in=%d cost=%v", m.TokensIn, m.CostUSD)
	}
}

func TestPolicyCheckDenyToolTakesPrecedence(t *testing.T) {
	p := Policy{AllowTools: []string{"*"}, DenyTools: []string{"network"}}
	if allowed, _ := p.Check("read_file", ""); !allowed {
		t.Error("read_file should be allowed under a wildcard allow-list")
	}
	if allowed, reason := p.Check("network", ""); allowed {
		t.Error("network should be denied despite the wildcard allow-list")
	} else if reason == "" {
		t.Error("expected a denial reason")
	}
}

func TestPolicyCheckAllowListExcludesUnlisted(t *testing.T) {
	p := Policy{AllowTools: []string{"read_file", "grep"}}
	if allowed, _ := p.Check("read_file", ""); !allowed {
		t.Error("read_file is in the allow-list")
	}
	if allowed, _ := p.Check("write_file", ""); allowed {
		t.Error("write_file is not in the allow-list and should be denied")
	}
}

func TestPolicyCheckPathGlobs(t *testing.T) {
	p := Policy{AllowPaths: []string{"internal/*"}, DenyPaths: []string{"internal/secrets/*"}}
	if allowed, _ := p.Check("write_file", "internal/store/x.go"); !allowed {
		t.Error("internal/store/x.go should match the allow glob")
	}
	if allowed, _ := p.Check("write_file", "internal/secrets/key.go"); allowed {
		t.Error("internal/secrets/key.go should be denied despite matching the allow glob too")
	}
	if allowed, _ := p.Check("write_file", "cmd/main.go"); allowed {
		t.Error("cmd/main.go does not match any allow pattern")
	}
}

func TestPolicyEmptyIsPermissive(t *testing.T) {
	var p Policy
	if allowed, _ := p.Check("anything", "any/path.go"); !allowed {
		t.Error("an empty policy should permit everything")
	}
}

func TestParsePolicyLegacyFreeTextIsPermissive(t *testing.T) {
	p := ParsePolicy("read-only exploration")
	if p.Label != "read-only exploration" {
		t.Errorf("expected legacy text preserved as label, got %q", p.Label)
	}
	if allowed, _ := p.Check("write_file", ""); !allowed {
		t.Error("legacy free-text policy should remain permissive")
	}
}

func TestCheckPolicyLogsViolationOnDenial(t *testing.T) {
	s := humanStore(t)
	policy := Policy{DenyTools: []string{"network"}}
	pj, _ := policy.JSON()
	sessID, err := s.StartSession(nil, nil, nil, pj)
	if err != nil {
		t.Fatal(err)
	}

	allowed, _, err := s.CheckPolicy("network", "")
	if err != nil {
		t.Fatal(err)
	}
	if allowed {
		t.Fatal("network should have been denied")
	}

	events, err := s.QueryEvents(EventFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range events {
		if e.Type == "policy_violation" && e.SessionID.Int64 == sessID {
			found = true
		}
	}
	if !found {
		t.Error("expected a policy_violation event to be recorded")
	}

	m, err := s.ComputeMetrics()
	if err != nil {
		t.Fatal(err)
	}
	if m.PolicyViolations != 1 {
		t.Errorf("expected 1 policy violation in metrics, got %d", m.PolicyViolations)
	}
}

func TestCheckPolicyAllowsDoNotLogViolations(t *testing.T) {
	s := humanStore(t)
	if _, err := s.StartSession(nil, nil, nil, ""); err != nil {
		t.Fatal(err)
	}
	allowed, _, err := s.CheckPolicy("read_file", "any/path.go")
	if err != nil {
		t.Fatal(err)
	}
	if !allowed {
		t.Fatal("unrestricted session should allow everything")
	}
	m, err := s.ComputeMetrics()
	if err != nil {
		t.Fatal(err)
	}
	if m.PolicyViolations != 0 {
		t.Errorf("expected no violations recorded, got %d", m.PolicyViolations)
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	src := agentStore(t)
	taskID, err := src.AddTask("snapshot me", "desc", "high", TaskOpts{Risk: "high", Autonomy: "hitl"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := src.LogEvent(&taskID, nil, "note", "before snapshot"); err != nil {
		t.Fatal(err)
	}
	if _, err := src.AddCheck(taskID, "test", "pass", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := src.AddApproval(taskID, "code_review", "reviewer", "approved", ""); err != nil {
		t.Fatal(err)
	}
	specID, err := src.AddSpec("spec title", "spec body")
	if err != nil {
		t.Fatal(err)
	}
	if err := src.setSpecStatus(specID, "approved"); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := src.SnapshotJSON(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "snapshot me") {
		t.Fatal("snapshot did not include the task")
	}

	dst := humanStore(t)
	counts, err := dst.LoadSnapshot(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if counts["tasks"] != 1 || counts["specs"] != 1 || counts["events"] != 6 || // the hash_version marker, the logged event, the spec approval, the seal watermark, and a row_seal each for the approval and the check
		counts["checks"] != 1 || counts["approvals"] != 1 {
		t.Errorf("unexpected import counts: %+v", counts)
	}

	got, err := dst.GetTask(taskID)
	if err != nil {
		t.Fatalf("task not present after import: %v", err)
	}
	if got.Title != "snapshot me" || got.Risk != "high" || got.Autonomy != "hitl" {
		t.Errorf("imported task mismatch: %+v", got)
	}
	if got.ActorType.String != "agent" || got.ActorID.String != "test-agent" {
		t.Errorf("provenance lost on import: actor_type=%q actor_id=%q", got.ActorType.String, got.ActorID.String)
	}

	sp, err := dst.GetSpec(specID)
	if err != nil {
		t.Fatalf("spec not present after import: %v", err)
	}
	if sp.Status != "approved" {
		t.Errorf("spec status = %q, want approved", sp.Status)
	}

	r, err := dst.VerifyChain()
	if err != nil {
		t.Fatal(err)
	}
	if !r.OK() {
		t.Errorf("imported event chain should still verify (hash was preserved), got failure at #%d: %s", r.BadID, r.Reason)
	}
}

func TestSnapshotImportIsIdempotent(t *testing.T) {
	src := humanStore(t)
	if _, err := src.AddTask("idempotent", "", "normal", TaskOpts{}); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := src.SnapshotJSON(&buf); err != nil {
		t.Fatal(err)
	}

	dst := humanStore(t)
	first, err := dst.LoadSnapshot(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if first["tasks"] != 1 {
		t.Fatalf("expected 1 task imported first time, got %d", first["tasks"])
	}

	second, err := dst.LoadSnapshot(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 0 {
		t.Errorf("re-importing the same snapshot should add nothing, got %+v", second)
	}

	tasks, err := dst.ListTasks(TaskFilter{All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Errorf("expected exactly 1 task after two imports, got %d", len(tasks))
	}
}

func TestSnapshotImportRejectsNewerVersion(t *testing.T) {
	s := humanStore(t)
	future := `{"version": 999, "exported_at": "now", "tables": {}}`
	if _, err := s.LoadSnapshot(strings.NewReader(future)); err == nil {
		t.Error("expected a future snapshot version to be rejected")
	}
}

func TestSupersedeRequiresExistingReplacement(t *testing.T) {
	s := humanStore(t)
	id, err := s.AddDecision("original", DecisionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SupersedeDecision(id, 999, ""); err == nil {
		t.Error("expected superseding by a non-existent decision to fail")
	}

	newID, _ := s.AddDecision("replacement", DecisionOpts{})
	if err := s.SupersedeDecision(id, newID, ""); err != nil {
		t.Fatal(err)
	}
	d, err := s.GetDecision(id)
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != "superseded" || d.SupersededBy.Int64 != newID {
		t.Errorf("supersession not recorded: status=%s by=%v", d.Status, d.SupersededBy)
	}
}
