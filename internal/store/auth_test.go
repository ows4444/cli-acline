package store

import (
	"errors"
	"strings"
	"testing"
)

func TestApprovalTokenLifecycle(t *testing.T) {
	s := humanStore(t)
	if on, _ := s.ApprovalTokenEnabled(); on {
		t.Fatal("token must be off by default")
	}
	if err := s.CheckApprovalToken(""); err != nil {
		t.Fatalf("with no token enabled every check must pass, got %v", err)
	}
	token, err := s.EnableApprovalToken()
	if err != nil || !strings.HasPrefix(token, "acl_") || len(token) < 40 {
		t.Fatalf("EnableApprovalToken = %q, %v", token, err)
	}
	if _, err := s.EnableApprovalToken(); !errors.Is(err, ErrApprovalTokenAlreadyEnabled) {
		t.Errorf("second enable = %v, want ErrApprovalTokenAlreadyEnabled", err)
	}
	if err := s.CheckApprovalToken(""); !errors.Is(err, ErrApprovalTokenRequired) {
		t.Errorf("empty token = %v, want required", err)
	}
	if err := s.CheckApprovalToken("acl_wrong"); !errors.Is(err, ErrApprovalTokenInvalid) {
		t.Errorf("wrong token = %v, want invalid", err)
	}
	if err := s.CheckApprovalToken(token); err != nil {
		t.Errorf("right token = %v", err)
	}

	// the cleartext token is never stored
	var stored string
	if err := s.DB.QueryRow(`SELECT value FROM meta WHERE key = ?`, metaApprovalTokenHash).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == token || strings.Contains(stored, token) {
		t.Error("the token itself was stored")
	}

	// rotate needs the current token and invalidates it
	if _, err := s.RotateApprovalToken("acl_wrong"); err == nil {
		t.Error("rotate accepted a wrong token")
	}
	next, err := s.RotateApprovalToken(token)
	if err != nil || next == token {
		t.Fatalf("rotate = %q, %v", next, err)
	}
	if err := s.CheckApprovalToken(token); !errors.Is(err, ErrApprovalTokenInvalid) {
		t.Errorf("old token still valid after rotate: %v", err)
	}

	// disable needs the current token
	if err := s.DisableApprovalToken(token); err == nil {
		t.Error("disable accepted the rotated-away token")
	}
	if err := s.DisableApprovalToken(next); err != nil {
		t.Fatal(err)
	}
	if on, _ := s.ApprovalTokenEnabled(); on {
		t.Error("still enabled after disable")
	}
	// every change is audited, and the chain (which never contains the token) is intact
	if n := count(t, s, `SELECT COUNT(*) FROM events WHERE type='approval_token'`); n < 4 {
		t.Errorf("approval_token audit events = %d, want >= 4", n)
	}
	if r, err := s.VerifyChain(); err != nil || !r.OK() {
		t.Fatalf("chain: %+v, %v", r, err)
	}
}

// The scenario the token exists for: an agent that flips its own identity to
// "human" (the env var is all the legacy check looked at) is still refused.
func TestApprovalTokenStopsAnAgentThatSpoofsAHuman(t *testing.T) {
	s := humanStore(t) // == what an agent looks like after ACLINE_ACTOR_TYPE=human
	id, _ := s.AddTask("t", "", "normal", TaskOpts{Risk: "high"})
	token, _ := s.EnableApprovalToken()

	if _, err := s.RecordApproval(ApprovalRequest{TaskID: id, Decision: "approved"}); !errors.Is(err, ErrApprovalTokenRequired) {
		t.Fatalf("approve without token = %v, want ErrApprovalTokenRequired", err)
	}
	if _, err := s.RecordApproval(ApprovalRequest{TaskID: id, Decision: "approved", By: "alice", Token: "acl_guess"}); !errors.Is(err, ErrApprovalTokenInvalid) {
		t.Fatalf("approve with wrong token = %v", err)
	}
	if _, err := s.CompleteTask(id, true, ""); !errors.Is(err, ErrApprovalTokenRequired) {
		t.Fatalf("force without token = %v", err)
	}
	if got, _ := s.GetTask(id); got.Status == "done" {
		t.Fatal("task completed without a token")
	}
	if n := count(t, s, `SELECT COUNT(*) FROM approvals`); n != 0 {
		t.Fatalf("%d approval row(s) written by refused attempts", n)
	}

	if _, err := s.RecordApproval(ApprovalRequest{TaskID: id, Decision: "approved", Token: token}); err != nil {
		t.Fatalf("approve with the right token: %v", err)
	}
	runnerCheck(t, s, id, "test", "pass", "") // high risk needs a check acline ran
	if _, err := s.CompleteTask(id, false, ""); err != nil {
		t.Fatalf("a satisfied gate needs no token to complete: %v", err)
	}
}

func TestForceWithTokenOverridesButNoTokenNeededWhenGateIsSatisfied(t *testing.T) {
	s := humanStore(t)
	id, _ := s.AddTask("t", "", "normal", TaskOpts{Risk: "high"})
	token, _ := s.EnableApprovalToken()
	if res, err := s.CompleteTask(id, true, token); err != nil || !res.Overridden {
		t.Fatalf("force with token = %+v, %v", res, err)
	}
}

// Naming a person is not evidence that a person approved: the actor type is an
// environment variable the agent controls, so an agent that types `--by alice`
// has proved nothing. Without the token an agent cannot approve at all.
func TestAgentCannotApproveByNamingAPersonWithoutAToken(t *testing.T) {
	s := agentStore(t)
	id, _ := s.AddTask("t", "", "normal", TaskOpts{Risk: "high"})
	if _, err := s.RecordApproval(ApprovalRequest{TaskID: id, Decision: "approved"}); !errors.Is(err, ErrAgentCannotApprove) {
		t.Fatalf("agent approve with no --by = %v", err)
	}
	if _, err := s.RecordApproval(ApprovalRequest{TaskID: id, Decision: "approved", By: "alice"}); !errors.Is(err, ErrAgentCannotApprove) {
		t.Fatalf("agent approve naming a person = %v, want ErrAgentCannotApprove", err)
	}
	if approvals, _ := s.ListApprovals(id); len(approvals) != 0 {
		t.Fatalf("a refused approval was recorded: %+v", approvals)
	}
	// a person still approves without a token, until one is enabled
	h := *s
	h.Actor = Actor{Type: "human", ID: "alice"}
	if _, err := h.RecordApproval(ApprovalRequest{TaskID: id, Decision: "approved"}); err != nil {
		t.Fatalf("human approve = %v", err)
	}
	// rejecting is always allowed
	if _, err := s.RecordApproval(ApprovalRequest{TaskID: id, Decision: "rejected", Note: "no"}); err != nil {
		t.Fatalf("reject = %v", err)
	}
}

// A valid token lets a server whose actor is an agent (an editor-spawned one)
// act on a human's behalf; without it the agent is still refused.
func TestTokenLetsAnAgentTypedServerApproveOnBehalfOfAHuman(t *testing.T) {
	s := agentStore(t)
	token, _ := s.EnableApprovalToken()
	id, _ := s.AddMemory("cli", "lesson", "agent wrote this")
	if err := s.ReviewMemory(id, true, ""); !errors.Is(err, ErrApprovalTokenRequired) {
		t.Fatalf("no token = %v", err)
	}
	if err := s.ReviewMemory(id, true, token); err != nil {
		t.Fatalf("with token = %v", err)
	}
	// rejecting needs no token, but still not an agent
	id2, _ := s.AddMemory("cli", "lesson", "another")
	if err := s.ReviewMemory(id2, false, ""); !errors.Is(err, ErrAgentCannotReview) {
		t.Fatalf("agent reject = %v", err)
	}
}

func TestSnapshotNeverCarriesTheApprovalToken(t *testing.T) {
	src := humanStore(t)
	if _, err := src.EnableApprovalToken(); err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	if err := src.SnapshotJSON(&buf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), metaApprovalTokenHash) || strings.Contains(buf.String(), `"meta"`) {
		t.Error("snapshot leaked the approval-token table")
	}
	dst := humanStore(t)
	if _, err := dst.LoadSnapshot(strings.NewReader(buf.String())); err != nil {
		t.Fatal(err)
	}
	if on, _ := dst.ApprovalTokenEnabled(); on {
		t.Error("import enabled a token")
	}
}

func TestMigrationAddsMetaToAStoreAtVersion5(t *testing.T) {
	path := t.TempDir() + "/old.db"
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`DROP TABLE meta; PRAGMA user_version = 5`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if _, err := s2.EnableApprovalToken(); err != nil {
		t.Fatalf("meta not recreated by migration 6: %v", err)
	}
}

func TestCanApproveRolesNeedTheTokenWhenEnabled(t *testing.T) {
	s := humanStore(t)
	pid, _ := s.AddProject("p", "/tmp/p", "hotl")
	token, _ := s.EnableApprovalToken()

	// a role that can approve is as privileged as approving
	if _, err := s.AddRoleWithToken(pid, "signer", "human", true, nil, "", ""); !errors.Is(err, ErrApprovalTokenRequired) {
		t.Fatalf("can_approve role without token = %v", err)
	}
	if _, err := s.AddRole(pid, "signer", "human", true, nil, ""); !errors.Is(err, ErrApprovalTokenRequired) {
		t.Fatalf("AddRole must not be a way around the token: %v", err)
	}
	// ordinary roles are unaffected
	if _, err := s.AddRole(pid, "reviewer", "both", false, nil, ""); err != nil {
		t.Fatalf("non-approving role = %v", err)
	}
	if _, err := s.AddRoleWithToken(pid, "signer", "human", true, nil, "", token); err != nil {
		t.Fatalf("with token = %v", err)
	}
}

func TestSetFeatureStatusReportsMissingFeature(t *testing.T) {
	s := humanStore(t)
	if err := s.SetFeatureStatus(999, "removed"); err == nil {
		t.Fatal("updating a nonexistent feature silently succeeded")
	}
	id, _ := s.AddFeature("dash", "live", FeatureOpts{})
	if err := s.SetFeatureStatus(id, "deprecated"); err != nil {
		t.Fatal(err)
	}
}
