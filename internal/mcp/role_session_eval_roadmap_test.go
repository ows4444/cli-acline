package mcp

import (
	"testing"

	"acline/internal/store"
)

// TestRoleListIncludesNewGlobals is the MCP counterpart of the cmd-layer
// TestRoleAddAndList (internal/cmd/integration_test.go): confirms the read
// gap this file closes actually surfaces the 7 global roles -- including
// the two seeded by migration 5 (architect, security) -- over MCP, not just
// the CLI.
func TestRoleListIncludesNewGlobals(t *testing.T) {
	cs, _ := connectedTestServer(t)

	out := callTool[roleListOut](t, cs, "acline_role_list", roleListArgs{})
	if len(out.Roles) != 7 {
		t.Fatalf("expected 7 global roles, got %d: %+v", len(out.Roles), out.Roles)
	}
	seen := map[string]bool{}
	for _, r := range out.Roles {
		seen[r.Name] = true
	}
	for _, want := range []string{"designer", "developer", "qa", "manager", "scrummaster", "architect", "security"} {
		if !seen[want] {
			t.Fatalf("expected a global role named %q, got %+v", want, out.Roles)
		}
	}
}

// TestSessionCurrentReportsInactiveGracefully: no shell equivalent of
// store.ErrNoActiveSession should leak as a tool error -- a client polling
// "is anything running" should get a normal (active=false) result.
func TestSessionCurrentReportsInactiveGracefully(t *testing.T) {
	cs, _ := connectedTestServer(t)

	out := callTool[sessionCurrentOut](t, cs, "acline_session_current", struct{}{})
	if out.Active || out.Session != nil {
		t.Fatalf("expected no active session, got %+v", out)
	}
}

// TestSessionCurrentAndListReflectStartedSession exercises the started-session
// path end to end: acline_session_current reports it active, and
// acline_session_list surfaces it.
func TestSessionCurrentAndListReflectStartedSession(t *testing.T) {
	cs, st := connectedTestServer(t)

	sessID, err := st.StartSession(nil, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}

	cur := callTool[sessionCurrentOut](t, cs, "acline_session_current", struct{}{})
	if !cur.Active || cur.Session == nil || cur.Session.ID != sessID {
		t.Fatalf("expected active session #%d, got %+v", sessID, cur)
	}

	list := callTool[sessionListOut](t, cs, "acline_session_list", sessionListArgs{})
	found := false
	for _, s := range list.Sessions {
		if s.ID == sessID {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected session #%d in session_list, got %+v", sessID, list.Sessions)
	}
}

// TestEvalListSurfacesRecordedPassRate confirms the eval read gap is closed:
// a suite recorded via the store (the same path `acline eval record` uses)
// is visible over MCP with its pass rate intact.
func TestEvalListSurfacesRecordedPassRate(t *testing.T) {
	cs, st := connectedTestServer(t)

	if _, err := st.AddEval(nil, nil, "unit-suite", 0.97, 40, "nightly run"); err != nil {
		t.Fatal(err)
	}

	out := callTool[evalListOut](t, cs, "acline_eval_list", evalListArgs{Suite: "unit-suite"})
	if len(out.Evals) != 1 {
		t.Fatalf("expected 1 eval, got %d: %+v", len(out.Evals), out.Evals)
	}
	if out.Evals[0].Suite != "unit-suite" || out.Evals[0].PassRate != 0.97 {
		t.Fatalf("unexpected eval: %+v", out.Evals[0])
	}
}

// TestRoadmapListAndShowReportProgress confirms the milestone read gap is
// closed: a milestone with an assigned task reports the right progress
// counts through both acline_roadmap_list and acline_roadmap_show.
func TestRoadmapListAndShowReportProgress(t *testing.T) {
	cs, st := connectedTestServer(t)

	milestoneID, err := st.AddMilestone("v1 launch", store.MilestoneOpts{})
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := st.AddTask("ship it", "", "normal", store.TaskOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetTaskMilestone(taskID, &milestoneID); err != nil {
		t.Fatal(err)
	}

	list := callTool[roadmapListOut](t, cs, "acline_roadmap_list", roadmapListArgs{})
	var found *roadmapOut
	for i := range list.Milestones {
		if list.Milestones[i].ID == milestoneID {
			found = &list.Milestones[i]
		}
	}
	if found == nil || found.Total != 1 {
		t.Fatalf("expected milestone #%d with 1 task in roadmap_list, got %+v", milestoneID, list.Milestones)
	}

	show := callTool[roadmapShowOut](t, cs, "acline_roadmap_show", idArgs{ID: milestoneID})
	if show.Milestone.Total != 1 || len(show.Tasks) != 1 || show.Tasks[0].ID != taskID {
		t.Fatalf("expected roadmap_show to include task #%d, got %+v", taskID, show)
	}
}

func TestRoadmapUpdateTool(t *testing.T) {
	cs, st := connectedTestServer(t)
	milestoneID, err := st.AddMilestone("v1 launch", store.MilestoneOpts{})
	if err != nil {
		t.Fatal(err)
	}

	if r := callToolRaw(t, cs, "acline_roadmap_update", roadmapUpdateArgs{ID: milestoneID}); !r.IsError {
		t.Fatal("update with nothing to change should be refused")
	}

	callTool[roadmapUpdateOut](t, cs, "acline_roadmap_update", roadmapUpdateArgs{ID: milestoneID, Status: "active", Target: "2026-12-01"})
	m, err := st.GetMilestone(milestoneID)
	if err != nil {
		t.Fatal(err)
	}
	if m.Status != "active" || m.TargetDate.String != "2026-12-01" {
		t.Fatalf("milestone = %+v", m)
	}

	if r := callToolRaw(t, cs, "acline_roadmap_update", roadmapUpdateArgs{ID: milestoneID, Status: "nonsense"}); !r.IsError {
		t.Fatal("invalid status accepted")
	}
}
