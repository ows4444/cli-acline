package store

import (
	"strings"
	"testing"
)

// The gate could not tell a result acline ran from one somebody typed, or a
// result about today's code from one about code that has since changed.

const (
	treeA = "sha256:aaaa"
	treeB = "sha256:bbbb"
)

func runnerCheck(t *testing.T, s *Store, task int64, kind, status, tree string) {
	t.Helper()
	if _, err := s.AddCheckWithMeta(task, nil, kind, status, "ran: "+kind, "", CheckMeta{Source: CheckSourceRunner, TreeHash: tree}); err != nil {
		t.Fatal(err)
	}
}

func approve(t *testing.T, s *Store, task int64) {
	t.Helper()
	if _, err := s.AddApproval(task, "code_review", "bob", "approved", ""); err != nil {
		t.Fatal(err)
	}
}

func TestChecksRecordTheirSourceAndTree(t *testing.T) {
	h := humanStore(t)
	id, _ := h.AddTask("t", "", "normal", TaskOpts{})
	if _, err := h.AddCheck(id, "lint", "pass", "typed"); err != nil {
		t.Fatal(err)
	}
	runnerCheck(t, h, id, "test", "pass", treeA)
	checks, _ := h.ListChecks(id)
	if len(checks) != 2 {
		t.Fatal(checks)
	}
	if checks[0].Source != CheckSourceManual || checks[0].TreeHash.Valid {
		t.Errorf("a hand-recorded check = %+v, want source manual and no tree", checks[0])
	}
	if checks[1].Source != CheckSourceRunner || checks[1].TreeHash.String != treeA {
		t.Errorf("a runner check = %+v", checks[1])
	}
	if res, err := h.VerifyRecords(); err != nil || !res.OK() || res.Checked != 2 {
		t.Fatalf("seals: %+v, %v", res, err)
	}
}

func TestChangingASealedChecksSourceOrTreeIsDetected(t *testing.T) {
	for _, tc := range []struct{ name, update string }{
		{"manual to runner", `UPDATE checks SET source = 'runner' WHERE id = 1`},
		{"runner to manual", `UPDATE checks SET source = 'manual' WHERE id = 2`},
		{"tree swapped", `UPDATE checks SET tree_hash = 'sha256:forged' WHERE id = 2`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := humanStore(t)
			id, _ := h.AddTask("t", "", "normal", TaskOpts{})
			h.AddCheck(id, "lint", "pass", "typed")
			runnerCheck(t, h, id, "test", "pass", treeA)
			if _, err := h.DB.Exec(`DROP TRIGGER checks_no_update`); err != nil { // an attacker with file access
				t.Fatal(err)
			}
			if _, err := h.DB.Exec(tc.update); err != nil {
				t.Fatal(err)
			}
			if res, _ := h.VerifyRecords(); res.OK() {
				t.Fatalf("the edit went unnoticed: %+v", res)
			}
		})
	}
}

func TestHighRiskNeedsARunnerSourcedPass(t *testing.T) {
	h := humanStore(t)
	id, _ := h.AddTask("t", "", "normal", TaskOpts{Risk: "high"})
	if _, err := h.AddCheck(id, "test", "pass", "trust me"); err != nil {
		t.Fatal(err)
	}
	approve(t, h, id)
	g, _ := h.EvaluateGate(id)
	if g.OK() || !strings.Contains(strings.Join(g.Blockers, "\n"), "acline check run") {
		t.Fatalf("hand-recorded evidence alone satisfied a high-risk gate: %+v", g)
	}
	// A runner pass of another kind does not vouch for the typed test pass.
	runnerCheck(t, h, id, "lint", "pass", "")
	if g, _ := h.EvaluateGate(id); g.OK() {
		t.Fatalf("a lint runner pass carried a typed test pass: %+v", g)
	}
	runnerCheck(t, h, id, "test", "pass", "")
	if g, _ := h.EvaluateGate(id); !g.OK() {
		t.Fatalf("runner-sourced passes should satisfy it: %+v", g)
	}
}

func TestAHandRecordedPassCannotReplaceARunnersFailure(t *testing.T) {
	h := humanStore(t)
	id, _ := h.AddTask("t", "", "normal", TaskOpts{Risk: "critical"})
	runnerCheck(t, h, id, "test", "fail", treeA)
	if _, err := h.AddCheck(id, "test", "pass", "it works now, honest"); err != nil { // newest per kind wins
		t.Fatal(err)
	}
	approve(t, h, id)
	if g, _ := h.EvaluateGate(id); g.OK() {
		t.Fatalf("a typed pass over a runner failure satisfied the gate: %+v", g)
	}
}

func TestStaleRunnerPassBlocksHighRiskWhenTheTreeChanged(t *testing.T) {
	h := humanStore(t)
	id, _ := h.AddTask("t", "", "normal", TaskOpts{Risk: "high"})
	runnerCheck(t, h, id, "test", "pass", treeA)
	approve(t, h, id)

	if g, _ := h.EvaluateGateForTree(id, treeA); !g.OK() {
		t.Fatalf("same tree should pass: %+v", g)
	}
	g, _ := h.EvaluateGateForTree(id, treeB)
	if g.OK() || !strings.Contains(strings.Join(g.Blockers, "\n"), "changed since") {
		t.Fatalf("a pass for old code satisfied the gate: %+v", g)
	}
	if g, _ := h.EvaluateGateForTree(id, ""); !g.OK() {
		t.Fatalf("an unknown current tree cannot prove staleness: %+v", g)
	}
	// re-running against the new tree fixes it
	runnerCheck(t, h, id, "test", "pass", treeB)
	if g, _ := h.EvaluateGateForTree(id, treeB); !g.OK() {
		t.Fatalf("after re-running: %+v", g)
	}
}

func TestLowerRiskOnlyWarnsAboutStaleEvidence(t *testing.T) {
	h := humanStore(t)
	id, _ := h.AddTask("t", "", "normal", TaskOpts{Risk: "medium"})
	if _, err := h.AddCheck(id, "test", "pass", "a person ran it"); err != nil {
		t.Fatal(err)
	}
	if g, _ := h.EvaluateGate(id); !g.OK() {
		t.Fatalf("a person's typed pass at medium risk was blocked: %+v", g)
	}
	runnerCheck(t, h, id, "lint", "pass", treeA)
	g, _ := h.EvaluateGateForTree(id, treeB)
	if !g.OK() || !strings.Contains(strings.Join(g.Warnings, "\n"), "changed since") {
		t.Errorf("stale evidence should warn, not block, below high risk: %+v", g)
	}
}

// The agent whose work is being judged cannot vouch for it: its own typed pass
// of a kind acline can run never counts, at any risk. It runs the check instead.
func TestAnAgentsTypedPassNeverCounts(t *testing.T) {
	for _, risk := range []string{"low", "medium"} {
		h := humanStore(t)
		id, _ := h.AddTask("t", "", "normal", TaskOpts{Risk: risk})
		if _, err := asAgent(h).AddCheck(id, "test", "pass", "trust me"); err != nil {
			t.Fatal(err)
		}
		g, _ := h.EvaluateGateForTree(id, treeA)
		if g.OK() || !strings.Contains(strings.Join(g.Blockers, "\n"), "by hand") {
			t.Fatalf("risk=%s: an agent's typed pass completed the task: %+v", risk, g)
		}
		runnerCheck(t, asAgent(h), id, "test", "pass", treeA)
		if g, _ := h.EvaluateGateForTree(id, treeA); !g.OK() {
			t.Errorf("risk=%s: a pass acline ran did not clear it: %+v", risk, g)
		}
	}
}

func TestCompleteTaskForTreeAppliesTheStaleCheck(t *testing.T) {
	h := humanStore(t)
	id, _ := h.AddTask("t", "", "normal", TaskOpts{Risk: "high"})
	runnerCheck(t, h, id, "test", "pass", treeA)
	approve(t, h, id)
	if _, err := h.CompleteTaskForTree(id, false, "", treeB); err == nil {
		t.Fatal("completed against a tree the evidence does not describe")
	}
	if _, err := h.CompleteTaskForTree(id, false, "", treeA); err != nil {
		t.Fatalf("same tree: %v", err)
	}
}

func TestMigrationAddsCheckSourceAndTreeToAVersion11Store(t *testing.T) {
	path := t.TempDir() + "/old.db"
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Actor = Actor{Type: "human", ID: "t"}
	id, _ := s.AddTask("t", "", "normal", TaskOpts{})
	if _, err := s.AddCheck(id, "test", "pass", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`ALTER TABLE checks DROP COLUMN source; ALTER TABLE checks DROP COLUMN tree_hash; PRAGMA user_version = 11`); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s, err = Open(path)
	if err != nil {
		t.Fatalf("reopening a version-11 store: %v", err)
	}
	defer s.Close()
	checks, err := s.ListChecks(id)
	if err != nil || len(checks) != 1 || checks[0].Source != CheckSourceManual {
		t.Fatalf("after migrating: %+v, %v", checks, err)
	}
	if res, err := s.VerifyRecords(); err != nil || !res.OK() {
		t.Fatalf("a pre-existing sealed check must still verify after the migration: %+v, %v", res, err)
	}
}

// The newest result per kind used to win regardless of where it came from,
// so a typed "pass" erased a failure acline had actually observed.
func TestAnAgentsTypedPassCannotHideARunnerFailureAtAnyRisk(t *testing.T) {
	h := humanStore(t)
	id, _ := h.AddTask("t", "", "normal", TaskOpts{Risk: "medium"})
	runnerCheck(t, h, id, "test", "fail", treeA)
	if _, err := asAgent(h).AddCheck(id, "test", "pass", "fixed it, trust me"); err != nil {
		t.Fatal(err)
	}
	g, _ := h.EvaluateGate(id)
	if g.OK() || !strings.Contains(strings.Join(g.Blockers, "\n"), "cannot replace") {
		t.Fatalf("an agent's typed pass hid a runner failure: %+v", g)
	}
	// re-running the tool is the way out
	runnerCheck(t, h, id, "test", "pass", treeA)
	if g, _ := h.EvaluateGate(id); !g.OK() {
		t.Fatalf("after re-running: %+v", g)
	}
}

func TestAPersonsTypedPassOverARunnerFailureOnlyWarnsBelowHighRisk(t *testing.T) {
	h := humanStore(t)
	id, _ := h.AddTask("t", "", "normal", TaskOpts{Risk: "low"})
	runnerCheck(t, h, id, "test", "fail", treeA)
	if _, err := h.AddCheck(id, "test", "pass", "flaky; verified by hand"); err != nil {
		t.Fatal(err)
	}
	g, _ := h.EvaluateGate(id)
	if !g.OK() || !strings.Contains(strings.Join(g.Warnings, "\n"), "cannot replace") {
		t.Fatalf("a person's override should warn, not block: %+v", g)
	}
}

// H7 at high risk: one runner pass of any kind (sast) used to satisfy the
// "acline ran it" rule while the test result was typed.
func TestHighRiskNeedsEveryRunnablePassToComeFromTheRunner(t *testing.T) {
	h := humanStore(t)
	id, _ := h.AddTask("t", "", "normal", TaskOpts{Risk: "high"})
	runnerCheck(t, h, id, "sast", "pass", treeA)
	if _, err := asAgent(h).AddCheck(id, "test", "pass", "typed"); err != nil {
		t.Fatal(err)
	}
	approve(t, h, id)
	g, _ := h.EvaluateGateForTree(id, treeA)
	if g.OK() || !strings.Contains(strings.Join(g.Blockers, "\n"), "test check passed by hand") {
		t.Fatalf("a runner sast pass carried a typed test pass through a high-risk gate: %+v", g)
	}
	runnerCheck(t, h, id, "test", "pass", treeA)
	if g, _ := h.EvaluateGateForTree(id, treeA); !g.OK() {
		t.Fatalf("with both from the runner: %+v", g)
	}
}

// When the current tree could not be fingerprinted (too large, unreadable),
// "" used to mean "fine", so staleness detection silently switched off.
func TestAnUnfingerprintableTreeBlocksHighRiskAndWarnsBelow(t *testing.T) {
	for _, tc := range []struct {
		risk  string
		block bool
	}{{"high", true}, {"critical", true}, {"medium", false}, {"low", false}} {
		t.Run(tc.risk, func(t *testing.T) {
			h := humanStore(t)
			id, _ := h.AddTask("t", "", "normal", TaskOpts{Risk: tc.risk})
			runnerCheck(t, h, id, "test", "pass", treeA)
			approve(t, h, id)
			g, _ := h.EvaluateGateForTree(id, TreeUnavailable)
			all := strings.Join(append(g.Blockers, g.Warnings...), "\n")
			if !strings.Contains(all, "could not be fingerprinted") {
				t.Fatalf("an unknown tree went unmentioned: %+v", g)
			}
			if g.OK() == tc.block {
				t.Fatalf("risk=%s: blocked=%t, want %t: %+v", tc.risk, !g.OK(), tc.block, g)
			}
		})
	}
}

// A runner pass that recorded no tree cannot show it is about today's code.
func TestHighRiskRunnerPassWithNoTreeIsNotEvidenceForAKnownTree(t *testing.T) {
	h := humanStore(t)
	id, _ := h.AddTask("t", "", "normal", TaskOpts{Risk: "high"})
	runnerCheck(t, h, id, "test", "pass", "")
	approve(t, h, id)
	if g, _ := h.EvaluateGateForTree(id, treeA); g.OK() {
		t.Fatalf("a runner pass bound to no tree satisfied a high-risk gate for a known tree: %+v", g)
	}
	if g, _ := h.EvaluateGate(id); !g.OK() {
		t.Fatalf("a caller that does not know the tree cannot prove anything stale: %+v", g)
	}
}

// A high-risk task whose only results are skipped had nothing verified, yet the
// "needs a passing check acline ran" rule only fired when some check had passed,
// so an approval alone completed it.
func TestHighRiskWithOnlySkippedChecksIsBlocked(t *testing.T) {
	for _, tc := range []struct {
		name string
		add  func(s *Store, task int64)
	}{
		{"runner skipped", func(s *Store, task int64) { runnerCheck(t, s, task, "test", "skipped", treeA) }},
		{"typed skipped", func(s *Store, task int64) { s.AddCheck(task, "test", "skipped", "no tool") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := humanStore(t)
			id, _ := h.AddTask("t", "", "normal", TaskOpts{Risk: "high"})
			tc.add(h, id)
			approve(t, h, id)
			g, err := h.EvaluateGateForTree(id, treeA)
			if err != nil {
				t.Fatal(err)
			}
			if g.OK() {
				t.Fatalf("a high-risk task with nothing verified passed the gate: %+v", g)
			}
			if !strings.Contains(strings.Join(g.Blockers, "\n"), "every recorded check was skipped") {
				t.Errorf("blockers %q do not say nothing was verified", g.Blockers)
			}
		})
	}
}

// Skipped is never evidence. A task whose every
// newest result is skipped had nothing verified and blocks at every risk; one
// real pass beside a skip still counts.
func TestEveryRiskBlocksWhenEveryCheckWasSkipped(t *testing.T) {
	for _, risk := range []string{"low", "medium", "high", "critical"} {
		h := humanStore(t)
		id, _ := h.AddTask("t", "", "normal", TaskOpts{Risk: risk})
		runnerCheck(t, h, id, "test", "skipped", treeA)
		approve(t, h, id)
		g, err := h.EvaluateGateForTree(id, treeA)
		if err != nil {
			t.Fatal(err)
		}
		if g.OK() {
			t.Errorf("risk=%s: a task with only skipped checks passed the gate: %+v", risk, g)
		}
		if n := strings.Count(strings.Join(g.Blockers, "\n"), "skipped"); n != 1 {
			t.Errorf("risk=%s: want exactly one skipped blocker, got %q", risk, g.Blockers)
		}
	}
	h := humanStore(t)
	id, _ := h.AddTask("t", "", "normal", TaskOpts{Risk: "low"})
	runnerCheck(t, h, id, "test", "pass", treeA)
	runnerCheck(t, h, id, "sast", "skipped", treeA)
	if g, _ := h.EvaluateGateForTree(id, treeA); !g.OK() {
		t.Errorf("a real pass beside a skip was blocked: %+v", g)
	}
}

// "A passing check acline ran" let a lone lint (or sast/sca) pass carry a
// high-risk task: the tests themselves were never run. High and critical risk
// need a passing test check acline ran.
func TestHighRiskNeedsATestRunByAcline(t *testing.T) {
	for _, risk := range []string{"high", "critical"} {
		h := humanStore(t)
		id, _ := h.AddTask("t", "", "normal", TaskOpts{Risk: risk})
		runnerCheck(t, h, id, "lint", "pass", treeA)
		approve(t, h, id)
		g, _ := h.EvaluateGateForTree(id, treeA)
		if g.OK() || !strings.Contains(strings.Join(g.Blockers, "\n"), "test check") {
			t.Fatalf("risk=%s: a lint pass alone passed the gate: %+v", risk, g)
		}
		runnerCheck(t, h, id, "test", "pass", treeA)
		if g, _ := h.EvaluateGateForTree(id, treeA); !g.OK() {
			t.Errorf("risk=%s: lint + test runner passes still blocked: %+v", risk, g)
		}
	}
	// Below high risk a lint pass is still enough.
	h := humanStore(t)
	id, _ := h.AddTask("t", "", "normal", TaskOpts{Risk: "medium"})
	runnerCheck(t, h, id, "lint", "pass", treeA)
	if g, _ := h.EvaluateGateForTree(id, treeA); !g.OK() {
		t.Errorf("medium risk with a lint runner pass blocked: %+v", g)
	}
}
