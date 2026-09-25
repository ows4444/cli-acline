package store

import (
	"errors"
	"fmt"
)

// ErrNoteAlreadyPromoted is returned by PromoteNote when a note was already
// (or is concurrently being) promoted.
var ErrNoteAlreadyPromoted = errors.New("note was already promoted")

// PromoteOpts carries the kind-specific fields of a promotion.
type PromoteOpts struct {
	Title      string // decision title (default: the note body)
	Scope      string
	Context    string
	Decision   string // default: the note body
	Rationale  string
	MemoryArea string
	MemoryKind string
}

// PromoteNote turns a note into a decision (kind "decision") or a memory entry
// (kind "memory") and marks the note promoted, returning the new row's id.
//
// The note is *claimed* first with a conditional UPDATE, so two racing
// promotions of the same note cannot both create a row (the CLI used to check
// `promoted_to` and act later). If creating the row fails, the claim is
// released so the note can be promoted again.
func (s *Store) PromoteNote(noteID int64, kind string, o PromoteOpts) (int64, error) {
	if kind != "decision" && kind != "memory" {
		return 0, fmt.Errorf("invalid promotion kind %q (want: decision|memory)", kind)
	}
	note, err := s.GetNote(noteID)
	if err != nil {
		return 0, err
	}
	res, err := s.DB.Exec(`UPDATE notes SET promoted_to = ? WHERE id = ? AND promoted_to IS NULL`, kind, noteID)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, fmt.Errorf("note #%d: %w", noteID, ErrNoteAlreadyPromoted)
	}
	release := func() {
		_, _ = s.DB.Exec(`UPDATE notes SET promoted_to = NULL, promoted_id = NULL WHERE id = ?`, noteID)
	}

	var projectID *int64
	if note.ProjectID.Valid {
		projectID = &note.ProjectID.Int64
	}
	var id int64
	switch kind {
	case "decision":
		title := o.Title
		if title == "" {
			title = note.Body
		}
		decision := o.Decision
		if decision == "" {
			decision = note.Body
		}
		id, err = s.AddDecision(title, DecisionOpts{Scope: o.Scope, Context: o.Context, Decision: decision, Rationale: o.Rationale, ProjectID: projectID})
		if err == nil {
			s.LogEventGlobal("decision_recorded", fmt.Sprintf("decision #%d proposed from note #%d: %s", id, noteID, title))
		}
	case "memory":
		id, err = s.AddMemory(o.MemoryArea, o.MemoryKind, note.Body, MemoryOpts{ProjectID: projectID, SourceKind: "note", SourceID: noteID})
	}
	if err != nil {
		release()
		return 0, err
	}
	if err := s.MarkPromoted(noteID, kind, id); err != nil {
		return id, err
	}
	return id, nil
}
