package mcp

import (
	"context"
	"database/sql"
	"fmt"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

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

	addTool(s, &sdkmcp.Tool{
		Name:        "acline_feature_add",
		Description: "Record or document a capability (status defaults to live).",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args featureAddArgs) (*sdkmcp.CallToolResult, idOut, error) {
		if args.Name == "" {
			return nil, idOut{}, fmt.Errorf("name is required")
		}
		projectID, err := resolveProject(st, args.Project)
		if err != nil {
			return nil, idOut{}, err
		}
		status := args.Status
		if status == "" {
			status = "live"
		}
		id, err := st.AddFeature(args.Name, status, store.FeatureOpts{
			OwnerArea: args.OwnerArea, SourcePointer: args.SourcePointer, Description: args.Description, ProjectID: projectID,
		})
		if err != nil {
			return nil, idOut{}, err
		}
		return textResult(fmt.Sprintf("feature #%d recorded", id)), idOut{ID: id}, nil
	})

	addTool(s, &sdkmcp.Tool{
		Name:        "acline_feature_set_status",
		Description: "Change a feature's lifecycle status (live|deprecated|removed|n_a); recorded in the audit trail.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args featureSetStatusArgs) (*sdkmcp.CallToolResult, idOut, error) {
		if err := st.SetFeatureStatus(args.ID, args.Status); err != nil {
			return nil, idOut{}, err
		}
		st.LogEventGlobal("feature_status_change", fmt.Sprintf("feature #%d -> %s", args.ID, args.Status))
		return textResult(fmt.Sprintf("feature #%d -> %s", args.ID, args.Status)), idOut{ID: args.ID}, nil
	})

	addTool(s, &sdkmcp.Tool{
		Name: "acline_role_add",
		Description: "Add a project-scoped role. The built-in global roles already exist. Creating a can_approve role is as privileged as " +
			"approving, so it needs the human approval token when the store has one enabled.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args roleAddArgs) (*sdkmcp.CallToolResult, idOut, error) {
		projectID, err := resolveProject(st, args.Project)
		if err != nil {
			return nil, idOut{}, err
		}
		if projectID == nil {
			return nil, idOut{}, fmt.Errorf("project is required: a role belongs to a project")
		}
		id, err := st.AddRoleWithToken(*projectID, args.Name, args.Kind, args.CanApprove, args.StageOrder, args.Description, args.Token)
		if err != nil {
			return nil, idOut{}, err
		}
		return textResult(fmt.Sprintf("role #%d added: %s", id, args.Name)), idOut{ID: id}, nil
	})

	addTool(s, &sdkmcp.Tool{
		Name:        "acline_task_assign",
		Description: "Set the task's current owning role (the workflow-stage handoff); records a role_assigned event and returns the advisory next role.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args taskAssignArgs) (*sdkmcp.CallToolResult, taskAssignOut, error) {
		t, err := st.GetTask(args.ID)
		if err != nil {
			return nil, taskAssignOut{}, err
		}
		projectID, err := resolveProject(st, args.Project)
		if err != nil {
			return nil, taskAssignOut{}, err
		}
		if projectID == nil {
			projectID = nullIntPtr(t.ProjectID)
		}
		role, err := st.GetRoleByName(projectID, args.Role)
		if err != nil {
			return nil, taskAssignOut{}, err
		}
		prev, err := st.AssignRole(args.ID, &role.ID)
		if err != nil {
			return nil, taskAssignOut{}, err
		}
		from := "unassigned"
		if prev.Valid {
			if pr, err := st.GetRole(prev.Int64); err == nil {
				from = pr.Name
			}
		}
		st.LogTaskEvent(args.ID, "role_assigned", fmt.Sprintf("%s -> %s", from, role.Name))
		out := taskAssignOut{ID: args.ID, Role: role.Name, Previous: from}
		if hint, err := st.NextRoleHint(projectID, sql.NullInt64{Int64: role.ID, Valid: true}); err == nil && hint != nil {
			out.Next = &hint.Name
		}
		return textResult(fmt.Sprintf("task #%d assigned to role %s (was: %s)", args.ID, role.Name, from)), out, nil
	})
}
