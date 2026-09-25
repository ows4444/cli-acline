package store

import (
	"errors"
	"testing"
)

// The guard holds a session running as a read-only role (security, qa,
// architect, designer) to "no file writes". Ending the session used to need
// nothing, so an agent could end it and start again as developer.

func startRoleSession(t *testing.T, s *Store, role string) int64 {
	t.Helper()
	r, err := s.GetRoleByName(nil, role)
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.BeginSession(SessionStart{RoleID: &r.ID})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestAgentCannotEndAReadOnlyRoleSession(t *testing.T) {
	for _, role := range []string{"security", "qa", "architect", "designer"} {
		t.Run(role, func(t *testing.T) {
			h := humanStore(t)
			startRoleSession(t, h, role)
			if _, err := asAgent(h).EndSessionWithToken("done", SessionCost{}, ""); !errors.Is(err, ErrAgentCannotLeaveReadOnlyRole) {
				t.Fatalf("agent ending a %s session = %v, want ErrAgentCannotLeaveReadOnlyRole", role, err)
			}
			if _, err := h.CurrentSession(); err != nil {
				t.Fatalf("the session ended anyway: %v", err)
			}
			if _, err := h.EndSession("done", SessionCost{}); err != nil {
				t.Fatalf("a person ending it = %v", err)
			}
		})
	}
}

func TestAgentMayEndAWritableRoleSession(t *testing.T) {
	h := humanStore(t)
	startRoleSession(t, h, "developer")
	if _, err := asAgent(h).EndSession("done", SessionCost{}); err != nil {
		t.Fatalf("agent ending a developer session = %v", err)
	}
}

func TestReadOnlyRoleSessionEndsWithTheToken(t *testing.T) {
	h := humanStore(t)
	tok, err := h.EnableApprovalToken()
	if err != nil {
		t.Fatal(err)
	}
	startRoleSession(t, h, "security")
	if _, err := asAgent(h).EndSessionWithToken("done", SessionCost{}, tok); err != nil {
		t.Fatalf("agent with the token = %v", err)
	}
}

// The orchestrator launches read-only steps (plan, spec, research) as an agent
// and must still close the session it opened.
func TestTheLauncherEndsItsOwnReadOnlySession(t *testing.T) {
	h := humanStore(t)
	id := startRoleSession(t, h, "architect")
	if _, err := asAgent(h).EndLaunchedSession(id, "step finished", SessionCost{CostUSD: 0.1}); err != nil {
		t.Fatalf("EndLaunchedSession = %v", err)
	}
	if _, err := h.CurrentSession(); !errors.Is(err, ErrNoActiveSession) {
		t.Fatalf("session still active: %v", err)
	}
	if _, err := asAgent(h).EndLaunchedSession(id, "again", SessionCost{}); err == nil {
		t.Fatal("ended a session that was not active")
	}
}
