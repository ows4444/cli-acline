package mcp

import (
	"fmt"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"acline/internal/store"
)

func errorCode(t *testing.T, r *sdkmcp.CallToolResult) string {
	t.Helper()
	if r.Meta == nil {
		return ""
	}
	code, _ := r.Meta["error_code"].(string)
	return code
}

func TestErrorCodeGateBlocked(t *testing.T) {
	cs, st := connectedTestServer(t)
	id, _ := st.AddTask("t", "", "normal", store.TaskOpts{Risk: "high"})

	r := callToolRaw(t, cs, "acline_task_done", taskDoneArgs{ID: id})
	if !r.IsError {
		t.Fatal("expected an error (unsatisfied gate)")
	}
	if got := errorCode(t, r); got != "gate_blocked" {
		t.Errorf("error_code = %q, want gate_blocked", got)
	}
}

func TestErrorCodeAgentCannotApprove(t *testing.T) {
	cs, st := connectedTestServerWithActor(t, store.Actor{Type: "agent", ID: "claude"})
	id, _ := st.AddTask("t", "", "normal", store.TaskOpts{})

	r := callToolRaw(t, cs, "acline_approve", approveArgs{ID: id})
	if !r.IsError {
		t.Fatal("expected an error (agent cannot approve without --by)")
	}
	if got := errorCode(t, r); got != "agent_cannot_approve" {
		t.Errorf("error_code = %q, want agent_cannot_approve", got)
	}
}

func TestErrorCodeAgentCannotReview(t *testing.T) {
	cs, st := connectedTestServerWithActor(t, store.Actor{Type: "agent", ID: "claude"})
	id, err := st.AddMemory("", "lesson", "a lesson")
	if err != nil {
		t.Fatal(err)
	}

	r := callToolRaw(t, cs, "acline_memory_approve", memoryReviewArgs{ID: id})
	if !r.IsError {
		t.Fatal("expected an error (agent cannot review memory)")
	}
	if got := errorCode(t, r); got != "agent_cannot_review" {
		t.Errorf("error_code = %q, want agent_cannot_review", got)
	}
}

func TestErrorCodeStatusNeedsGate(t *testing.T) {
	cs, st := connectedTestServer(t)
	id, _ := st.AddTask("t", "", "normal", store.TaskOpts{})

	r := callToolRaw(t, cs, "acline_task_set_status", taskSetStatusArgs{ID: id, Status: "done"})
	if !r.IsError {
		t.Fatal("expected an error (done must go through acline_task_done)")
	}
	if got := errorCode(t, r); got != "status_needs_gate" {
		t.Errorf("error_code = %q, want status_needs_gate", got)
	}
}

func TestErrorCodeNotFound(t *testing.T) {
	cs, _ := connectedTestServer(t)

	r := callToolRaw(t, cs, "acline_task_gate", taskGateArgs{ID: 999})
	if !r.IsError {
		t.Fatal("expected an error (no such task)")
	}
	if got := errorCode(t, r); got != "not_found" {
		t.Errorf("error_code = %q, want not_found", got)
	}
}

func TestErrorCodeNoActiveSession(t *testing.T) {
	cs, _ := connectedTestServer(t)

	r := callToolRaw(t, cs, "acline_session_end", sessionEndArgs{})
	if !r.IsError {
		t.Fatal("expected an error (no active session)")
	}
	if got := errorCode(t, r); got != "no_active_session" {
		t.Errorf("error_code = %q, want no_active_session", got)
	}
}

func TestErrorCodeSessionActive(t *testing.T) {
	cs, st := connectedTestServer(t)
	id, _ := st.AddTask("t", "", "normal", store.TaskOpts{})
	callTool[sessionStartOut](t, cs, "acline_session_start", sessionStartArgs{TaskID: &id})

	r := callToolRaw(t, cs, "acline_session_start", sessionStartArgs{TaskID: &id})
	if !r.IsError {
		t.Fatal("expected an error (a session is already active)")
	}
	if got := errorCode(t, r); got != "session_active" {
		t.Errorf("error_code = %q, want session_active", got)
	}
}

func TestErrorCodeTokenRequired(t *testing.T) {
	cs, st := connectedTestServer(t)
	if _, err := st.EnableApprovalToken(); err != nil {
		t.Fatal(err)
	}
	id, _ := st.AddTask("t", "", "normal", store.TaskOpts{})

	r := callToolRaw(t, cs, "acline_approve", approveArgs{ID: id, By: "alice"})
	if !r.IsError {
		t.Fatal("expected an error (token required)")
	}
	if got := errorCode(t, r); got != "token_required" {
		t.Errorf("error_code = %q, want token_required", got)
	}
}

func TestErrorCodeTokenInvalid(t *testing.T) {
	cs, st := connectedTestServer(t)
	if _, err := st.EnableApprovalToken(); err != nil {
		t.Fatal(err)
	}
	id, _ := st.AddTask("t", "", "normal", store.TaskOpts{})

	r := callToolRaw(t, cs, "acline_approve", approveArgs{ID: id, By: "alice", Token: "wrong"})
	if !r.IsError {
		t.Fatal("expected an error (invalid token)")
	}
	if got := errorCode(t, r); got != "token_invalid" {
		t.Errorf("error_code = %q, want token_invalid", got)
	}
}

func TestErrorCodeUnclassifiedIsUnset(t *testing.T) {
	cs, _ := connectedTestServer(t)

	r := callToolRaw(t, cs, "acline_task_add", taskAddArgs{Title: ""})
	if !r.IsError {
		t.Fatal("expected an error (empty title)")
	}
	if got := errorCode(t, r); got != "" {
		t.Errorf("error_code = %q, want unset for an unclassified error", got)
	}
}

func TestClassifyErrorGateBlockedSurvivesFriendlierMessage(t *testing.T) {
	// Regression: acline_task_done used to rebuild a fresh, friendlier error
	// message with no %w, which silently broke errors.As(&GateBlockedError)
	// for anyone downstream (including this middleware). wrapErr fixes it by
	// keeping the message but preserving Unwrap.
	cause := &store.GateBlockedError{TaskID: 1, Blockers: []string{"unchecked criterion"}}
	wrapped := wrapErr("cannot complete task: gate not satisfied: unchecked criterion", cause)
	if got := classifyError(wrapped); got != "gate_blocked" {
		t.Fatalf("classifyError(wrapped) = %q, want gate_blocked", got)
	}
	if wrapped.Error() != "cannot complete task: gate not satisfied: unchecked criterion" {
		t.Errorf("wrapErr changed the message: %q", wrapped.Error())
	}
}

func TestClassifyErrorAgentCannotChooseCheckCommand(t *testing.T) {
	err := fmt.Errorf("check run: %w", store.ErrAgentCannotChooseCheckCommand)
	if got := classifyError(err); got != "agent_cannot_choose_check_command" {
		t.Fatalf("classifyError = %q, want agent_cannot_choose_check_command", got)
	}
}

func TestClassifyErrorProjectAndRoleSentinels(t *testing.T) {
	for err, want := range map[error]string{
		store.ErrAgentCannotRegisterProjectPath: "agent_cannot_register_project_path",
		store.ErrProjectPathTooBroad:            "project_path_too_broad",
		store.ErrAgentCannotLeaveReadOnlyRole:   "agent_cannot_leave_read_only_role",
	} {
		if got := classifyError(fmt.Errorf("x: %w", err)); got != want {
			t.Errorf("classifyError(%v) = %q, want %q", err, got, want)
		}
	}
}
