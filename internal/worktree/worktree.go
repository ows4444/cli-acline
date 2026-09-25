// Package worktree fingerprints the contents of a project directory, so a
// verification result can say which code it verified.
//
// Without that, a `test: pass` recorded before the code changed still satisfies
// the completion gate afterwards. The fingerprint describes the tree, not the
// history: the same files hash the same before and after they are committed, and
// the same content hashes the same in a checkout and in a copy.
package worktree

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	maxFiles = 100000
	maxBytes = 512 << 20
	gitLimit = 20 * time.Second
)

// skipDirs are never descended into outside git (inside git, .gitignore decides).
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, ".venv": true, "venv": true,
	"target": true, "dist": true, "build": true, "__pycache__": true, ".cache": true,
}

// Hash returns "sha256:<hex>" over the files under dir, or "" when it cannot be
// determined (the directory is missing, unreadable, or too large to read). In a
// git checkout the files are the tracked and untracked-but-not-ignored ones; elsewhere
// every file outside the usual dependency and build directories.
func Hash(dir string) string {
	if dir == "" {
		return ""
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return ""
	}
	files, ok := gitFiles(dir)
	if !ok {
		files, ok = walkFiles(dir)
	}
	if !ok {
		return ""
	}
	sort.Strings(files)
	h := sha256.New()
	var total int64
	for _, rel := range files {
		abs := filepath.Join(dir, rel)
		h.Write([]byte(rel))
		h.Write([]byte{0})
		info, err := os.Lstat(abs)
		if err != nil {
			h.Write([]byte("missing\x00")) // in git's index but deleted from disk
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, _ := os.Readlink(abs)
			h.Write([]byte("link:" + target + "\x00"))
			continue
		}
		if !info.Mode().IsRegular() {
			h.Write([]byte("special\x00"))
			continue
		}
		total += info.Size()
		if total > maxBytes {
			return ""
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return ""
		}
		sum := sha256.Sum256(data)
		h.Write(sum[:])
		if info.Mode()&0o111 != 0 {
			h.Write([]byte("x"))
		}
		h.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// gitFiles lists the files git considers part of the tree, relative to dir.
func gitFiles(dir string) ([]string, bool) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitLimit)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--full-name")
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.Output()
	if err != nil {
		return nil, false
	}
	// --full-name is relative to the repository root; make paths relative to dir.
	top, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return nil, false
	}
	root := strings.TrimSpace(string(top))
	realDir, _ := filepath.EvalSymlinks(dir)
	realRoot, _ := filepath.EvalSymlinks(root)
	prefix, err := filepath.Rel(realRoot, realDir)
	if err != nil {
		return nil, false
	}
	var files []string
	for _, name := range bytes.Split(out, []byte{0}) {
		if len(name) == 0 {
			continue
		}
		rel := string(name)
		if prefix != "." {
			r, err := filepath.Rel(prefix, rel)
			if err != nil || strings.HasPrefix(r, "..") {
				continue // outside the directory we were asked about
			}
			rel = r
		}
		files = append(files, filepath.ToSlash(rel))
		if len(files) > maxFiles {
			return nil, false
		}
	}
	return files, true
}

// walkFiles lists the files under dir outside the usual dependency and build directories.
func walkFiles(dir string) ([]string, bool) {
	var files []string
	tooMany := false
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != dir && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return nil
		}
		files = append(files, filepath.ToSlash(rel))
		if len(files) > maxFiles {
			tooMany = true
			return filepath.SkipAll
		}
		return nil
	})
	return files, !tooMany
}
