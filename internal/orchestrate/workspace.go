package orchestrate

import "acline/internal/worktree"

// fingerprint summarizes the files under dir so a step that only edited code
// can be told apart from one that did nothing. It is observed state, not the
// agent's account of itself, and it is the same content hash a runner check
// records (worktree.Hash), so "the agent changed files" and "the checks are
// stale" can never disagree. An empty dir, or a tree too large to read, yields
// "" (unknown), which never counts as a change on its own.
func fingerprint(dir string) string {
	return worktree.Hash(dir)
}

// workspaceChanged reports whether two fingerprints show the files changed.
// Unknown (empty) on either side is treated as unchanged: absence of evidence
// is not progress.
func workspaceChanged(before, after string) bool {
	return before != "" && after != "" && before != after
}
