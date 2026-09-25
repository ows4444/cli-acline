package store

import (
	"strings"
	"testing"
)

func routeOf(t *testing.T, s *Store, id int64) *Route {
	t.Helper()
	r, err := s.RouteTask(id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRouteFollowsTaskLifecycle(t *testing.T) {
	s := humanStore(t)
	id, _ := s.AddTask("t", "", "normal", TaskOpts{})
	if r := routeOf(t, s, id); r.Action != RouteDefineCriteria || r.Role == nil || r.Role.Name != "architect" {
		t.Fatalf("new task: %+v", r)
	}
	s.AddCriterion(id, "When X, the system shall Y")
	if r := routeOf(t, s, id); r.Action != RouteStartWork || r.Role.Name != "developer" || len(r.OpenCriteria) != 1 {
		t.Fatalf("with criteria: %+v", r)
	}
	updateTaskStatus(s.DB, id, "in_progress")
	if r := routeOf(t, s, id); r.Action != RouteVerify || r.Role.Name != "qa" {
		t.Fatalf("in progress: %+v", r)
	}
	s.AddCheck(id, "test", "fail", "boom")
	if r := routeOf(t, s, id); r.Action != RouteFixChecks || len(r.FailingChecks) != 1 || r.NeedsHuman {
		t.Fatalf("failing: %+v", r)
	}
	s.AddCheck(id, "test", "pass", "")
	if r := routeOf(t, s, id); r.Action != RouteComplete {
		t.Fatalf("passing: %+v", r)
	}
	s.CompleteTask(id, false, "")
	if r := routeOf(t, s, id); r.Action != RouteNone {
		t.Fatalf("done: %+v", r)
	}
}

func TestRouteHighRiskNeedsHumanApproval(t *testing.T) {
	s := humanStore(t)
	id, _ := s.AddTask("t", "", "normal", TaskOpts{Risk: "high"})
	updateTaskStatus(s.DB, id, "in_progress")
	s.AddCheck(id, "test", "pass", "")
	r := routeOf(t, s, id)
	if r.Action != RouteRequestApproval || !r.NeedsHuman || len(r.Blockers) == 0 {
		t.Fatalf("route = %+v", r)
	}
}

func TestRouteDraftSpecAndBlocked(t *testing.T) {
	s := humanStore(t)
	spec, _ := s.AddSpec("s", "body")
	id, _ := s.AddTask("t", "", "normal", TaskOpts{SpecID: &spec})
	if r := routeOf(t, s, id); r.Action != RouteApproveSpec || !r.NeedsHuman {
		t.Fatalf("draft spec: %+v", r)
	}
	updateTaskStatus(s.DB, id, "blocked")
	if r := routeOf(t, s, id); r.Action != RouteResolveBlocker || !r.NeedsHuman {
		t.Fatalf("blocked: %+v", r)
	}
}

func TestNextTaskPrefersAgentActionableByPriority(t *testing.T) {
	s := humanStore(t)
	if r, _ := s.NextTask(nil); r != nil {
		t.Fatalf("empty store: %+v", r)
	}
	blocked, _ := s.AddTask("blocked urgent", "", "urgent", TaskOpts{})
	updateTaskStatus(s.DB, blocked, "blocked")
	low, _ := s.AddTask("low", "", "low", TaskOpts{})
	normal, _ := s.AddTask("normal", "", "normal", TaskOpts{})
	r, _ := s.NextTask(nil)
	if r == nil || r.TaskID != normal {
		t.Fatalf("want normal #%d over urgent-but-blocked and low #%d, got %+v", normal, low, r)
	}
	updateTaskStatus(s.DB, normal, "cancelled")
	updateTaskStatus(s.DB, low, "cancelled")
	if r, _ := s.NextTask(nil); r == nil || r.TaskID != blocked || !r.NeedsHuman {
		t.Fatalf("fallback to waiting task: %+v", r)
	}
}

func TestRouteSkippedScanOnHighRiskGoesBackToVerify(t *testing.T) {
	s := humanStore(t)
	id, _ := s.AddTask("t", "", "normal", TaskOpts{Risk: "high"})
	updateTaskStatus(s.DB, id, "in_progress")
	s.AddCheck(id, "test", "pass", "")
	s.AddCheck(id, "sca", "skipped", "govulncheck is not installed")
	if r := routeOf(t, s, id); r.Action != RouteVerify || r.NeedsHuman || r.Role == nil || r.Role.Name != "security" {
		t.Fatalf("route = %+v", r)
	}
}

func TestBlockedReasonIsRecordedShownAndCleared(t *testing.T) {
	s := humanStore(t)
	id, _ := s.AddTask("t", "", "normal", TaskOpts{})
	if err := s.SetTaskStatusWithReason(id, "blocked", "  waiting on the API team  "); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetTask(id); got.BlockedReason.String != "waiting on the API team" {
		t.Fatalf("reason = %+v", got.BlockedReason)
	}
	if r := routeOf(t, s, id); r.Action != RouteResolveBlocker || !strings.Contains(r.Reason, "waiting on the API team") {
		t.Fatalf("route = %+v", r)
	}
	// A reason with any other status is dropped, and leaving blocked clears it.
	s.SetTaskStatusWithReason(id, "in_progress", "ignored")
	if got, _ := s.GetTask(id); got.BlockedReason.Valid {
		t.Fatalf("reason survived leaving blocked: %+v", got.BlockedReason)
	}
	// Blocked with no reason says so, and tells the user how to record one.
	s.SetTaskStatus(id, "blocked")
	if r := routeOf(t, s, id); !strings.Contains(r.Reason, "no recorded reason") {
		t.Fatalf("route = %+v", r)
	}
	// The reason is in the audit trail.
	s.SetTaskStatusWithReason(id, "blocked", "needs review")
	evs, _ := s.ListEvents(&id, 10)
	found := false
	for _, e := range evs {
		found = found || strings.Contains(e.Message, "status -> blocked: needs review")
	}
	if !found {
		t.Fatalf("no audit event with the reason: %+v", evs)
	}
}
