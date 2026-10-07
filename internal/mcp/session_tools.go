package mcp

import (
	"context"
	"fmt"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"acline/internal/app"
	"acline/internal/store"
)

type sessionStartArgs struct {
	TaskID     *int64   `json:"task_id,omitempty" jsonschema:"task to work on; it is moved to in_progress"`
	Project    string   `json:"project,omitempty" jsonschema:"project name (default: the task's project)"`
	Role       string   `json:"role,omitempty" jsonschema:"role name (default: $ACLINE_ROLE); persists as the session's role"`
	Label      string   `json:"policy_label,omitempty" jsonschema:"free-text label for what the session may touch"`
	AllowTools []string `json:"allow_tools,omitempty" jsonschema:"only these tools are permitted ('*' for any)"`
	DenyTools  []string `json:"deny_tools,omitempty" jsonschema:"these tools are explicitly denied"`
	AllowPaths []string `json:"allow_paths,omitempty" jsonschema:"only paths matching these globs are permitted"`
	DenyPaths  []string `json:"deny_paths,omitempty" jsonschema:"paths matching these globs are denied"`
}

type sessionStartOut struct {
	ID int64 `json:"id"`
	// Lessons are the approved pitfall/failure_pattern memory relevant to the
	// task, so the agent sees them the moment work starts.
	Lessons []memoryEntryOut `json:"lessons,omitempty"`
}

type sessionEndArgs struct {
	Summary   string  `json:"summary,omitempty"`
	TokensIn  int64   `json:"tokens_in,omitempty"`
	TokensOut int64   `json:"tokens_out,omitempty"`
	CostUSD   float64 `json:"cost_usd,omitempty"`
	Token     string  `json:"token,omitempty" jsonschema:"human approval token; required to end a session that carries a restrictive policy when the store has one enabled. Never read from the server's environment."`
}

type sessionEndOut struct {
	ID            int64 `json:"id"`
	PendingMemory int   `json:"pending_memory"`
}

func registerSessionLifecycleTools(s *sdkmcp.Server, st *store.Store) {
	addSessionStartTool(s, st)
	addSessionEndTool(s, st)
}

func addSessionStartTool(s *sdkmcp.Server, st *store.Store) {
	addTool(s, &sdkmcp.Tool{
		Name: "acline_session_start",
		Description: "Start a work session (at most one is active). With task_id the task moves to in_progress. " +
			"Allow/deny lists become the session's policy, which mutating tools are checked against.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args sessionStartArgs) (*sdkmcp.CallToolResult, sessionStartOut, error) {
		res, err := app.StartSession(st, app.StartSessionRequest{
			TaskID: args.TaskID, ProjectArg: args.Project, RoleArg: args.Role,
			Policy: store.Policy{Label: args.Label, AllowTools: args.AllowTools, DenyTools: args.DenyTools, AllowPaths: args.AllowPaths, DenyPaths: args.DenyPaths},
		})
		if err != nil {
			return nil, sessionStartOut{}, err
		}
		out := sessionStartOut{ID: res.ID}
		msg := fmt.Sprintf("session #%d started", res.ID)
		for _, m := range res.Lessons {
			out.Lessons = append(out.Lessons, toMemoryEntryOut(m))
			msg += fmt.Sprintf("\nlesson #%d [%s] %s", m.ID, m.Kind, m.Body)
		}
		return textResult(msg), out, nil
	})
}

func addSessionEndTool(s *sdkmcp.Server, st *store.Store) {
	addTool(s, &sdkmcp.Tool{
		Name: "acline_session_end",
		Description: "End the active session, recording a summary and token/cost totals. Ending a session discards its policy, so " +
			"a session with a restrictive policy needs the human approval token when the store has one enabled.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args sessionEndArgs) (*sdkmcp.CallToolResult, sessionEndOut, error) {
		res, err := app.EndSession(st, args.Summary, store.SessionCost{TokensIn: args.TokensIn, TokensOut: args.TokensOut, CostUSD: args.CostUSD}, args.Token)
		if err != nil {
			return nil, sessionEndOut{}, err
		}
		return textResult(fmt.Sprintf("session #%d ended", res.Session.ID)), sessionEndOut{ID: res.Session.ID, PendingMemory: res.PendingMemory}, nil
	})
}
