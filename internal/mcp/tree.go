package mcp

import (
	"os"

	"acline/internal/store"
	"acline/internal/worktree"
)

// dirForTask is the directory a task's checks run in and its gate is about. A
// long-running MCP server has no request-scoped working directory, so it is the
// task's project's registered path when that directory exists, and the server's
// own working directory otherwise. `acline_check_run` runs the tool here and
// treeForTask fingerprints the same place, so a result is never sealed with one
// directory's tree after running another directory's tests.
func dirForTask(st *store.Store, taskID int64) (string, error) {
	if task, err := st.GetTask(taskID); err == nil && task.ProjectID.Valid {
		if projects, err := st.ListProjects(); err == nil {
			for _, p := range projects {
				if p.ID != task.ProjectID.Int64 || !p.Path.Valid {
					continue
				}
				if info, err := os.Stat(p.Path.String); err == nil && info.IsDir() {
					return p.Path.String, nil
				}
			}
		}
	}
	return os.Getwd()
}

// treeForTask fingerprints dirForTask, so `acline_check_run`,
// `acline_check_record`, `acline_task_gate` and `acline_task_done` all describe
// the same directory. "" (unknown) never blocks anything.
func treeForTask(st *store.Store, taskID int64) string {
	dir, err := dirForTask(st, taskID)
	if err != nil {
		return ""
	}
	return worktree.Hash(dir)
}

// gateTreeForTask is treeForTask for the completion gate: a directory that
// exists but cannot be fingerprinted is store.TreeUnavailable, not "", so the
// gate does not mistake "could not check" for "nothing changed".
func gateTreeForTask(st *store.Store, taskID int64) string {
	dir, err := dirForTask(st, taskID)
	if err != nil {
		return ""
	}
	if tree := worktree.Hash(dir); tree != "" {
		return tree
	}
	return store.TreeUnavailable
}
