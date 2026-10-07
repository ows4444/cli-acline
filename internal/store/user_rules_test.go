package store

import (
	"errors"
	"strings"
	"testing"
)

// Two rules lived only in the adapters, so a caller that skipped them got past:
// which event types a user may log by hand (approval, override, row_seal... are
// written only by the action they record), and that a new task's spec,
// milestone and parent exist (MCP got a raw foreign-key error).

func TestALoggedEventMustBeAUserTypeOnARealTask(t *testing.T) {
	s := agentStore(t)
	for _, typ := range []string{"approval", "override", "row_seal", "status_change", "made_up"} {
		if _, err := s.LogEventWithRole(nil, nil, nil, typ, "forged"); !errors.Is(err, ErrNotAUserLogType) {
			t.Errorf("logging a %q event = %v, want ErrNotAUserLogType", typ, err)
		}
	}
	missing := int64(999)
	if _, err := s.LogEventWithRole(&missing, nil, nil, "bug", "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("logging against a missing task = %v, want ErrNotFound", err)
	}
	if _, err := s.LogEventWithRole(nil, nil, nil, "bug", "a real bug"); err != nil {
		t.Errorf("a user log type was refused: %v", err)
	}
}

func TestANewTasksReferencesMustExist(t *testing.T) {
	s := humanStore(t)
	missing := int64(999)
	for name, opts := range map[string]TaskOpts{
		"spec":      {SpecID: &missing},
		"milestone": {MilestoneID: &missing},
		"parent":    {ParentID: &missing},
		"project":   {ProjectID: &missing},
	} {
		_, err := s.AddTask("t", "", "normal", opts)
		if !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), name) {
			t.Errorf("a task with a missing %s = %v, want ErrNotFound naming it", name, err)
		}
	}
	if n := count(t, s, `SELECT COUNT(*) FROM tasks`); n != 0 {
		t.Errorf("%d task(s) written despite the errors", n)
	}
}
