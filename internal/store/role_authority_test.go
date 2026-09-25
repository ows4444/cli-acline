package store

import (
	"errors"
	"testing"
)

// A role gate that any agent can satisfy by minting a role, or by naming a
// human-only role, is attribution and nothing more (revision 1 of the review,
// G32). Both holes are closed here.

func TestAgentCannotCreateAnApprovingRole(t *testing.T) {
	h := humanStore(t)
	p, _ := h.AddProject("p", "/tmp/p", "hotl")
	a := asAgent(h)

	if _, err := a.AddRole(p, "boss", "human", true, nil, "mine now"); !errors.Is(err, ErrAgentCannotCreateApprovingRole) {
		t.Fatalf("agent creating a can_approve role = %v, want ErrAgentCannotCreateApprovingRole", err)
	}
	if roles, _ := h.ListRoles(&p); len(roles) != 7 {
		t.Fatalf("a refused role was created: %d roles", len(roles))
	}
	// ordinary roles are fine for an agent, and a person may create an approving one
	if _, err := a.AddRole(p, "helper", "agent", false, nil, ""); err != nil {
		t.Fatalf("agent creating a plain role = %v", err)
	}
	if _, err := h.AddRole(p, "boss", "human", true, nil, "approver"); err != nil {
		t.Fatalf("human creating a can_approve role = %v", err)
	}
}

func TestApprovingRoleCreationStillNeedsTheTokenWhenEnabled(t *testing.T) {
	h := humanStore(t)
	p, _ := h.AddProject("p", "/tmp/p", "hotl")
	tok, _ := h.EnableApprovalToken()
	if _, err := h.AddRole(p, "boss", "human", true, nil, ""); !errors.Is(err, ErrApprovalTokenRequired) {
		t.Fatalf("human without token = %v", err)
	}
	if _, err := asAgent(h).AddRoleWithToken(p, "boss", "human", true, nil, "", tok); err != nil {
		t.Fatalf("agent with token = %v", err)
	}
}

func TestAgentCannotActAsAHumanOnlyRole(t *testing.T) {
	h := humanStore(t)
	a := asAgent(h)

	if _, err := a.ResolveRole("manager", nil); !errors.Is(err, ErrAgentCannotUseHumanRole) {
		t.Fatalf("agent resolving the human-only manager role = %v, want ErrAgentCannotUseHumanRole", err)
	}
	t.Setenv("ACLINE_ROLE", "scrummaster")
	if _, err := a.ResolveRole("", nil); !errors.Is(err, ErrAgentCannotUseHumanRole) {
		t.Fatalf("agent via $ACLINE_ROLE = %v, want ErrAgentCannotUseHumanRole", err)
	}
	t.Setenv("ACLINE_ROLE", "")

	// roles meant for agents (or both) stay available to an agent; a person can be any role
	for _, name := range []string{"developer", "qa", "architect", "security", "designer"} {
		if id, err := a.ResolveRole(name, nil); err != nil || id == nil {
			t.Errorf("agent as %s = %v, %v", name, id, err)
		}
	}
	for _, name := range []string{"manager", "scrummaster", "developer", "architect"} {
		if id, err := h.ResolveRole(name, nil); err != nil || id == nil {
			t.Errorf("human as %s = %v, %v", name, id, err)
		}
	}
}

func TestAgentCannotRecordEvidenceAsAHumanRole(t *testing.T) {
	h := humanStore(t)
	id, _ := h.AddTask("t", "", "normal", TaskOpts{})
	a := asAgent(h)
	// resolving is where the refusal happens, so an agent never gets a role id for `manager`
	if roleID, err := a.ResolveRole("manager", nil); err == nil {
		t.Fatalf("agent got role id %v for a human-only role", roleID)
	}
	roleID, err := a.ResolveRole("qa", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.AddCheckWithRole(id, roleID, "test", "pass", ""); err != nil {
		t.Fatalf("recording as qa = %v", err)
	}
}
