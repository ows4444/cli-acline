// Package mcp exposes acline's store as an MCP (Model Context Protocol)
// server, so any MCP-compatible client (Claude Desktop, other agents, a
// VS Code extension) can search/browse/capture acline state the same way
// Claude Code's hooks do via the CLI, without shelling out to it. It talks
// to internal/store directly — a third consumer of the same store API
// alongside internal/cmd — rather than depending on internal/cmd, which
// owns cobra/global CLI state this package has no business touching.
//
// Gate/approval flows (task done --force, approve, reject — gate.go) got
// their own dedicated pass rather than being folded into the initial cut:
// they carry actor-identity rules (an agent can't approve its own work; an
// override is recorded, never silent) that mirror their CLI counterparts
// (taskDoneCmd/approveCmd/rejectCmd in cmd/task.go and cmd/gate.go)
// exactly, rather than reimplementing that policy from scratch.
package mcp

import (
	"context"
	"database/sql"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"acline/internal/app"
	"acline/internal/store"
)

// Version is the MCP server's own version, independent of acline's CLI
// version — bump it when the tool/resource surface changes.
const Version = "0.2.0"

// ProtocolVersion is the tool contract between this server and the VS Code
// extension: bump it whenever a tool's arguments or results change in a way an
// older client would misread. It is generated into the extension
// (ACLINE_PROTOCOL_VERSION in generated.ts), which compares it with what
// acline_server_info reports, so the two sides agree on one number instead of
// comparing unrelated version strings.
const ProtocolVersion = 2 // 2: acline_metrics and acline_plan_list take a project

// NewServer builds an MCP server backed by st. st must already be open
// (and have Embedder wired up if semantic search should be available) —
// this package never opens or configures the store itself, matching how
// internal/cmd's rootCmd does that once and hands the result down.
func NewServer(st *store.Store) *sdkmcp.Server {
	s, _ := NewServerWithToolset(st, ToolsetAll)
	return s
}

// NewServerWithToolset is NewServer exposing only toolset (ToolsetRead,
// ToolsetCapture or ToolsetAll; see DefaultToolset).
func NewServerWithToolset(st *store.Store, toolset string) (*sdkmcp.Server, error) {
	s := newServer(st)
	if err := applyToolset(s, toolset); err != nil {
		return nil, err
	}
	return s, nil
}

func newServer(st *store.Store) *sdkmcp.Server {
	s := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "acline", Version: Version}, &sdkmcp.ServerOptions{
		// Clients may subscribe to acline://events to hear that the store changed
		// (see WatchStore); the SDK keeps the subscription lists.
		SubscribeHandler:   func(context.Context, *sdkmcp.SubscribeRequest) error { return nil },
		UnsubscribeHandler: func(context.Context, *sdkmcp.UnsubscribeRequest) error { return nil },
	})

	s.AddReceivingMiddleware(policyMiddleware(st), errorCodeMiddleware(), versionMiddleware())

	registerSearchTool(s, st)
	registerMemoryTools(s, st)
	registerTaskTools(s, st)
	registerGateTools(s, st)
	registerRouteTool(s, st)
	registerBriefTool(s, st)
	registerPlanTools(s, st)
	registerTaskWorkflowTools(s, st)
	registerParityTools(s, st)
	registerCheckTools(s, st)
	registerDecisionTools(s, st)
	registerSpecTools(s, st)
	registerDepTools(s, st)
	registerNoteAndLogTools(s, st)
	registerDashboardTools(s, st)
	registerRoleTools(s, st)
	registerSessionTools(s, st)
	registerSessionLifecycleTools(s, st)
	registerEvalTools(s, st)
	registerNoteEvalTools(s, st)
	registerFeatureRoleTools(s, st)
	registerRoadmapTools(s, st)
	registerPolicyTools(s, st)
	registerServerInfoTool(s, st)
	registerEventsResource(s, st)

	return s
}

// resolveProject looks a project name up to its ID, the MCP-tool
// equivalent of cmd's resolveProjectFlag(Optional) — except a long-running
// MCP server has no single "current working directory" to fall back to the
// way a one-shot CLI invocation does, so an empty name here always means
// "no project scope", never "infer one from cwd". Thin wrapper over
// app.ResolveProject, the single implementation shared with internal/cmd.
func resolveProject(st *store.Store, name string) (*int64, error) {
	return app.ResolveProject(st, name, false)
}

// resolveRole mirrors the CLI's resolveRoleFlag, using resolveProject's
// same "no cwd to infer from" scoping: an explicit project name or nil,
// never an ambient one. st.ResolveRole then applies its own flag > env >
// active-session-role precedence.
func resolveRole(st *store.Store, roleArg, projectName string) (*int64, error) {
	return app.ResolveRole(st, roleArg, projectName, false)
}

func nullStrPtr(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	s := v.String
	return &s
}

func nullIntPtr(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	n := v.Int64
	return &n
}

// defaultListLimit/maxListLimit bound every list-style tool's response size
// (an MCP client over stdio would otherwise receive an unbounded result set
// from a large store).
// paginate slices items to at most `limit` (falling back to
// defaultListLimit when limit <= 0, capped at maxListLimit) starting at
// `offset`, and reports whether more rows exist beyond what was returned.
const (
	defaultListLimit = 100
	maxListLimit     = 500
)

func paginate[T any](items []T, limit, offset int) (page []T, truncated bool) {
	if offset < 0 {
		offset = 0
	}
	if offset >= len(items) {
		return []T{}, false
	}
	items = items[offset:]
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}
	if len(items) > limit {
		return items[:limit], true
	}
	return items, false
}

// textResult is the common case for a tool that returns structured data:
// the SDK's generic AddTool serializes Out as the tool's structured result
// automatically, so handlers just return (result, nil) with no Content
// needed — but every handler in this package also echoes a short text
// summary into Content, since not every MCP client renders structured
// content, and a human skimming a tool call's output benefits from a
// one-line summary regardless.
func textResult(summary string) *sdkmcp.CallToolResult {
	return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: summary}}}
}
