package store

import (
	"errors"
	"fmt"
)

// ErrAgentCannotApprove is returned when an agent actor tries to record an
// approval without holding the approval token. Naming a person (`--by`) does
// not help: identity is an environment variable the agent controls, so a name
// it typed is not evidence that a person approved. Adapters append their own
// hint.
var ErrAgentCannotApprove = errors.New("an agent cannot approve its own work")

// ApprovalRequest is one approve/reject of a task at review.
type ApprovalRequest struct {
	TaskID   int64
	RoleID   *int64
	Kind     string // code_review | override
	Decision string // approved | rejected
	By       string // who approved; empty = the recording actor
	Note     string
	Token    string // approval token; only consulted when the store has one enabled
}

// RecordApproval records an approval or rejection and its audit event. It is
// the single implementation behind `acline approve|reject` and the MCP
// acline_approve|reject tools (each used to carry its own copy of the rules).
//
// Approving is the privileged direction. When an approval token is enabled the
// caller must present it (see auth.go). An agent actor can only approve by
// presenting it; a person (human actor) at a terminal needs no token unless one
// is enabled. Until a token is enabled identity is self-declared, so the
// dashboard and `acline auth status` say so. Rejecting stays open to everyone -- the
// newest code_review decision controls, so a rejection can only ever make the
// gate stricter.
func (s *Store) RecordApproval(r ApprovalRequest) (int64, error) {
	if _, err := s.GetTask(r.TaskID); err != nil {
		return 0, err
	}
	if r.Kind == "" {
		r.Kind = "code_review"
	}
	if r.Decision == "approved" {
		viaToken, err := s.authorize(r.Token)
		if err != nil {
			return 0, err
		}
		if !viaToken && s.Actor.Type == "agent" {
			return 0, ErrAgentCannotApprove
		}
	}
	id, err := s.AddApprovalWithRole(r.TaskID, r.RoleID, r.Kind, r.By, r.Decision, r.Note)
	if err != nil {
		return 0, err
	}
	if r.Decision == "approved" {
		s.LogTaskEvent(r.TaskID, "approval", fmt.Sprintf("approved (%s)", r.Kind))
	} else {
		s.LogTaskEvent(r.TaskID, "approval", "rejected at review: "+scrubText(r.Note))
	}
	return id, nil
}
