package mcp

import (
	"context"
	"fmt"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"acline/internal/store"
)

type dependencyOut struct {
	ID        int64   `json:"id"`
	TaskID    *int64  `json:"task_id,omitempty"`
	ProjectID *int64  `json:"project_id,omitempty"`
	Ecosystem string  `json:"ecosystem"`
	Name      string  `json:"name"`
	Version   *string `json:"version,omitempty"`
	Verified  bool    `json:"verified"`
	CreatedAt string  `json:"created_at"`
}

func toDependencyOut(d store.Dependency) dependencyOut {
	return dependencyOut{
		ID: d.ID, TaskID: nullIntPtr(d.TaskID), ProjectID: nullIntPtr(d.ProjectID),
		Ecosystem: d.Ecosystem, Name: d.Name, Version: nullStrPtr(d.Version), Verified: d.Verified, CreatedAt: d.CreatedAt,
	}
}

type depAddArgs struct {
	Ecosystem string `json:"ecosystem" jsonschema:"e.g. npm, pypi, go, cargo"`
	Name      string `json:"name" jsonschema:"package name"`
	Version   string `json:"version,omitempty"`
	TaskID    *int64 `json:"task_id,omitempty" jsonschema:"the task that introduced this dependency"`
	Project   string `json:"project,omitempty" jsonschema:"project name to scope this dependency to"`
	Verified  bool   `json:"verified,omitempty" jsonschema:"the package is confirmed to exist and be the real published artifact (default false -- unverified). Only a person, or a holder of the approval token, may set this."`
	Token     string `json:"token,omitempty" jsonschema:"human approval token; needed with verified=true when the store has one enabled or the server's actor is an agent"`
}

type depVerifyArgs struct {
	ID    int64  `json:"id" jsonschema:"the dependency id"`
	Token string `json:"token,omitempty" jsonschema:"human approval token; needed when the store has one enabled or the server's actor is an agent"`
}

type depAddOut struct {
	ID       int64 `json:"id"`
	Verified bool  `json:"verified"`
}

type depListArgs struct {
	Project        string `json:"project,omitempty" jsonschema:"restrict to a project name"`
	UnverifiedOnly bool   `json:"unverified_only,omitempty" jsonschema:"show only unverified dependencies"`
	Limit          int    `json:"limit,omitempty" jsonschema:"max results (default 100, capped at 500)"`
	Offset         int    `json:"offset,omitempty" jsonschema:"skip this many results (for paging)"`
}

type depListOut struct {
	Dependencies []dependencyOut `json:"dependencies"`
	Truncated    bool            `json:"truncated,omitempty"`
}

type depVerifyOut struct {
	ID int64 `json:"id"`
}

func registerDepTools(s *sdkmcp.Server, st *store.Store) {
	addTool(s, &sdkmcp.Tool{
		Name: "acline_dep_add",
		Description: "Record a package introduced into the project (supply-chain provenance). Lands " +
			"unverified by default -- confirm the package exists and is the real published artifact " +
			"(not a hallucinated or typosquatted name) before installing, then verify=true or " +
			"acline_dep_verify it.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args depAddArgs) (*sdkmcp.CallToolResult, depAddOut, error) {
		if args.Ecosystem == "" || args.Name == "" {
			return nil, depAddOut{}, fmt.Errorf("ecosystem and name are required")
		}
		if args.TaskID != nil {
			if _, err := st.GetTask(*args.TaskID); err != nil {
				return nil, depAddOut{}, err
			}
		}
		projectID, err := resolveProject(st, args.Project)
		if err != nil {
			return nil, depAddOut{}, err
		}
		id, err := st.AddDependencyWithToken(args.TaskID, projectID, args.Ecosystem, args.Name, args.Version, args.Verified, args.Token)
		if err != nil {
			return nil, depAddOut{}, err
		}
		message := fmt.Sprintf("%s %s@%s", args.Ecosystem, args.Name, args.Version)
		if args.TaskID != nil {
			st.LogTaskEvent(*args.TaskID, "dependency_added", message)
		} else {
			st.LogEventGlobal("dependency_added", message)
		}
		summary := fmt.Sprintf("dependency #%d recorded: %s", id, message)
		if !args.Verified {
			summary += " (unverified -- confirm it's the real published artifact before installing)"
		}
		return textResult(summary), depAddOut{ID: id, Verified: args.Verified}, nil
	})

	addTool(s, &sdkmcp.Tool{
		Name:        "acline_dep_list",
		Description: "List recorded dependencies, optionally restricted to a project or to unverified ones.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args depListArgs) (*sdkmcp.CallToolResult, depListOut, error) {
		projectID, err := resolveProject(st, args.Project)
		if err != nil {
			return nil, depListOut{}, err
		}
		deps, err := st.ListDependencies(projectID, args.UnverifiedOnly)
		if err != nil {
			return nil, depListOut{}, err
		}
		page, truncated := paginate(deps, args.Limit, args.Offset)
		out := depListOut{Dependencies: make([]dependencyOut, len(page)), Truncated: truncated}
		for i, d := range page {
			out.Dependencies[i] = toDependencyOut(d)
		}
		return textResult(fmt.Sprintf("%d dependenc(ies)", len(page))), out, nil
	})

	addTool(s, &sdkmcp.Tool{
		Name:        "acline_dep_verify",
		Description: "Mark a dependency verified as the real published artifact. A person's confirmation: refused for an agent unless it presents the approval token.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args depVerifyArgs) (*sdkmcp.CallToolResult, depVerifyOut, error) {
		if err := st.VerifyDependencyWithToken(args.ID, args.Token); err != nil {
			return nil, depVerifyOut{}, err
		}
		return textResult(fmt.Sprintf("dependency #%d verified", args.ID)), depVerifyOut{ID: args.ID}, nil
	})
}
