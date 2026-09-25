// Package app is acline's application/use-case layer: the single place
// business rules live once, so internal/cmd (cobra) and internal/mcp
// (MCP tools) call the same code instead of hand-mirroring it. It sits
// between the adapters and internal/store — adapters translate flags/args
// (or MCP arguments) into calls here and format the result; app never
// touches cobra, os.Args, or MCP request types.
//
// This file is the first extraction: project/role scope resolution, which
// previously existed as three near-identical copies — internal/cmd's
// resolveProjectFlag/resolveProjectFlagOptional, internal/mcp's
// resolveProject/resolveRole, and a fourth, unused copy that had drifted
// into internal/store (resolveProjectID) — with no caller ever noticing the
// dead one.
package app

import (
	"errors"
	"fmt"

	"acline/internal/store"
)

// ResolveProject resolves an explicit project name to its ID.
//
// If explicit is empty and allowCwdFallback is true, it falls back to
// st.ResolveCurrentProject (ACLINE_PROJECT env, .acline-project marker,
// cwd path match) — the behavior a one-shot CLI invocation wants. If
// explicit is empty and allowCwdFallback is false, the result is always
// (nil, nil): "no project scope", never inferred — the behavior a
// long-running MCP server wants, since it has no single request-scoped cwd
// to infer from (internal/mcp/server.go's resolveProject documented this
// distinction; it's preserved here rather than papered over).
func ResolveProject(st *store.Store, explicit string, allowCwdFallback bool) (*int64, error) {
	if explicit != "" {
		p, err := st.GetProjectByName(explicit)
		if err != nil {
			return nil, fmt.Errorf("project %q: %w", explicit, err)
		}
		return &p.ID, nil
	}
	if !allowCwdFallback {
		return nil, nil
	}
	p, err := st.ResolveCurrentProject()
	if err != nil {
		if errors.Is(err, store.ErrNoProject) {
			return nil, nil
		}
		return nil, err
	}
	return &p.ID, nil
}

// ResolveProjectOptional resolves an explicit project name only, ignoring
// any ambient/cwd project — the behavior list/filter commands want, where
// no --project means "show everything," not "fall back to the current
// project." Equivalent to ResolveProject(st, explicit, false).
func ResolveProjectOptional(st *store.Store, explicit string) (*int64, error) {
	return ResolveProject(st, explicit, false)
}

// ResolveRole resolves a --role/role-argument against a project scope
// resolved the same way ResolveProject would (with or without cwd
// fallback, per the caller's context), then delegates to
// st.ResolveRole's own flag > $ACLINE_ROLE > active-session-role
// precedence so a project's own role can shadow a global one of the same
// name.
func ResolveRole(st *store.Store, roleArg, projectFlag string, allowCwdFallback bool) (*int64, error) {
	projectID, err := ResolveProject(st, projectFlag, allowCwdFallback)
	if err != nil {
		return nil, err
	}
	return st.ResolveRole(roleArg, projectID)
}
