package mcp

import (
	"strings"
	"testing"

	"acline/internal/store"
)

func proposeOver(t *testing.T, st *store.Store) (spec int64, plan int64) {
	t.Helper()
	spec, _ = st.AddSpec("Inventory", "Track stock.")
	asPerson(st).ApproveSpec(spec, "")
	plan, err := st.ProposePlan(spec, store.PlanInput{Items: []store.PlanItemInput{
		{Ref: "T1", Title: "Schema"}, {Ref: "T2", Title: "API", DependsOn: []string{"T1"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return spec, plan
}

func TestPlanDecisionsOverMCPNeedAnEnabledApprovalToken(t *testing.T) {
	cs, st := connectedTestServer(t) // the server's actor is human, as an editor's default
	_, plan := proposeOver(t, st)

	// No token enabled: approving or editing over MCP is refused outright, even for a human actor.
	yes := true
	if !refused(cs, "acline_plan_approve", planApproveArgs{ID: plan}) {
		t.Fatal("approve went through with no approval token enabled")
	}
	if !refused(cs, "acline_plan_edit_item", planEditItemArgs{ID: plan, Ref: "T1", Drop: &yes}) {
		t.Fatal("edit went through with no approval token enabled")
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM tasks`); n != 0 {
		t.Fatalf("%d task(s) exist", n)
	}
	if r := callToolRaw(t, cs, "acline_plan_approve", planApproveArgs{ID: plan}); !r.IsError {
		// callToolRaw fails the test on transport errors; a tool error is what we expect here
		t.Fatal("expected an error result")
	}
}

func TestPlanApproveOverMCPWithTheToken(t *testing.T) {
	cs, st := connectedTestServer(t)
	_, plan := proposeOver(t, st)
	tok, err := st.EnableApprovalToken()
	if err != nil {
		t.Fatal(err)
	}
	if !refused(cs, "acline_plan_approve", planApproveArgs{ID: plan}) {
		t.Error("approved without presenting the token")
	}
	if !refused(cs, "acline_plan_approve", planApproveArgs{ID: plan, Token: "wrong"}) {
		t.Error("approved with the wrong token")
	}
	title := "API v2"
	no := false
	yes := true
	callTool[planItemEditOut](t, cs, "acline_plan_edit_item", planEditItemArgs{ID: plan, Ref: "T2", Title: &title, Drop: &no, Token: tok})
	if !refused(cs, "acline_plan_edit_item", planEditItemArgs{ID: plan, Ref: "T2", Drop: &yes}) {
		t.Error("edited without the token")
	}
	out := callTool[planApproveOut](t, cs, "acline_plan_approve", planApproveArgs{ID: plan, Token: tok})
	if len(out.Created) != 2 || out.Created[0].Ref != "T1" || out.Created[1].TaskID == 0 {
		t.Fatalf("approve = %+v", out)
	}
	if got, _ := st.GetTask(out.Created[1].TaskID); got.Title != "API v2" {
		t.Errorf("the edit was not applied: %+v", got)
	}
	shown := callTool[planShowOut](t, cs, "acline_plan_show", planShowArgs{ID: plan})
	if shown.Plan.Status != "approved" || shown.Items[0].TaskID == nil {
		t.Errorf("show = %+v", shown)
	}
}

func TestAnAgentActorCannotDecideAPlanOverMCPEvenWithTheTokenUnset(t *testing.T) {
	cs, st := connectedTestServerWithActor(t, store.Actor{Type: "agent", ID: "planner"})
	_, plan := proposeOver(t, st)
	tok, _ := st.EnableApprovalToken()
	_ = tok // the agent was never given it
	if !refused(cs, "acline_plan_approve", planApproveArgs{ID: plan}) {
		t.Error("an agent approved a plan without the token")
	}
	if !refused(cs, "acline_plan_reject", planRejectArgs{ID: plan, Note: "no"}) {
		t.Error("an agent rejected a plan")
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM tasks`); n != 0 {
		t.Errorf("%d task(s) exist", n)
	}
}

func TestPlanRejectOverMCPNeedsNoTokenAndKeepsTheNote(t *testing.T) {
	cs, st := connectedTestServer(t)
	_, plan := proposeOver(t, st)
	st.EnableApprovalToken() // a token being enabled does not gate rejecting
	callTool[planRejectOut](t, cs, "acline_plan_reject", planRejectArgs{ID: plan, Note: "too coarse"})
	p, _ := st.GetPlan(plan)
	if p.Status != "rejected" {
		t.Fatalf("status = %s", p.Status)
	}
	notes, _ := st.ListNotes(store.NoteFilter{UnpromotedOnly: true})
	if len(notes) != 1 || !strings.Contains(notes[0].Body, "too coarse") {
		t.Errorf("notes = %+v", notes)
	}
	if !refused(cs, "acline_plan_reject", planRejectArgs{ID: plan}) {
		t.Error("rejected an already-rejected plan")
	}
}
