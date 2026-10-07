package mcp

import (
	"context"
	"fmt"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"acline/internal/app"
	"acline/internal/store"
)

type memoryEntryOut struct {
	ID         int64   `json:"id"`
	Area       *string `json:"area,omitempty"`
	Kind       string  `json:"kind"`
	Body       string  `json:"body"`
	Status     string  `json:"status"`
	Stale      bool    `json:"stale"`
	ProjectID  *int64  `json:"project_id,omitempty"`
	ActorID    *string `json:"actor_id,omitempty"`
	ReviewedAt *string `json:"reviewed_at,omitempty"`
	SourceKind *string `json:"source_kind,omitempty"`
	SourceID   *int64  `json:"source_id,omitempty"`
	CreatedAt  string  `json:"created_at"`
}

func toMemoryEntryOut(m store.MemoryEntry) memoryEntryOut {
	return memoryEntryOut{
		ID: m.ID, Area: nullStrPtr(m.Area), Kind: m.Kind, Body: m.Body, Status: m.Status, Stale: m.Stale,
		ProjectID: nullIntPtr(m.ProjectID), ActorID: nullStrPtr(m.ActorID), ReviewedAt: nullStrPtr(m.ReviewedAt),
		SourceKind: nullStrPtr(m.SourceKind), SourceID: nullIntPtr(m.SourceID), CreatedAt: m.CreatedAt,
	}
}

type memoryListArgs struct {
	Status       string `json:"status,omitempty" jsonschema:"pending|approved|rejected (omit for all statuses)"`
	Project      string `json:"project,omitempty" jsonschema:"restrict to a project name"`
	IncludeStale bool   `json:"include_stale,omitempty" jsonschema:"include entries marked stale (excluded by default)"`
	Limit        int    `json:"limit,omitempty" jsonschema:"max results (default 100, capped at 500)"`
	Offset       int    `json:"offset,omitempty" jsonschema:"skip this many results (for paging)"`
}

type memoryListOut struct {
	Entries   []memoryEntryOut `json:"entries"`
	Truncated bool             `json:"truncated,omitempty"`
}

type memoryAddArgs struct {
	Area    string `json:"area,omitempty" jsonschema:"ownership area this memory applies to"`
	Kind    string `json:"kind,omitempty" jsonschema:"constraint|lesson|pitfall|operational|failure_pattern (default lesson)"`
	Body    string `json:"body" jsonschema:"the memory text -- non-obvious facts only, not routine notes"`
	Project string `json:"project,omitempty" jsonschema:"project name to scope this entry to"`
}

type memoryAddOut struct {
	ID              int64 `json:"id"`
	PendingReview   bool  `json:"pending_review"`
	SecretsRedacted bool  `json:"secrets_redacted,omitempty"`
	// SimilarTo is the id of an existing entry that already says the same
	// thing. The new entry is still recorded; this is advisory.
	SimilarTo *int64 `json:"similar_to,omitempty"`
}

type memoryReviewArgs struct {
	ID    int64  `json:"id" jsonschema:"the memory entry id"`
	Token string `json:"token,omitempty" jsonschema:"human approval token; required to approve when the store has one enabled (see acline auth). Never read from the server's environment."`
}

type memoryDecayArgs struct {
	Days    int    `json:"days,omitempty" jsonschema:"minimum days since last reconfirmation (default 90)"`
	Project string `json:"project,omitempty" jsonschema:"restrict to a project name"`
}

type memoryIDArgs struct {
	ID int64 `json:"id" jsonschema:"the memory entry id"`
}

// memoryRetireArgs is memoryIDArgs plus the approval token: forgetting and
// reconfirming memory are a person's decisions (store.ErrAgentCannotRetireMemory).
type memoryRetireArgs struct {
	ID    int64  `json:"id" jsonschema:"the memory entry id"`
	Token string `json:"token,omitempty" jsonschema:"human approval token; needed when the store has one enabled or the server's actor is an agent. Never read from the server's environment."`
}

type memoryIDOut struct {
	ID int64 `json:"id"`
}

func registerMemoryTools(s *sdkmcp.Server, st *store.Store) {
	addMemoryListTool(s, st)
	addMemoryAddTool(s, st)
	addMemoryDecayTool(s, st)
	addMemoryTouchTool(s, st)
	addMemoryForgetTool(s, st)
	addMemoryReviewTool(s, st, "acline_memory_approve", "approved", true)
	addMemoryReviewTool(s, st, "acline_memory_reject", "rejected", false)
}

func addMemoryListTool(s *sdkmcp.Server, st *store.Store) {
	addTool(s, &sdkmcp.Tool{
		Name:        "acline_memory_list",
		Description: "List durable memory entries (constraints/lessons/pitfalls/operational notes/failure patterns), optionally filtered by status or project. Pass status=\"pending\" to see the review queue.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args memoryListArgs) (*sdkmcp.CallToolResult, memoryListOut, error) {
		projectID, err := resolveProject(st, args.Project)
		if err != nil {
			return nil, memoryListOut{}, err
		}
		entries, err := st.ListMemory(store.MemoryFilter{Status: args.Status, IncludeStale: args.IncludeStale, ProjectID: projectID})
		if err != nil {
			return nil, memoryListOut{}, err
		}
		page, truncated := paginate(entries, args.Limit, args.Offset)
		out := memoryListOut{Entries: make([]memoryEntryOut, len(page)), Truncated: truncated}
		for i, m := range page {
			out.Entries[i] = toMemoryEntryOut(m)
		}
		return textResult(fmt.Sprintf("%d memory entr(ies)", len(page))), out, nil
	})
}

func addMemoryAddTool(s *sdkmcp.Server, st *store.Store) {
	addTool(s, &sdkmcp.Tool{
		Name: "acline_memory_add",
		Description: "Record a durable memory entry -- a non-obvious lesson/constraint/pitfall not derivable " +
			"from code. Agent-written entries land pending review. Any live secret value pasted in body is " +
			"redacted before storage.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args memoryAddArgs) (*sdkmcp.CallToolResult, memoryAddOut, error) {
		res, err := app.AddMemory(st, app.AddMemoryRequest{Area: args.Area, Kind: args.Kind, Body: args.Body, ProjectArg: args.Project})
		if err != nil {
			return nil, memoryAddOut{}, err
		}
		out := memoryAddOut{ID: res.ID, PendingReview: res.Pending, SecretsRedacted: res.Redacted}
		if res.Similar != nil {
			out.SimilarTo = &res.Similar.ID
		}
		return textResult(recordedSummary("memory", res.RecordResult)), out, nil
	})
}

func addMemoryDecayTool(s *sdkmcp.Server, st *store.Store) {
	addTool(s, &sdkmcp.Tool{
		Name:        "acline_memory_decay",
		Description: "List approved memory entries not reconfirmed (via touch) in --days days (default 90) -- candidates for a human to reconfirm or retire. Nothing is auto-deleted or auto-marked stale.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args memoryDecayArgs) (*sdkmcp.CallToolResult, memoryListOut, error) {
		days := args.Days
		if days <= 0 {
			days = store.DefaultMemoryDecayDays
		}
		projectID, err := resolveProject(st, args.Project)
		if err != nil {
			return nil, memoryListOut{}, err
		}
		entries, err := st.DecayCandidates(days, projectID)
		if err != nil {
			return nil, memoryListOut{}, err
		}
		out := memoryListOut{Entries: make([]memoryEntryOut, len(entries))}
		for i, m := range entries {
			out.Entries[i] = toMemoryEntryOut(m)
		}
		return textResult(fmt.Sprintf("%d entr(ies) not reconfirmed in %d+ days", len(entries), days)), out, nil
	})
}

func addMemoryTouchTool(s *sdkmcp.Server, st *store.Store) {
	addTool(s, &sdkmcp.Tool{
		Name:        "acline_memory_touch",
		Description: "Reconfirm a memory entry is still true, resetting its decay clock (see acline_memory_decay). A person's decision: refused for an agent unless it presents the approval token; recorded as a memory_reconfirmed event.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args memoryRetireArgs) (*sdkmcp.CallToolResult, memoryIDOut, error) {
		if err := st.TouchMemoryWithToken(args.ID, args.Token); err != nil {
			return nil, memoryIDOut{}, err
		}
		return textResult(fmt.Sprintf("memory #%d reconfirmed", args.ID)), memoryIDOut{ID: args.ID}, nil
	})
}

func addMemoryForgetTool(s *sdkmcp.Server, st *store.Store) {
	addTool(s, &sdkmcp.Tool{
		Name:        "acline_memory_forget",
		Description: "Mark a memory entry stale, dropping it from context and the default list. Never deletes the row; the change is recorded as a memory_forgotten event. A person's decision: refused for an agent unless it presents the approval token.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args memoryRetireArgs) (*sdkmcp.CallToolResult, memoryIDOut, error) {
		if err := st.SetMemoryStaleWithToken(args.ID, true, args.Token); err != nil {
			return nil, memoryIDOut{}, err
		}
		return textResult(fmt.Sprintf("memory #%d marked stale", args.ID)), memoryIDOut{ID: args.ID}, nil
	})
}

// addMemoryReviewTool registers acline_memory_approve or acline_memory_reject.
func addMemoryReviewTool(s *sdkmcp.Server, st *store.Store, name, verb string, approve bool) {
	addTool(s, &sdkmcp.Tool{
		Name: name,
		Description: "Review a pending memory entry: " + verb + ". Entries written by an agent are held for review; " +
			"if this server's actor is an agent the call is refused -- an agent cannot review memory (a human must).",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args memoryReviewArgs) (*sdkmcp.CallToolResult, memoryIDOut, error) {
		if err := st.ReviewMemory(args.ID, approve, args.Token); err != nil {
			return nil, memoryIDOut{}, err
		}
		return textResult(fmt.Sprintf("memory #%d %s", args.ID, verb)), memoryIDOut{ID: args.ID}, nil
	})
}
