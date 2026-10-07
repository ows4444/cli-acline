package mcp

import (
	"acline/internal/app"
	"acline/internal/store"
	"acline/internal/worktree"
)

// The task's directory and its fingerprints come from internal/app, the one
// rule shared with the CLI (see app/tree.go).

func treeForTask(st *store.Store, taskID int64) string {
	return app.TaskTree(st, taskID, worktree.Hash)
}

func gateTreeForTask(st *store.Store, taskID int64) string {
	return app.GateTree(st, taskID, worktree.Hash)
}
