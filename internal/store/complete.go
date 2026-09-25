package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// GateBlockedError is returned by CompleteTask when the completion gate is not
// satisfied and the caller did not ask to override it. Callers can detect it
// with errors.As instead of string-matching a message.
type GateBlockedError struct {
	TaskID   int64
	Blockers []string
}

func (e *GateBlockedError) Error() string {
	return fmt.Sprintf("task #%d: gate not satisfied: %s", e.TaskID, strings.Join(e.Blockers, "; "))
}

// ErrAgentCannotOverrideGate is returned when an agent tries to complete a task
// past an unsatisfied gate (`task done --force`) without the approval token.
var ErrAgentCannotOverrideGate = errors.New("an agent cannot override the completion gate: a person must")

// CompleteResult reports how CompleteTask finished.
type CompleteResult struct {
	Overridden bool     // the gate was unsatisfied and force bypassed it (recorded)
	Warnings   []string // non-blocking gate warnings
	Blockers   []string // what was overridden, when Overridden
}

// CompleteTask marks a task done, subject to the verification/approval gate.
// With force=false an unsatisfied gate yields *GateBlockedError and nothing is
// written. With force=true the override is itself recorded as an oversight
// event, never silently skipped, and -- when the store has an approval token
// enabled -- requires that token (see auth.go).
//
// The status change and every audit row it implies (the override approval, the
// override event, the status_change event) are written in ONE transaction, so
// a crash or a lock timeout part-way can no longer leave an override on record
// for a task that never completed, or a completed task with no audit trail.
// This is the single implementation behind both `acline task done` and the MCP
// acline_task_done tool (they used to carry separate, hand-mirrored copies).
//
// The gate helpers read through the pooled connection, which the transaction
// holds, so the gate is evaluated just before it opens. To close that window,
// the gate's inputs are fingerprinted (gateInputs) before evaluating and read
// again inside the transaction; if anything changed (another process recorded
// a check or approval, changed the task...), the gate is evaluated again.
func (s *Store) CompleteTask(id int64, force bool, token string) (CompleteResult, error) {
	return s.CompleteTaskForTree(id, force, token, "")
}

// CompleteTaskForTree is CompleteTask with the current fingerprint of the working
// tree, so a high-risk task cannot complete on evidence about code that has since
// changed (see EvaluateGateForTree). "" means unknown.
func (s *Store) CompleteTaskForTree(id int64, force bool, token, currentTree string) (CompleteResult, error) {
	for attempt := 0; attempt < 3; attempt++ {
		res, done, err := s.tryComplete(id, force, token, currentTree)
		if done || err != nil {
			return res, err
		}
	}
	return CompleteResult{}, fmt.Errorf("task #%d: its checks or approvals kept changing while the gate was evaluated; try again", id)
}

// afterGateEvaluated lets a test act in the window between evaluating the gate
// and opening the completion transaction, as another process could.
var afterGateEvaluated func()

// tryComplete is one attempt of CompleteTaskForTree. done=false means the gate's
// inputs changed while it was evaluated and nothing was written.
func (s *Store) tryComplete(id int64, force bool, token, currentTree string) (res CompleteResult, done bool, err error) {
	before, err := gateInputs(s.DB, id)
	if err != nil {
		return res, false, err
	}
	gate, err := s.EvaluateGateForTree(id, currentTree)
	if err != nil {
		return res, false, err
	}
	res.Warnings = gate.Warnings
	if !gate.OK() {
		if !force {
			return res, true, &GateBlockedError{TaskID: id, Blockers: gate.Blockers}
		}
		// Overriding the gate is privileged: an agent needs the approval token, and
		// with a token enabled everyone does.
		if err := s.requirePerson(token, ErrAgentCannotOverrideGate); err != nil {
			return res, true, err
		}
		res.Overridden = true
		res.Blockers = gate.Blockers
	}
	if afterGateEvaluated != nil {
		afterGateEvaluated()
	}

	// Resolved before the transaction opens, for the same single-connection
	// reason as above.
	var sessionID *int64
	if sess, err := s.CurrentSession(); err == nil {
		sessionID = &sess.ID
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return res, true, err
	}
	defer tx.Rollback()

	// IMMEDIATE: no other writer can change the inputs from here to Commit.
	if after, err := gateInputs(tx, id); err != nil {
		return res, true, err
	} else if after != before {
		return CompleteResult{}, false, nil
	}

	if res.Overridden {
		reason := strings.Join(gate.Blockers, "; ")
		if _, err := s.insertApproval(tx, sessionID, id, nil, "override", "", "overridden", scrubText(reason)); err != nil {
			return res, true, err
		}
		if _, err := s.logEventTx(tx, &id, sessionID, nil, "override", "gate overridden: "+reason); err != nil {
			return res, true, err
		}
	}
	if err := updateTaskStatus(tx, id, "done"); err != nil {
		return res, true, err
	}
	if _, err := s.logEventTx(tx, &id, sessionID, nil, "status_change", "status -> done"); err != nil {
		return res, true, err
	}
	return res, true, tx.Commit()
}

// rowQuerier is satisfied by *sql.DB and *sql.Tx.
type rowQuerier interface {
	QueryRow(query string, args ...any) *sql.Row
}

// gateInputs fingerprints everything EvaluateGateForTree reads for a task: its
// checks, approvals, own row (status, risk, autonomy, project), criteria,
// prerequisites and the project's approving roles. Rows are append-only or
// stamp updated_at, so ids, counts and timestamps are enough to see a change.
func gateInputs(q rowQuerier, taskID int64) (string, error) {
	var fp string
	err := q.QueryRow(`SELECT
		(SELECT COALESCE(MAX(id), 0) FROM checks WHERE task_id = ?1) || '|' ||
		(SELECT COALESCE(MAX(id), 0) FROM approvals WHERE task_id = ?1) || '|' ||
		(SELECT COALESCE(updated_at, '') || status || risk || autonomy || COALESCE(project_id, '') FROM tasks WHERE id = ?1) || '|' ||
		(SELECT COUNT(*) || ':' || COALESCE(SUM(done), 0) FROM task_criteria WHERE task_id = ?1) || '|' ||
		(SELECT COALESCE(GROUP_CONCAT(l.id || t.status || t.updated_at), '') FROM task_links l JOIN tasks t ON t.id = l.related_task_id OR t.id = l.task_id WHERE l.task_id = ?1 OR l.related_task_id = ?1) || '|' ||
		(SELECT COUNT(*) FROM roles WHERE can_approve = 1 AND project_id = (SELECT project_id FROM tasks WHERE id = ?1))`,
		taskID).Scan(&fp)
	return fp, err
}

// ErrStatusDoneNeedsGate is returned by SetTaskStatus for "done": completion
// must go through CompleteTask so the verification/approval gate is evaluated.
var ErrStatusDoneNeedsGate = errors.New("use task done so the completion gate is evaluated")

// SetTaskStatus moves a task to any status except "done" and records the
// status_change event in the same transaction, so the change and its audit
// trail cannot diverge. It backs `acline task update --status` and the MCP
// acline_task_set_status tool.
func (s *Store) SetTaskStatus(id int64, status string) error {
	return s.SetTaskStatusWithReason(id, status, "")
}

// SetTaskStatusWithReason is SetTaskStatus that also records why a task is
// blocked. The reason is kept only for status "blocked"; every status change
// clears the previous one, so it never outlives the block.
func (s *Store) SetTaskStatusWithReason(id int64, status, reason string) error {
	reason = strings.TrimSpace(scrubText(reason))
	if status != "blocked" {
		reason = ""
	}
	if status == "done" {
		return ErrStatusDoneNeedsGate
	}
	var sessionID *int64
	if sess, err := s.CurrentSession(); err == nil {
		sessionID = &sess.ID
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := updateTaskStatus(tx, id, status); err != nil {
		return err
	}
	msg := "status -> " + status
	if reason != "" {
		if _, err := tx.Exec(`UPDATE tasks SET blocked_reason = ? WHERE id = ?`, reason, id); err != nil {
			return err
		}
		msg += ": " + reason
	}
	if _, err := s.logEventTx(tx, &id, sessionID, nil, "status_change", msg); err != nil {
		return err
	}
	return tx.Commit()
}
