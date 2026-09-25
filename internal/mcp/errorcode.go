package mcp

import (
	"context"
	"errors"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	sqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"acline/internal/store"
)

// errorCodeMiddleware attaches a stable, machine-readable `error_code` to
// every failed tool call's response `_meta`, alongside the existing
// human-readable message. Before this, a client could only tell one failure
// from another by matching message text (an extension had to detect a gate
// failure by catching any exception, and a connection failure by regex) -- fragile, and it meant genuinely
// different failures (an unsatisfied gate vs. a locked database vs. a typo'd
// id) were indistinguishable to code that wanted to react differently to
// each.
//
// It runs as receiving middleware rather than requiring every tool handler
// to opt in: `next` already returns the *CallToolResult a failed handler
// built via CallToolResult.SetError (see the go-sdk's AddTool wiring), which
// keeps the original error accessible via GetError() specifically so server
// middleware like this can inspect it. One classifier, applied uniformly, so
// a new handler doesn't have to remember to wire this up itself -- and can't
// forget to.
func errorCodeMiddleware() sdkmcp.Middleware {
	return func(next sdkmcp.MethodHandler) sdkmcp.MethodHandler {
		return func(ctx context.Context, method string, req sdkmcp.Request) (sdkmcp.Result, error) {
			res, err := next(ctx, method, req)
			if method != "tools/call" {
				return res, err
			}
			ctr, ok := res.(*sdkmcp.CallToolResult)
			if !ok || !ctr.IsError {
				return res, err
			}
			if code := classifyError(ctr.GetError()); code != "" {
				if ctr.Meta == nil {
					ctr.Meta = sdkmcp.Meta{}
				}
				ctr.Meta["error_code"] = code
			}
			return res, err
		}
	}
}

// classifyError maps a known error (or chain containing one, via errors.Is/
// errors.As -- so wrapping a cause with extra context, as several handlers
// do for a friendlier message, doesn't defeat classification as long as the
// wrap preserves Unwrap) to a stable code. Returns "" for anything not
// recognized, which errorCodeMiddleware leaves unset -- an unclassified
// error is still reported (message + IsError), just without a code a client
// can branch on.
func classifyError(err error) string {
	if err == nil {
		return ""
	}
	var blocked *store.GateBlockedError
	switch {
	case errors.As(err, &blocked):
		return "gate_blocked"
	case errors.Is(err, store.ErrAgentCannotApprove):
		return "agent_cannot_approve"
	case errors.Is(err, store.ErrAgentCannotReview):
		return "agent_cannot_review"
	case errors.Is(err, store.ErrAgentCannotLoosenTask):
		return "agent_cannot_loosen_task"
	case errors.Is(err, store.ErrAgentCannotOverrideGate):
		return "agent_cannot_override_gate"
	case errors.Is(err, store.ErrAgentCannotCreateApprovingRole):
		return "agent_cannot_create_approving_role"
	case errors.Is(err, store.ErrAgentCannotUseHumanRole):
		return "agent_cannot_use_human_role"
	case errors.Is(err, store.ErrAgentCannotRecordHumanReview):
		return "agent_cannot_record_human_review"
	case errors.Is(err, store.ErrAgentCannotVerifyDependency):
		return "agent_cannot_verify_dependency"
	case errors.Is(err, store.ErrAgentCannotImportSnapshot):
		return "agent_cannot_import_snapshot"
	case errors.Is(err, store.ErrAgentCannotChooseCheckCommand):
		return "agent_cannot_choose_check_command"
	case errors.Is(err, store.ErrAgentCannotRegisterProjectPath):
		return "agent_cannot_register_project_path"
	case errors.Is(err, store.ErrProjectPathTooBroad):
		return "project_path_too_broad"
	case errors.Is(err, store.ErrAgentCannotLeaveReadOnlyRole):
		return "agent_cannot_leave_read_only_role"
	case errors.Is(err, store.ErrApprovalTokenRequired):
		return "token_required"
	case errors.Is(err, store.ErrApprovalTokenInvalid):
		return "token_invalid"
	case errors.Is(err, store.ErrNoActiveSession):
		return "no_active_session"
	case errors.Is(err, store.ErrSessionActive):
		return "session_active"
	case errors.Is(err, store.ErrStatusDoneNeedsGate):
		return "status_needs_gate"
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrProjectNotFound):
		return "not_found"
	case isBusy(err):
		return "busy"
	default:
		return ""
	}
}

// isBusy reports whether err is (or wraps) SQLITE_BUSY -- the database was
// locked by another writer. WAL plus busy_timeout makes this rare (a
// writer waits up to 5s before failing), but concurrent load can still hit
// it, and a client that can tell "busy, retry" from every other failure can
// actually retry instead of just surfacing an opaque error.
func isBusy(err error) bool {
	var sqliteErr *sqlite.Error
	return errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_BUSY
}

// wrappedErr lets a handler give an error a friendlier message while keeping
// the original classifiable via errors.Is/errors.As -- fmt.Errorf's %w also
// re-includes the wrapped error's own text in the formatted message, which
// isn't always wanted (see acline_task_done's gate-blocked message, which
// already states the blockers in its own words).
type wrappedErr struct {
	msg   string
	cause error
}

func wrapErr(msg string, cause error) error { return &wrappedErr{msg: msg, cause: cause} }
func (e *wrappedErr) Error() string         { return e.msg }
func (e *wrappedErr) Unwrap() error         { return e.cause }
