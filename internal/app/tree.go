// This file is where a task's code lives, for every adapter. `check run`,
// `check record`, `task gate` and `task done` (CLI) and their MCP tools used two
// rules: the MCP server used the task's project path, the CLI the current
// directory. From a subfolder the CLI then ran only that subfolder's tests and
// fingerprinted only its files, so a high-risk task could complete on a subset
// while tests elsewhere failed.
package app

import (
	"os"

	"acline/internal/store"
)

// ProjectDir is the task's project's registered path when that directory
// exists, else "" (no project, no path, or the path is gone).
func ProjectDir(st *store.Store, taskID int64) string {
	task, err := st.GetTask(taskID)
	if err != nil || !task.ProjectID.Valid {
		return ""
	}
	projects, err := st.ListProjects()
	if err != nil {
		return ""
	}
	for _, p := range projects {
		if p.ID != task.ProjectID.Int64 || !p.Path.Valid || p.Path.String == "" {
			continue
		}
		if info, err := os.Stat(p.Path.String); err == nil && info.IsDir() {
			return p.Path.String
		}
	}
	return ""
}

// TaskDir is the directory a task's checks run in and its gate is about:
// ProjectDir, or the working directory when the task has none.
func TaskDir(st *store.Store, taskID int64) (string, error) {
	if dir := ProjectDir(st, taskID); dir != "" {
		return dir, nil
	}
	return os.Getwd()
}

// TaskTree fingerprints TaskDir with hash (worktree.Hash, or a test's stand-in).
// "" means unknown, which never blocks anything on its own.
func TaskTree(st *store.Store, taskID int64, hash func(string) string) string {
	dir, err := TaskDir(st, taskID)
	if err != nil {
		return ""
	}
	return hash(dir)
}

// GateTree is TaskTree for the completion gate: a directory that exists but
// cannot be fingerprinted is store.TreeUnavailable, not "", so the gate does not
// mistake "could not check" for "nothing changed".
func GateTree(st *store.Store, taskID int64, hash func(string) string) string {
	dir, err := TaskDir(st, taskID)
	if err != nil {
		return ""
	}
	if tree := hash(dir); tree != "" {
		return tree
	}
	return store.TreeUnavailable
}
