package mcp

import (
	"context"
	"fmt"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"acline/internal/store"
)

// readOnlyTools never change state, so a session policy does not gate them: a
// policy like `--allow-tools Read,Edit` written for an agent's file tools must
// not blank out an editor's views of the store.
var readOnlyTools = map[string]bool{
	"acline_search": true, "acline_memory_list": true, "acline_memory_decay": true, "acline_task_list": true,
	"acline_task_gate": true, "acline_decision_list": true, "acline_spec_list": true, "acline_check_list": true,
	"acline_dep_list": true, "acline_dashboard": true, "acline_metrics": true, "acline_verify": true,
	"acline_project_list": true, "acline_project_resolve": true, "acline_role_list": true, "acline_session_current": true, "acline_session_list": true,
	"acline_eval_list": true, "acline_roadmap_list": true, "acline_roadmap_show": true, "acline_server_info": true,
	"acline_plan_list": true, "acline_plan_show": true, "acline_task_brief": true, "acline_task_route": true,
	"acline_criteria_list": true, "acline_note_list": true, "acline_feature_list": true, "acline_policy_show": true,
	"acline_check_runner_list": true, "acline_spec_show": true, "acline_decision_show": true,
}

// policyMiddleware makes MCP tool calls answer to the active session's policy
// (`acline session start --allow-tools/--deny-tools`), as `guard check-tool`
// already does for Claude Code's built-in tools. Previously a client could call
// acline_task_done, acline_approve, acline_memory_add… with no policy check and
// no policy_violation trail. Only mutating tools are checked, by their MCP name
// (e.g. deny_tools "acline_task_done"); a denial is recorded by CheckPolicy.
func policyMiddleware(st *store.Store) sdkmcp.Middleware {
	return func(next sdkmcp.MethodHandler) sdkmcp.MethodHandler {
		return func(ctx context.Context, method string, req sdkmcp.Request) (sdkmcp.Result, error) {
			if method == "tools/call" {
				if call, ok := req.(*sdkmcp.CallToolRequest); ok && call.Params != nil {
					name := call.Params.Name
					if strings.HasPrefix(name, "acline_") && !readOnlyTools[name] {
						allowed, reason, err := st.CheckPolicy(name, "")
						if err != nil && err != store.ErrNoActiveSession {
							return nil, err
						}
						if err == nil && !allowed {
							return nil, fmt.Errorf("blocked by the active session policy: %s", reason)
						}
					}
				}
			}
			return next(ctx, method, req)
		}
	}
}

type policyShowOut struct {
	Label      string   `json:"label,omitempty"`
	AllowTools []string `json:"allow_tools,omitempty"`
	DenyTools  []string `json:"deny_tools,omitempty"`
	AllowPaths []string `json:"allow_paths,omitempty"`
	DenyPaths  []string `json:"deny_paths,omitempty"`
	Restricted bool     `json:"restricted"`
}

// registerPolicyTools gives `acline policy show` an MCP counterpart
// (read-only, like the other 7 tree-view-backing commands) had no MCP/extension
// counterpart, unlike `acline policy check` -- which stays CLI/hook-only, since
// it's meant to be called by a harness's pre-tool-use hook and asserted on its
// exit code, not something an editor view would invoke.
func registerPolicyTools(s *sdkmcp.Server, st *store.Store) {
	addTool(s, &sdkmcp.Tool{
		Name:        "acline_policy_show",
		Description: "Show the active session's parsed policy (allow/deny tools and paths). Same view `acline policy show` prints.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args struct{}) (*sdkmcp.CallToolResult, policyShowOut, error) {
		sess, err := st.CurrentSession()
		if err != nil {
			return nil, policyShowOut{}, err
		}
		p := store.ParsePolicy(sess.Policy.String)
		out := policyShowOut{
			Label: p.Label, AllowTools: p.AllowTools, DenyTools: p.DenyTools,
			AllowPaths: p.AllowPaths, DenyPaths: p.DenyPaths,
			Restricted: len(p.AllowTools) > 0 || len(p.DenyTools) > 0 || len(p.AllowPaths) > 0 || len(p.DenyPaths) > 0,
		}
		summary := "no restrictions recorded -- this session is unrestricted by policy"
		if out.Restricted {
			summary = fmt.Sprintf("session #%d: %d allow-tool(s), %d deny-tool(s), %d allow-path(s), %d deny-path(s)",
				sess.ID, len(p.AllowTools), len(p.DenyTools), len(p.AllowPaths), len(p.DenyPaths))
		}
		return textResult(summary), out, nil
	})
}
