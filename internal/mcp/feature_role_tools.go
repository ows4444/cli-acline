package mcp

import (
	"context"
	"errors"
	"fmt"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"acline/internal/app"
	"acline/internal/store"
)

type featureOut struct {
	ID            int64   `json:"id"`
	Name          string  `json:"name"`
	Status        string  `json:"status"`
	OwnerArea     *string `json:"owner_area,omitempty"`
	SourcePointer *string `json:"source_pointer,omitempty"`
	Description   *string `json:"description,omitempty"`
	ProjectID     *int64  `json:"project_id,omitempty"`
	UpdatedAt     string  `json:"updated_at"`
}

type featureListArgs struct {
	Status  string `json:"status,omitempty" jsonschema:"live|deprecated|removed|n_a (omit for all)"`
	Project string `json:"project,omitempty" jsonschema:"restrict to a project name"`
	Limit   int    `json:"limit,omitempty" jsonschema:"max results (default 100, capped at 500)"`
	Offset  int    `json:"offset,omitempty" jsonschema:"skip this many results (for paging)"`
}

type featureListOut struct {
	Features  []featureOut `json:"features"`
	Truncated bool         `json:"truncated,omitempty"`
}

type featureAddArgs struct {
	Name          string `json:"name" jsonschema:"the capability's name"`
	Status        string `json:"status,omitempty" jsonschema:"live|deprecated|removed|n_a (default live)"`
	OwnerArea     string `json:"owner_area,omitempty"`
	SourcePointer string `json:"source_pointer,omitempty" jsonschema:"where it lives in the code"`
	Description   string `json:"description,omitempty"`
	Project       string `json:"project,omitempty"`
}

type featureSetStatusArgs struct {
	ID     int64  `json:"id"`
	Status string `json:"status" jsonschema:"live|deprecated|removed|n_a"`
}

type roleAddArgs struct {
	Project     string `json:"project" jsonschema:"project name the role belongs to (required)"`
	Name        string `json:"name"`
	Kind        string `json:"kind,omitempty" jsonschema:"human|agent|both (default both)"`
	CanApprove  bool   `json:"can_approve,omitempty" jsonschema:"role's approvals satisfy the gate once the project enforces roles; needs the approval token when one is enabled"`
	StageOrder  *int64 `json:"stage_order,omitempty" jsonschema:"advisory pipeline position"`
	Description string `json:"description,omitempty"`
	Token       string `json:"token,omitempty" jsonschema:"human approval token; required only for can_approve roles when the store has one enabled. Never read from the server's environment."`
}

type taskAssignArgs struct {
	ID      int64  `json:"id" jsonschema:"the task id"`
	Role    string `json:"role" jsonschema:"role name"`
	Project string `json:"project,omitempty" jsonschema:"project to resolve the role in (default: the task's project)"`
}

type taskAssignOut struct {
	ID       int64   `json:"id"`
	Role     string  `json:"role"`
	Previous string  `json:"previous"`
	Next     *string `json:"next_hint,omitempty"`
}

func registerFeatureRoleTools(s *sdkmcp.Server, st *store.Store) {
	addFeatureListTool(s, st)
	addFeatureAddTool(s, st)
	addFeatureSetStatusTool(s, st)
	addRoleAddTool(s, st)
	addTaskAssignTool(s, st)
}

func addFeatureListTool(s *sdkmcp.Server, st *store.Store) {
	addTool(s, &sdkmcp.Tool{
		Name:        "acline_feature_list",
		Description: "List documented capabilities (features) and their lifecycle status.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args featureListArgs) (*sdkmcp.CallToolResult, featureListOut, error) {
		projectID, err := resolveProject(st, args.Project)
		if err != nil {
			return nil, featureListOut{}, err
		}
		list, err := st.ListFeatures(args.Status, projectID)
		if err != nil {
			return nil, featureListOut{}, err
		}
		page, truncated := paginate(list, args.Limit, args.Offset)
		out := featureListOut{Features: make([]featureOut, len(page)), Truncated: truncated}
		for i, f := range page {
			out.Features[i] = featureOut{
				ID: f.ID, Name: f.Name, Status: f.Status, OwnerArea: nullStrPtr(f.OwnerArea), SourcePointer: nullStrPtr(f.SourcePointer),
				Description: nullStrPtr(f.Description), ProjectID: nullIntPtr(f.ProjectID), UpdatedAt: f.UpdatedAt,
			}
		}
		return textResult(fmt.Sprintf("%d feature(s)", len(page))), out, nil
	})
}

func addFeatureAddTool(s *sdkmcp.Server, st *store.Store) {
	addTool(s, &sdkmcp.Tool{
		Name:        "acline_feature_add",
		Description: "Record or document a capability (status defaults to live).",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args featureAddArgs) (*sdkmcp.CallToolResult, idOut, error) {
		id, err := app.AddFeature(st, app.AddFeatureRequest{
			Name: args.Name, Status: args.Status, OwnerArea: args.OwnerArea, SourcePointer: args.SourcePointer,
			Description: args.Description, ProjectArg: args.Project,
		})
		if err != nil {
			return nil, idOut{}, err
		}
		return textResult(fmt.Sprintf("feature #%d recorded", id)), idOut{ID: id}, nil
	})
}

func addFeatureSetStatusTool(s *sdkmcp.Server, st *store.Store) {
	addTool(s, &sdkmcp.Tool{
		Name:        "acline_feature_set_status",
		Description: "Change a feature's lifecycle status (live|deprecated|removed|n_a); recorded in the audit trail.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args featureSetStatusArgs) (*sdkmcp.CallToolResult, idOut, error) {
		if err := st.SetFeatureStatus(args.ID, args.Status); err != nil {
			return nil, idOut{}, err
		}
		return textResult(fmt.Sprintf("feature #%d -> %s", args.ID, args.Status)), idOut{ID: args.ID}, nil
	})
}

func addRoleAddTool(s *sdkmcp.Server, st *store.Store) {
	addTool(s, &sdkmcp.Tool{
		Name: "acline_role_add",
		Description: "Add a project-scoped role. The built-in global roles already exist. Creating a can_approve role is as privileged as " +
			"approving, so it needs the human approval token when the store has one enabled.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args roleAddArgs) (*sdkmcp.CallToolResult, idOut, error) {
		id, err := app.AddRole(st, app.AddRoleRequest{
			Name: args.Name, Kind: args.Kind, CanApprove: args.CanApprove, StageOrder: args.StageOrder,
			Description: args.Description, Token: args.Token, ProjectArg: args.Project,
		})
		if errors.Is(err, app.ErrRoleNeedsProject) {
			return nil, idOut{}, fmt.Errorf("project is required: a role belongs to a project")
		}
		if err != nil {
			return nil, idOut{}, err
		}
		return textResult(fmt.Sprintf("role #%d added: %s", id, args.Name)), idOut{ID: id}, nil
	})
}

func addTaskAssignTool(s *sdkmcp.Server, st *store.Store) {
	addTool(s, &sdkmcp.Tool{
		Name:        "acline_task_assign",
		Description: "Set the task's current owning role (the workflow-stage handoff); records a role_assigned event and returns the advisory next role.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args taskAssignArgs) (*sdkmcp.CallToolResult, taskAssignOut, error) {
		res, err := app.AssignRole(st, app.AssignRoleRequest{TaskID: args.ID, Role: args.Role, ProjectArg: args.Project})
		if err != nil {
			return nil, taskAssignOut{}, err
		}
		out := taskAssignOut{ID: args.ID, Role: res.Role.Name, Previous: res.Previous, Next: res.Next}
		return textResult(fmt.Sprintf("task #%d assigned to role %s (was: %s)", args.ID, res.Role.Name, res.Previous)), out, nil
	})
}
