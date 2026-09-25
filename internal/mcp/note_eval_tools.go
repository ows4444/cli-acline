package mcp

import (
	"context"
	"fmt"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"acline/internal/store"
)

type noteOut struct {
	ID         int64   `json:"id"`
	ProjectID  *int64  `json:"project_id,omitempty"`
	Body       string  `json:"body"`
	Source     string  `json:"source"`
	ActorID    *string `json:"actor_id,omitempty"`
	CreatedAt  string  `json:"created_at"`
	PromotedTo *string `json:"promoted_to,omitempty"`
	PromotedID *int64  `json:"promoted_id,omitempty"`
}

type noteListArgs struct {
	Project        string `json:"project,omitempty" jsonschema:"restrict to a project name"`
	UnpromotedOnly bool   `json:"unpromoted_only,omitempty" jsonschema:"only notes not yet promoted (the /reflect queue)"`
	Limit          int    `json:"limit,omitempty" jsonschema:"max results (default 100, capped at 500)"`
	Offset         int    `json:"offset,omitempty" jsonschema:"skip this many results (for paging)"`
}

type noteListOut struct {
	Notes     []noteOut `json:"notes"`
	Truncated bool      `json:"truncated,omitempty"`
}

type notePromoteArgs struct {
	NoteID     int64  `json:"note_id" jsonschema:"the note id"`
	Kind       string `json:"kind" jsonschema:"decision|memory"`
	Title      string `json:"title,omitempty" jsonschema:"decision title (default: the note text)"`
	Decision   string `json:"decision,omitempty"`
	Context    string `json:"context,omitempty"`
	Rationale  string `json:"rationale,omitempty"`
	MemoryArea string `json:"memory_area,omitempty"`
	MemoryKind string `json:"memory_kind,omitempty" jsonschema:"constraint|lesson|pitfall|operational|failure_pattern (default lesson)"`
}

type notePromoteOut struct {
	ID   int64  `json:"id"`
	Kind string `json:"kind"`
}

type evalRecordArgs struct {
	Suite      string  `json:"suite" jsonschema:"the eval suite name"`
	PassRate   float64 `json:"pass_rate" jsonschema:"measured pass rate between 0 and 1"`
	SampleSize int64   `json:"sample_size,omitempty"`
	Note       string  `json:"note,omitempty"`
	TaskID     *int64  `json:"task_id,omitempty"`
	Project    string  `json:"project,omitempty" jsonschema:"project name"`
}

type evalRecordOut struct {
	ID int64 `json:"id"`
}

func registerNoteEvalTools(s *sdkmcp.Server, st *store.Store) {
	addTool(s, &sdkmcp.Tool{
		Name:        "acline_note_list",
		Description: "List captured notes (fast, unstructured capture), oldest first. With unpromoted_only these are the notes awaiting /reflect.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args noteListArgs) (*sdkmcp.CallToolResult, noteListOut, error) {
		projectID, err := resolveProject(st, args.Project)
		if err != nil {
			return nil, noteListOut{}, err
		}
		notes, err := st.ListNotes(store.NoteFilter{ProjectID: projectID, UnpromotedOnly: args.UnpromotedOnly})
		if err != nil {
			return nil, noteListOut{}, err
		}
		page, truncated := paginate(notes, args.Limit, args.Offset)
		out := noteListOut{Notes: make([]noteOut, len(page)), Truncated: truncated}
		for i, n := range page {
			out.Notes[i] = noteOut{
				ID: n.ID, ProjectID: nullIntPtr(n.ProjectID), Body: n.Body, Source: n.Source, ActorID: nullStrPtr(n.ActorID),
				CreatedAt: n.CreatedAt, PromotedTo: nullStrPtr(n.PromotedTo), PromotedID: nullIntPtr(n.PromotedID),
			}
		}
		return textResult(fmt.Sprintf("%d note(s)", len(page))), out, nil
	})

	addTool(s, &sdkmcp.Tool{
		Name: "acline_note_promote",
		Description: "Promote a note into a proposed decision or a memory entry. A note can be promoted only once. " +
			"Memory written by an agent is held for human review, exactly as acline_memory_add.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args notePromoteArgs) (*sdkmcp.CallToolResult, notePromoteOut, error) {
		id, err := st.PromoteNote(args.NoteID, args.Kind, store.PromoteOpts{
			Title: args.Title, Decision: args.Decision, Context: args.Context, Rationale: args.Rationale,
			MemoryArea: args.MemoryArea, MemoryKind: args.MemoryKind,
		})
		if err != nil {
			return nil, notePromoteOut{}, err
		}
		return textResult(fmt.Sprintf("note #%d promoted to %s #%d", args.NoteID, args.Kind, id)), notePromoteOut{ID: id, Kind: args.Kind}, nil
	})

	addTool(s, &sdkmcp.Tool{
		Name:        "acline_eval_record",
		Description: "Record a measured eval result (pass rate per suite) -- the evidence behind an autonomy promotion.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args evalRecordArgs) (*sdkmcp.CallToolResult, evalRecordOut, error) {
		if args.TaskID != nil {
			if _, err := st.GetTask(*args.TaskID); err != nil {
				return nil, evalRecordOut{}, err
			}
		}
		projectID, err := resolveProject(st, args.Project)
		if err != nil {
			return nil, evalRecordOut{}, err
		}
		id, err := st.AddEval(args.TaskID, projectID, args.Suite, args.PassRate, args.SampleSize, args.Note)
		if err != nil {
			return nil, evalRecordOut{}, err
		}
		return textResult(fmt.Sprintf("eval #%d recorded: %s at %.1f%%", id, args.Suite, args.PassRate*100)), evalRecordOut{ID: id}, nil
	})
}
