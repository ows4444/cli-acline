package mcp

import (
	"context"
	"errors"
	"fmt"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"acline/internal/store"
)

// This file closes the read-side MCP gap flagged in the acline CLI refactor
// review: role, session, eval, and roadmap all have full CLI commands
// (internal/cmd/role.go, session.go, eval.go, roadmap.go) but no MCP
// counterpart, so a client wired to acline purely over MCP (no shell access
// to the `acline` binary) could create tasks and record checks but couldn't
// see whose role a task is on, check the active session, see a suite's
// measured pass rate, or see milestone progress. Only read tools are added
// here -- mutating operations (role add, eval promote, session start/end)
// stay CLI/hook-only until a concrete MCP-only workflow needs them, so the
// surface grows only as far as the actual gap, not further.

type roleOut struct {
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	Kind        string  `json:"kind"`
	CanApprove  bool    `json:"can_approve"`
	StageOrder  *int64  `json:"stage_order,omitempty"`
	Description *string `json:"description,omitempty"`
	ProjectID   *int64  `json:"project_id,omitempty"`
}

func toRoleOut(r store.Role) roleOut {
	return roleOut{
		ID: r.ID, Name: r.Name, Kind: r.Kind, CanApprove: r.CanApprove,
		StageOrder: nullIntPtr(r.StageOrder), Description: nullStrPtr(r.Description),
		ProjectID: nullIntPtr(r.ProjectID),
	}
}

type roleListArgs struct {
	Project string `json:"project,omitempty" jsonschema:"restrict to a project's own roles, in addition to the global built-ins"`
}

type roleListOut struct {
	Roles []roleOut `json:"roles"`
}

func registerRoleTools(s *sdkmcp.Server, st *store.Store) {
	addTool(s, &sdkmcp.Tool{
		Name:        "acline_role_list",
		Description: "List roles: the global built-ins (developer/qa/designer/manager/scrummaster/architect/security) plus a project's own additions.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args roleListArgs) (*sdkmcp.CallToolResult, roleListOut, error) {
		projectID, err := resolveProject(st, args.Project)
		if err != nil {
			return nil, roleListOut{}, err
		}
		roles, err := st.ListRoles(projectID)
		if err != nil {
			return nil, roleListOut{}, err
		}
		out := roleListOut{Roles: make([]roleOut, len(roles))}
		for i, r := range roles {
			out.Roles[i] = toRoleOut(r)
		}
		return textResult(fmt.Sprintf("%d role(s)", len(roles))), out, nil
	})
}

// sessionDetailOut is a fuller session projection than dashboard.go's
// sessionOut (which only carries the four fields the dashboard view needs)
// -- named distinctly to avoid colliding with that existing type.
type sessionDetailOut struct {
	ID        int64   `json:"id"`
	TaskID    *int64  `json:"task_id,omitempty"`
	ProjectID *int64  `json:"project_id,omitempty"`
	ActorType *string `json:"actor_type,omitempty"`
	ActorID   *string `json:"actor_id,omitempty"`
	Model     *string `json:"model,omitempty"`
	RoleID    *int64  `json:"role_id,omitempty"`
	StartedAt string  `json:"started_at"`
	EndedAt   *string `json:"ended_at,omitempty"`
	Summary   *string `json:"summary,omitempty"`
}

func toSessionDetailOut(sess store.Session) sessionDetailOut {
	return sessionDetailOut{
		ID: sess.ID, TaskID: nullIntPtr(sess.TaskID), ProjectID: nullIntPtr(sess.ProjectID),
		ActorType: nullStrPtr(sess.ActorType), ActorID: nullStrPtr(sess.ActorID), Model: nullStrPtr(sess.Model),
		RoleID: nullIntPtr(sess.RoleID), StartedAt: sess.StartedAt,
		EndedAt: nullStrPtr(sess.EndedAt), Summary: nullStrPtr(sess.Summary),
	}
}

type sessionCurrentOut struct {
	Active  bool              `json:"active"`
	Session *sessionDetailOut `json:"session,omitempty"`
}

func registerSessionTools(s *sdkmcp.Server, st *store.Store) {
	addTool(s, &sdkmcp.Tool{
		Name:        "acline_session_current",
		Description: "Show the active work session, if any. Reports active=false rather than erroring when nothing is running.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args struct{}) (*sdkmcp.CallToolResult, sessionCurrentOut, error) {
		sess, err := st.CurrentSession()
		if errors.Is(err, store.ErrNoActiveSession) {
			return textResult("no active session"), sessionCurrentOut{Active: false}, nil
		}
		if err != nil {
			return nil, sessionCurrentOut{}, err
		}
		out := toSessionDetailOut(*sess)
		return textResult(fmt.Sprintf("session #%d active", sess.ID)), sessionCurrentOut{Active: true, Session: &out}, nil
	})

	addTool(s, &sdkmcp.Tool{
		Name:        "acline_session_list",
		Description: "List recent work sessions, optionally restricted to a project.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args sessionListArgs) (*sdkmcp.CallToolResult, sessionListOut, error) {
		projectID, err := resolveProject(st, args.Project)
		if err != nil {
			return nil, sessionListOut{}, err
		}
		sessions, err := st.ListSessions(projectID, args.Limit)
		if err != nil {
			return nil, sessionListOut{}, err
		}
		out := sessionListOut{Sessions: make([]sessionDetailOut, len(sessions))}
		for i, sess := range sessions {
			out.Sessions[i] = toSessionDetailOut(sess)
		}
		return textResult(fmt.Sprintf("%d session(s)", len(sessions))), out, nil
	})
}

type sessionListArgs struct {
	Project string `json:"project,omitempty" jsonschema:"restrict to a project name"`
	Limit   int    `json:"limit,omitempty" jsonschema:"max results (default 50)"`
}

type sessionListOut struct {
	Sessions []sessionDetailOut `json:"sessions"`
}

type evalOut struct {
	ID         int64   `json:"id"`
	TaskID     *int64  `json:"task_id,omitempty"`
	ProjectID  *int64  `json:"project_id,omitempty"`
	Suite      string  `json:"suite"`
	PassRate   float64 `json:"pass_rate"`
	SampleSize *int64  `json:"sample_size,omitempty"`
	Note       *string `json:"note,omitempty"`
	CreatedAt  string  `json:"created_at"`
}

func toEvalOut(e store.Eval) evalOut {
	return evalOut{
		ID: e.ID, TaskID: nullIntPtr(e.TaskID), ProjectID: nullIntPtr(e.ProjectID),
		Suite: e.Suite, PassRate: e.PassRate, SampleSize: nullIntPtr(e.SampleSize),
		Note: nullStrPtr(e.Note), CreatedAt: e.CreatedAt,
	}
}

type evalListArgs struct {
	Project string `json:"project,omitempty" jsonschema:"restrict to a project name"`
	Suite   string `json:"suite,omitempty" jsonschema:"restrict to one suite"`
	Limit   int    `json:"limit,omitempty" jsonschema:"max results (default 50)"`
}

type evalListOut struct {
	Evals []evalOut `json:"evals"`
}

func registerEvalTools(s *sdkmcp.Server, st *store.Store) {
	addTool(s, &sdkmcp.Tool{
		Name:        "acline_eval_list",
		Description: "List recorded eval results (measured pass rate per suite), newest first -- the evidence behind an autonomy promotion.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args evalListArgs) (*sdkmcp.CallToolResult, evalListOut, error) {
		projectID, err := resolveProject(st, args.Project)
		if err != nil {
			return nil, evalListOut{}, err
		}
		evals, err := st.ListEvals(projectID, args.Suite, args.Limit)
		if err != nil {
			return nil, evalListOut{}, err
		}
		out := evalListOut{Evals: make([]evalOut, len(evals))}
		for i, e := range evals {
			out.Evals[i] = toEvalOut(e)
		}
		return textResult(fmt.Sprintf("%d eval(s)", len(evals))), out, nil
	})
}

type roadmapOut struct {
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
	Status      string  `json:"status"`
	TargetDate  *string `json:"target_date,omitempty"`
	ProjectID   *int64  `json:"project_id,omitempty"`
	Total       int     `json:"tasks_total"`
	Done        int     `json:"tasks_done"`
	Cancelled   int     `json:"tasks_cancelled"`
}

func toRoadmapOut(m store.Milestone, p store.MilestoneProgress) roadmapOut {
	return roadmapOut{
		ID: m.ID, Name: m.Name, Description: nullStrPtr(m.Description), Status: m.Status,
		TargetDate: nullStrPtr(m.TargetDate), ProjectID: nullIntPtr(m.ProjectID),
		Total: p.Total, Done: p.Done, Cancelled: p.Cancelled,
	}
}

type roadmapListArgs struct {
	Status  string `json:"status,omitempty" jsonschema:"planned|active|done|cancelled"`
	Project string `json:"project,omitempty" jsonschema:"restrict to a project name"`
}

type roadmapListOut struct {
	Milestones []roadmapOut `json:"milestones"`
}

type roadmapShowOut struct {
	Milestone roadmapOut `json:"milestone"`
	Tasks     []taskOut  `json:"tasks"`
}

type roadmapUpdateArgs struct {
	ID     int64  `json:"id" jsonschema:"the milestone id"`
	Status string `json:"status,omitempty" jsonschema:"planned|active|done|cancelled"`
	Target string `json:"target,omitempty" jsonschema:"target date, e.g. 2026-12-01"`
}

type roadmapUpdateOut struct {
	ID int64 `json:"id"`
}

func registerRoadmapTools(s *sdkmcp.Server, st *store.Store) {
	addTool(s, &sdkmcp.Tool{
		Name:        "acline_roadmap_list",
		Description: "List milestones with task progress, optionally filtered by status or project.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args roadmapListArgs) (*sdkmcp.CallToolResult, roadmapListOut, error) {
		projectID, err := resolveProject(st, args.Project)
		if err != nil {
			return nil, roadmapListOut{}, err
		}
		milestones, err := st.ListMilestones(args.Status, projectID)
		if err != nil {
			return nil, roadmapListOut{}, err
		}
		out := roadmapListOut{Milestones: make([]roadmapOut, len(milestones))}
		for i, m := range milestones {
			progress, err := st.GetMilestoneProgress(m.ID)
			if err != nil {
				return nil, roadmapListOut{}, err
			}
			out.Milestones[i] = toRoadmapOut(m, progress)
		}
		return textResult(fmt.Sprintf("%d milestone(s)", len(milestones))), out, nil
	})

	addTool(s, &sdkmcp.Tool{
		Name:        "acline_roadmap_show",
		Description: "Show one milestone's progress and the tasks assigned to it.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args idArgs) (*sdkmcp.CallToolResult, roadmapShowOut, error) {
		m, err := st.GetMilestone(args.ID)
		if err != nil {
			return nil, roadmapShowOut{}, err
		}
		progress, err := st.GetMilestoneProgress(args.ID)
		if err != nil {
			return nil, roadmapShowOut{}, err
		}
		tasks, err := st.MilestoneTasks(args.ID)
		if err != nil {
			return nil, roadmapShowOut{}, err
		}
		out := roadmapShowOut{Milestone: toRoadmapOut(*m, progress), Tasks: make([]taskOut, len(tasks))}
		for i, t := range tasks {
			out.Tasks[i] = toTaskOut(t)
		}
		return textResult(fmt.Sprintf("milestone #%d: %d/%d tasks done", m.ID, progress.Done, progress.Total)), out, nil
	})

	addTool(s, &sdkmcp.Tool{
		Name:        "acline_roadmap_update",
		Description: "Update a milestone's status and/or target date. Same as `acline roadmap update`.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args roadmapUpdateArgs) (*sdkmcp.CallToolResult, roadmapUpdateOut, error) {
		if args.Status == "" && args.Target == "" {
			return nil, roadmapUpdateOut{}, fmt.Errorf("nothing to update: pass status and/or target")
		}
		if args.Status != "" {
			if err := st.SetMilestoneStatus(args.ID, args.Status); err != nil {
				return nil, roadmapUpdateOut{}, err
			}
			st.LogEventGlobal("milestone_status_change", fmt.Sprintf("milestone #%d -> %s", args.ID, args.Status))
		}
		if args.Target != "" {
			if err := st.SetMilestoneTarget(args.ID, args.Target); err != nil {
				return nil, roadmapUpdateOut{}, err
			}
			st.LogEventGlobal("milestone_target_change", fmt.Sprintf("milestone #%d target -> %s", args.ID, args.Target))
		}
		return textResult(fmt.Sprintf("milestone #%d updated", args.ID)), roadmapUpdateOut{ID: args.ID}, nil
	})
}
