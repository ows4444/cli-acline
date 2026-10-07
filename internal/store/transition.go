package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// The status a task, spec, decision or plan may move to from its current one,
// in one table per kind. Every status change checks it inside the transaction
// that makes the change, so the status it reads is the one it replaces.
//
// A status that maps to itself may be set again without effect (approving an
// approved spec, accepting an accepted decision); one that does not is refused.

var taskTransitions = map[string][]string{
	"backlog":     {"backlog", "todo", "in_progress", "blocked", "review", "cancelled", "done"},
	"todo":        {"backlog", "todo", "in_progress", "blocked", "review", "cancelled", "done"},
	"in_progress": {"backlog", "todo", "in_progress", "blocked", "review", "cancelled", "done"},
	"blocked":     {"backlog", "todo", "in_progress", "blocked", "review", "cancelled", "done"},
	"review":      {"backlog", "todo", "in_progress", "blocked", "review", "cancelled", "done"},
	// Reopening a done task is rework (ComputeMetrics counts it); completing it
	// again is not a thing, and cancelling it means reopening it first.
	"done": {"todo", "in_progress", "blocked", "review"},
	// A cancelled task comes back as planned work, not straight into progress.
	"cancelled": {"backlog", "todo"},
}

var specTransitions = map[string][]string{
	"draft":    {"draft", "approved", "superseded"},
	"approved": {"approved", "draft", "superseded"}, // revising withdraws the approval
	// Superseded is final, like a superseded decision: it keeps pointing at its
	// replacement. Whether a spec is implemented is computed (SpecImplemented).
	"superseded": {},
}

var decisionTransitions = map[string][]string{
	"proposed": {"proposed", "accepted", "rejected", "superseded"},
	"accepted": {"accepted", "rejected", "superseded", "deprecated"},
	// Rejected and superseded are final: re-accepting one would bring back a
	// rule later work was told is gone (and a superseded one keeps pointing at
	// its replacement). Propose it again instead.
	"rejected":   {},
	"superseded": {},
	// Deprecated: an accepted rule that no longer applies and has no
	// replacement (one with a replacement is superseded).
	"deprecated": {},
}

var planTransitions = map[string][]string{
	"draft":      {"approved", "rejected", "superseded"},
	"approved":   {},
	"rejected":   {},
	"superseded": {},
}

var transitions = map[string]struct {
	table string
	next  map[string][]string
}{
	"task":     {"tasks", taskTransitions},
	"spec":     {"specs", specTransitions},
	"decision": {"decisions", decisionTransitions},
	"plan":     {"plans", planTransitions},
}

// ErrInvalidTransition is a status change the record's current status does not allow.
var ErrInvalidTransition = errors.New("status change not allowed")

// allowedTransition reports whether a record of kind may go from one status to another.
func allowedTransition(kind, from, to string) bool {
	for _, s := range transitions[kind].next[from] {
		if s == to {
			return true
		}
	}
	return false
}

// checkTransition reads the current status of kind id through q (the
// transaction about to change it) and refuses a change the table does not
// allow. A missing record is ErrNotFound.
func checkTransition(q rowQuerier, kind string, id int64, to string) error {
	t, ok := transitions[kind]
	if !ok {
		return fmt.Errorf("no status rules for %q", kind)
	}
	var from string
	err := q.QueryRow(`SELECT status FROM `+t.table+` WHERE id = ?`, id).Scan(&from)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%s #%d: %w", kind, id, ErrNotFound)
	}
	if err != nil {
		return err
	}
	if !allowedTransition(kind, from, to) {
		if from == to {
			return fmt.Errorf("%s #%d is already %s: %w", kind, id, from, ErrInvalidTransition)
		}
		return fmt.Errorf("%s #%d is %s and cannot become %s: %w", kind, id, from, to, ErrInvalidTransition)
	}
	return nil
}

// transitionWithEvent sets kind id's status to `to` with query, after checking
// the change in the same transaction, and records the event.
func (s *Store) transitionWithEvent(kind string, id int64, to, eventType, message, query string, args ...any) error {
	sessionID := s.currentSessionID()
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := checkTransition(tx, kind, id, to); err != nil {
		return err
	}
	res, err := tx.Exec(query, args...)
	if err != nil {
		return err
	}
	if err := mustExist(res, kind, id); err != nil {
		return err
	}
	if _, err := s.logEventTx(tx, nil, sessionID, nil, eventType, message); err != nil {
		return err
	}
	return tx.Commit()
}
