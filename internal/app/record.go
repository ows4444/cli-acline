// This file records specs, decisions and memory entries for every adapter:
// `acline spec|decision|memory add` and their MCP tools each redacted pasted
// secrets, resolved the project, called the store and then logged
// secret_redacted in a separate write; only acline_memory_add told the caller
// anything was redacted, and only the MCP tools refused a blank title or body.
// The store now redacts and writes secret_redacted in the record's own
// transaction; Redacted here is the notice for the person or agent.
package app

import (
	"errors"
	"strings"

	"acline/internal/redact"
	"acline/internal/store"
)

// ErrBodyRequired is a memory entry with a blank body.
var ErrBodyRequired = errors.New("body is required")

// RecordResult is a new record's id and whether a pasted secret was redacted
// from its text before it was stored.
type RecordResult struct {
	ID       int64
	Redacted bool
}

// containsSecret reports whether any field holds a secret value the store
// will redact (redact.Fields on copies, so the caller's text is untouched).
func containsSecret(fields ...string) bool {
	ptrs := make([]*string, len(fields))
	for i := range fields {
		ptrs[i] = &fields[i]
	}
	return redact.Fields(ptrs...)
}

// AddSpecRequest is a new draft spec.
type AddSpecRequest struct {
	Title            string
	Body             string
	ProjectArg       string
	AllowCwdFallback bool
}

// AddSpec records a draft spec in the request's project.
func AddSpec(st *store.Store, req AddSpecRequest) (RecordResult, error) {
	if strings.TrimSpace(req.Title) == "" {
		return RecordResult{}, ErrTitleRequired
	}
	projectID, err := ResolveProject(st, req.ProjectArg, req.AllowCwdFallback)
	if err != nil {
		return RecordResult{}, err
	}
	id, err := st.AddSpec(req.Title, req.Body, store.SpecOpts{ProjectID: projectID})
	if err != nil {
		return RecordResult{}, err
	}
	return RecordResult{ID: id, Redacted: containsSecret(req.Title, req.Body)}, nil
}

// AddDecisionRequest is a new proposed decision.
type AddDecisionRequest struct {
	Title            string
	Scope            string
	Context          string
	Decision         string
	Rationale        string
	ProjectArg       string
	AllowCwdFallback bool
}

// AddDecision records a proposed decision in the request's project.
func AddDecision(st *store.Store, req AddDecisionRequest) (RecordResult, error) {
	if strings.TrimSpace(req.Title) == "" {
		return RecordResult{}, ErrTitleRequired
	}
	projectID, err := ResolveProject(st, req.ProjectArg, req.AllowCwdFallback)
	if err != nil {
		return RecordResult{}, err
	}
	id, err := st.AddDecision(req.Title, store.DecisionOpts{
		Scope: req.Scope, Context: req.Context, Decision: req.Decision, Rationale: req.Rationale, ProjectID: projectID,
	})
	if err != nil {
		return RecordResult{}, err
	}
	return RecordResult{ID: id, Redacted: containsSecret(req.Title, req.Context, req.Decision, req.Rationale)}, nil
}

// AddMemoryRequest is a new memory entry; Kind "" is "lesson".
type AddMemoryRequest struct {
	Area             string
	Kind             string
	Body             string
	ProjectArg       string
	AllowCwdFallback bool
}

// MemoryResult is RecordResult for a memory entry: whether it waits for a
// person's review (an agent wrote it) and an existing entry that already
// says something similar (advisory; the new entry is still recorded).
type MemoryResult struct {
	RecordResult
	Pending bool
	Similar *store.MemoryEntry
}

// AddMemory records a memory entry in the request's project.
func AddMemory(st *store.Store, req AddMemoryRequest) (MemoryResult, error) {
	if strings.TrimSpace(req.Body) == "" {
		return MemoryResult{}, ErrBodyRequired
	}
	projectID, err := ResolveProject(st, req.ProjectArg, req.AllowCwdFallback)
	if err != nil {
		return MemoryResult{}, err
	}
	similar, _ := st.SimilarMemory(req.Body, projectID)
	id, err := st.AddMemory(req.Area, req.Kind, req.Body, store.MemoryOpts{ProjectID: projectID})
	if err != nil {
		return MemoryResult{}, err
	}
	return MemoryResult{
		RecordResult: RecordResult{ID: id, Redacted: containsSecret(req.Body)},
		Pending:      st.Actor.Type != "human",
		Similar:      similar,
	}, nil
}

// ReviseSpecResult is the spec's new version, whether a pasted secret was
// redacted from the new text, and whether revising withdrew its approval (an
// approved spec becomes a draft again).
type ReviseSpecResult struct {
	Version           int
	Redacted          bool
	ApprovalWithdrawn bool
}

// ReviseSpec replaces a spec's body, keeping the earlier text as a version.
func ReviseSpec(st *store.Store, id int64, body string) (ReviseSpecResult, error) {
	if strings.TrimSpace(body) == "" {
		return ReviseSpecResult{}, ErrBodyRequired
	}
	before, err := st.GetSpec(id)
	if err != nil {
		return ReviseSpecResult{}, err
	}
	version, err := st.ReviseSpec(id, body)
	if err != nil {
		return ReviseSpecResult{}, err
	}
	return ReviseSpecResult{
		Version:           version,
		Redacted:          containsSecret(body),
		ApprovalWithdrawn: before.Status == "approved",
	}, nil
}

// ErrLogTypeRequired is a history event logged without a type.
var ErrLogTypeRequired = errors.New("a type is required (decision|bug|commit|blocker|note)")

// ErrMessageRequired is a history event with a blank message.
var ErrMessageRequired = errors.New("message is required")

// AddNoteRequest is a quick capture for /reflect; Source "" is "manual".
type AddNoteRequest struct {
	Body             string
	Source           string
	RoleArg          string
	ProjectArg       string
	AllowCwdFallback bool
}

// AddNote records a note in the request's project under its role.
func AddNote(st *store.Store, req AddNoteRequest) (RecordResult, error) {
	if strings.TrimSpace(req.Body) == "" {
		return RecordResult{}, ErrBodyRequired
	}
	projectID, err := ResolveProject(st, req.ProjectArg, req.AllowCwdFallback)
	if err != nil {
		return RecordResult{}, err
	}
	roleID, err := st.ResolveRole(req.RoleArg, projectID)
	if err != nil {
		return RecordResult{}, err
	}
	id, err := st.AddNoteWithRole(projectID, roleID, req.Body, req.Source)
	if err != nil {
		return RecordResult{}, err
	}
	return RecordResult{ID: id, Redacted: containsSecret(req.Body)}, nil
}

// LogRequest is a hand-recorded history event (never a /reflect note).
type LogRequest struct {
	Type             string
	Message          string
	TaskID           *int64
	RoleArg          string
	ProjectArg       string
	AllowCwdFallback bool
}

// Log records a history event in the current session, if one is active.
// store.ErrNotAUserLogType comes back for a type only acline writes.
func Log(st *store.Store, req LogRequest) (RecordResult, error) {
	if strings.TrimSpace(req.Message) == "" {
		return RecordResult{}, ErrMessageRequired
	}
	if req.Type == "" {
		return RecordResult{}, ErrLogTypeRequired
	}
	var sessionID *int64
	if sess, err := st.CurrentSession(); err == nil {
		sessionID = &sess.ID
	} else if !errors.Is(err, store.ErrNoActiveSession) {
		return RecordResult{}, err
	}
	roleID, err := ResolveRole(st, req.RoleArg, req.ProjectArg, req.AllowCwdFallback)
	if err != nil {
		return RecordResult{}, err
	}
	id, err := st.LogEventWithRole(req.TaskID, sessionID, roleID, req.Type, req.Message)
	if err != nil {
		return RecordResult{}, err
	}
	return RecordResult{ID: id, Redacted: containsSecret(req.Message)}, nil
}
