package mcp

import (
	"context"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"acline/internal/brief"
	"acline/internal/store"
)

type taskBriefArgs struct {
	ID      *int64 `json:"id,omitempty" jsonschema:"the task id; omit to brief the task acline_task_route would pick"`
	Project string `json:"project,omitempty" jsonschema:"restrict the pick to a project name (only when id is omitted)"`
}

type taskBriefOut struct {
	Found      bool    `json:"found"`
	TaskID     int64   `json:"task_id,omitempty"`
	Action     string  `json:"action,omitempty"`
	NeedsHuman bool    `json:"needs_human,omitempty"`
	Role       *string `json:"role,omitempty"`
	// Markdown is the whole brief: the step, task, spec, criteria, current
	// state, lessons, role contract and standing rules.
	Markdown string `json:"markdown,omitempty"`
}

func registerBriefTool(s *sdkmcp.Server, st *store.Store) {
	addTool(s, &sdkmcp.Tool{
		Name: "acline_task_brief",
		Description: "Assemble everything needed for a task's next step into one Markdown document: the routed step and who should take it, " +
			"the task, spec, decision, acceptance criteria, failing checks, approved lessons, the role's behavior contract and the standing rules. " +
			"Read-only. Omit id to brief the task acline_task_route would pick.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args taskBriefArgs) (*sdkmcp.CallToolResult, taskBriefOut, error) {
		var id int64
		if args.ID != nil {
			id = *args.ID
		} else {
			projectID, err := resolveProject(st, args.Project)
			if err != nil {
				return nil, taskBriefOut{}, err
			}
			next, err := st.NextTask(projectID)
			if err != nil {
				return nil, taskBriefOut{}, err
			}
			if next == nil {
				return textResult("no open tasks"), taskBriefOut{}, nil
			}
			id = next.TaskID
		}
		b, err := brief.Build(st, id)
		if err != nil {
			return nil, taskBriefOut{}, err
		}
		out := taskBriefOut{Found: true, TaskID: id, Action: b.Route.Action, NeedsHuman: b.Route.NeedsHuman, Markdown: b.Markdown()}
		if b.Route.Role != nil {
			out.Role = &b.Route.Role.Name
		}
		return textResult(out.Markdown), out, nil
	})
}
