package store

import (
	"fmt"
	"os"
	"path/filepath"
)

// RealPath resolves path to its real, symlink-free absolute form. Every
// scope/protection check (the guard, session policy) compares against this instead of a plain
// filepath.Abs: a symlink placed inside an allowed root (vault, a tracked
// project, SOUL.md's directory) pointing outside it would otherwise pass
// the check by path string alone, while the OS follows the symlink on the
// actual read/write — a real bypass for an agent (or a prompt-injected
// instruction) that creates the symlink first.
//
// This deliberately does not use filepath.EvalSymlinks: that function
// fails outright when the final target doesn't exist, which is exactly the
// dangling-symlink case a bypass would use (create a symlink pointing at a
// not-yet-existing path outside the sandbox, then Write through it) — a
// naive EvalSymlinks failure would fall back to the symlink's own literal
// (in-scope) path and miss the redirect entirely. Instead this walks the
// path component by component, resolving each ancestor directory fully and
// following the final component if it's a symlink (recursively, in case
// the target is itself a symlink or contains further symlinked
// directories) — only a genuinely nonexistent component stops resolution
// and is appended literally, since a not-yet-existing path can't itself be
// a symlink.
func RealPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	resolved, err := resolvePathComponents(abs, 0)
	if err != nil {
		return abs
	}
	return resolved
}

func resolvePathComponents(path string, depth int) (string, error) {
	if depth > 40 {
		return "", fmt.Errorf("too many levels of symbolic links: %s", path)
	}
	path = filepath.Clean(path)
	dir := filepath.Dir(path)
	if dir == path {
		return path, nil // filesystem root
	}
	resolvedDir, err := resolvePathComponents(dir, depth+1)
	if err != nil {
		return "", err
	}
	full := filepath.Join(resolvedDir, filepath.Base(path))
	info, err := os.Lstat(full)
	if err != nil {
		return full, nil // full (or an ancestor) doesn't exist — nothing further to resolve
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(full)
		if err != nil {
			return full, nil
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(resolvedDir, target)
		}
		return resolvePathComponents(target, depth+1)
	}
	return full, nil
}
