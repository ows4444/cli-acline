package mcp

import (
	"context"
	"fmt"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"acline/internal/store"
)

// CLI commands the VS Code extension needs that had no tool: changing a
// task's risk/autonomy/priority/area/type, listing a project's check runners,
// and showing one spec (with the text it had before each revision) or decision.

type taskUpdateArgs struct {
	ID       int64  `json:"id" jsonschema:"the task id"`
	Risk     string `json:"risk,omitempty" jsonschema:"low|medium|high|critical; lowering it needs a person (or the token)"`
	Autonomy string `json:"autonomy,omitempty" jsonschema:"hitl|hotl|auto; loosening it needs a person (or the token)"`
	Priority string `json:"priority,omitempty" jsonschema:"low|normal|high|urgent"`
	Area     string `json:"area,omitempty"`
	Type     string `json:"type,omitempty" jsonschema:"feature|bug|refactor|test|security|... (as acline task add --type)"`
	Token    string `json:"token,omitempty" jsonschema:"human approval token; needed to lower risk or loosen autonomy when the store has one enabled or the server's actor is an agent. Never read from the server's environment."`
}

type taskUpdateOut struct {
	ID      int64    `json:"id"`
	Changed []string `json:"changed"`
}

type checkRunnerListArgs struct {
	Project string `json:"project" jsonschema:"the project name"`
}

type checkRunnerOut struct {
	Kind      string  `json:"kind"`
	Command   string  `json:"command"`
	ActorID   *string `json:"actor_id,omitempty"`
	CreatedAt string  `json:"created_at"`
}

type checkRunnerListOut struct {
	Runners []checkRunnerOut `json:"runners"`
}

type specShowArgs struct {
	ID int64 `json:"id" jsonschema:"the spec id"`
}

type specVersionOut struct {
	Version   int    `json:"version"`
	Title     string `json:"title"`
	Body      string `json:"body,omitempty"`
	Status    string `json:"status"`
	ActorID   string `json:"actor_id,omitempty"`
	CreatedAt string `json:"created_at"`
}

type specShowOut struct {
	Spec specOut `json:"spec"`
	// Versions is what the spec said before each revision, oldest first.
	Versions []specVersionOut `json:"versions"`
}

type decisionShowArgs struct {
	ID int64 `json:"id" jsonschema:"the decision id"`
}

func registerParityTools(s *sdkmcp.Server, st *store.Store) {
	addTool(s, &sdkmcp.Tool{
		Name: "acline_task_update",
		Description: "Change a task's risk, autonomy, priority, area or type (only the fields given). Raising risk and tightening " +
			"autonomy are always allowed; lowering risk or loosening autonomy needs a person (or the approval token). " +
			"Every change is recorded. Fields apply in the order risk, autonomy, priority, area, type, and a refused one stops the update there.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args taskUpdateArgs) (*sdkmcp.CallToolResult, taskUpdateOut, error) {
		if _, err := st.GetTask(args.ID); err != nil {
			return nil, taskUpdateOut{}, err
		}
		steps := []struct {
			name, value string
			apply       func() error
		}{
			{"risk", args.Risk, func() error { return st.UpdateTaskRiskWithToken(args.ID, args.Risk, args.Token) }},
			{"autonomy", args.Autonomy, func() error { return st.UpdateTaskAutonomyWithToken(args.ID, args.Autonomy, args.Token) }},
			{"priority", args.Priority, func() error { return st.UpdateTaskPriority(args.ID, args.Priority) }},
			{"area", args.Area, func() error { return st.UpdateTaskArea(args.ID, args.Area) }},
			{"type", args.Type, func() error { return st.UpdateTaskType(args.ID, args.Type) }},
		}
		out := taskUpdateOut{ID: args.ID, Changed: []string{}}
		for _, step := range steps {
			if step.value == "" {
				continue
			}
			if err := step.apply(); err != nil {
				return nil, taskUpdateOut{}, err
			}
			out.Changed = append(out.Changed, step.name+"="+step.value)
		}
		if len(out.Changed) == 0 {
			return nil, taskUpdateOut{}, fmt.Errorf("nothing to update: give at least one of risk, autonomy, priority, area, type")
		}
		return textResult(fmt.Sprintf("task #%d: %s", args.ID, strings.Join(out.Changed, ", "))), out, nil
	})

	addTool(s, &sdkmcp.Tool{
		Name:        "acline_check_runner_list",
		Description: "List the check runners a person configured for a project (the commands acline_check_run executes). Kinds with none use the defaults (Go projects only).",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args checkRunnerListArgs) (*sdkmcp.CallToolResult, checkRunnerListOut, error) {
		if args.Project == "" {
			return nil, checkRunnerListOut{}, fmt.Errorf("project is required")
		}
		pid, err := resolveProject(st, args.Project)
		if err != nil {
			return nil, checkRunnerListOut{}, err
		}
		list, err := st.ListCheckRunners(*pid)
		if err != nil {
			return nil, checkRunnerListOut{}, err
		}
		out := checkRunnerListOut{Runners: make([]checkRunnerOut, len(list))}
		for i, r := range list {
			out.Runners[i] = checkRunnerOut{Kind: r.Kind, Command: r.Command, ActorID: nullStrPtr(r.ActorID), CreatedAt: r.CreatedAt}
		}
		return textResult(fmt.Sprintf("%d runner(s) configured for %s", len(list), args.Project)), out, nil
	})

	addTool(s, &sdkmcp.Tool{
		Name:        "acline_spec_show",
		Description: "Show one spec, with what it said before each revision (oldest first). Revising an approved spec withdraws its approval, so the versions show what was approved.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args specShowArgs) (*sdkmcp.CallToolResult, specShowOut, error) {
		sp, err := st.GetSpec(args.ID)
		if err != nil {
			return nil, specShowOut{}, err
		}
		versions, err := st.ListSpecVersions(args.ID)
		if err != nil {
			return nil, specShowOut{}, err
		}
		out := specShowOut{Spec: toSpecOut(*sp), Versions: make([]specVersionOut, len(versions))}
		for i, v := range versions {
			out.Versions[i] = specVersionOut{Version: v.Version, Title: v.Title, Body: v.Body, Status: v.Status, ActorID: v.ActorID, CreatedAt: v.CreatedAt}
		}
		return textResult(fmt.Sprintf("spec #%d v%d (%s), %d earlier version(s)", sp.ID, sp.Version, sp.Status, len(versions))), out, nil
	})

	addTool(s, &sdkmcp.Tool{
		Name:        "acline_decision_show",
		Description: "Show one ADR-style decision by id.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args decisionShowArgs) (*sdkmcp.CallToolResult, decisionOut, error) {
		d, err := st.GetDecision(args.ID)
		if err != nil {
			return nil, decisionOut{}, err
		}
		return textResult(fmt.Sprintf("decision #%d %s (%s)", d.ID, d.Title, d.Status)), toDecisionOut(*d), nil
	})
}
