package mcp

import (
	"fmt"
	"strings"
	"sync"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"acline/internal/store"
)

// Every tool carries MCP annotations, so a client can show which calls only
// read and which change the store, and policyMiddleware reads the same
// classification: readOnlyTools (policy.go) is the one list of read-only tools.

// destructiveTools remove, retire or override something rather than only add
// to the store.
var destructiveTools = map[string]bool{
	"acline_memory_forget": true, "acline_memory_reject": true, "acline_decision_reject": true,
	"acline_decision_supersede": true, "acline_plan_reject": true, "acline_reject": true,
	"acline_task_done": true, "acline_session_end": true,
}

// idempotentTools have no further effect when repeated with the same arguments.
var idempotentTools = map[string]bool{
	"acline_task_set_status": true, "acline_memory_touch": true, "acline_criteria_check": true,
	"acline_feature_set_status": true, "acline_roadmap_update": true, "acline_task_assign": true,
	"acline_task_defer": true, "acline_decision_accept": true, "acline_spec_approve": true,
	"acline_dep_verify": true, "acline_task_update": true,
}

// openWorldTools reach outside the local store: semantic search sends the
// query to the embedding provider when one is configured.
var openWorldTools = map[string]bool{"acline_search": true}

// annotationsFor is the MCP annotation set for tool name.
func annotationsFor(name string) *sdkmcp.ToolAnnotations {
	open := openWorldTools[name]
	a := &sdkmcp.ToolAnnotations{
		Title:         strings.ReplaceAll(strings.TrimPrefix(name, "acline_"), "_", " "),
		ReadOnlyHint:  readOnlyTools[name],
		OpenWorldHint: &open,
	}
	if !a.ReadOnlyHint {
		destructive := destructiveTools[name]
		a.DestructiveHint = &destructive
		a.IdempotentHint = idempotentTools[name]
	}
	return a
}

// addTool is sdkmcp.AddTool with the tool's annotations filled in. Every tool
// in this package is registered through it.
func addTool[In, Out any](s *sdkmcp.Server, t *sdkmcp.Tool, h sdkmcp.ToolHandlerFor[In, Out]) {
	t.Annotations = annotationsFor(t.Name)
	registeredTools.Store(t.Name, true)
	sdkmcp.AddTool(s, t, h)
}

// registeredTools is every tool name addTool has seen (the same set for every
// server), so a toolset can remove what it does not include.
var registeredTools sync.Map

// decisionTools record a person's decision: approving, accepting, retiring,
// verifying. The store refuses an agent without the approval token either way;
// the capture toolset leaves them out so an agent client is not offered them.
var decisionTools = map[string]bool{
	"acline_approve": true, "acline_reject": true, "acline_decision_accept": true, "acline_decision_reject": true,
	"acline_decision_supersede": true, "acline_spec_approve": true, "acline_plan_approve": true, "acline_plan_reject": true,
	"acline_memory_approve": true, "acline_memory_reject": true, "acline_memory_forget": true, "acline_dep_verify": true,
	"acline_role_add": true,
}

// Toolsets a server can expose.
const (
	ToolsetRead    = "read"    // read-only tools
	ToolsetCapture = "capture" // read + recording work (tasks, checks, notes, drafts), no decisions
	ToolsetAll     = "all"     // everything, for a person's client (the VS Code extension)
)

// DefaultToolset is the toolset for a server acting as actor: an agent's client
// is offered reading and recording, a person's everything.
func DefaultToolset(actor store.Actor) string {
	if actor.Type == "agent" {
		return ToolsetCapture
	}
	return ToolsetAll
}

// applyToolset removes the tools toolset leaves out.
func applyToolset(s *sdkmcp.Server, toolset string) error {
	keep := func(string) bool { return true }
	switch toolset {
	case "", ToolsetAll:
		return nil
	case ToolsetRead:
		keep = func(name string) bool { return readOnlyTools[name] }
	case ToolsetCapture:
		keep = func(name string) bool { return !decisionTools[name] }
	default:
		return fmt.Errorf("unknown toolset %q (want %s, %s or %s)", toolset, ToolsetRead, ToolsetCapture, ToolsetAll)
	}
	var drop []string
	registeredTools.Range(func(k, _ any) bool {
		if name := k.(string); !keep(name) {
			drop = append(drop, name)
		}
		return true
	})
	s.RemoveTools(drop...)
	return nil
}
