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
// exists, else "" (no project, no path, the path is gone, or the store could
// not be read: TaskDir reports that last case).
func ProjectDir(st *store.Store, taskID int64) string {
	dir, _ := projectDir(st, taskID)
	return dir
}

func projectDir(st *store.Store, taskID int64) (string, error) {
	task, err := st.GetTask(taskID)
	if err != nil {
		return "", err
	}
	if !task.ProjectID.Valid {
		return "", nil
	}
	projects, err := st.ListProjects()
	if err != nil {
		return "", err
	}
	for _, p := range projects {
		if p.ID != task.ProjectID.Int64 || !p.Path.Valid || p.Path.String == "" {
			continue
		}
		if info, err := os.Stat(p.Path.String); err == nil && info.IsDir() {
			return p.Path.String, nil
		}
	}
	return "", nil
}

// TaskDir is the directory a task's checks run in and its gate is about:
// ProjectDir, or the working directory when the task has none. A task or a
// project list that cannot be read is an error, never the working directory:
// that would run the checks of, and fingerprint, whatever directory the caller
// happens to be in.
func TaskDir(st *store.Store, taskID int64) (string, error) {
	dir, err := projectDir(st, taskID)
	if err != nil {
		return "", err
	}
	if dir != "" {
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

// GateTree is TaskTree for the completion gate: a directory that cannot be
// found or fingerprinted is store.TreeUnavailable, not "", so the gate does not
// mistake "could not check" for "nothing changed".
func GateTree(st *store.Store, taskID int64, hash func(string) string) string {
	dir, err := TaskDir(st, taskID)
	if err != nil {
		return store.TreeUnavailable
	}
	if tree := hash(dir); tree != "" {
		return tree
	}
	return store.TreeUnavailable
}
