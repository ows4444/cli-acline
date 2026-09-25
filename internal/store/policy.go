package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// Policy is what a session is permitted to touch. It is what a harness (hooks,
// sandboxing, permission prompts) should consult before letting an agent act —
// acline cannot intercept anything itself, but it can be the thing the harness
// asks. An empty Policy is fully permissive, matching the old free-text
// label that nothing ever checked.
type Policy struct {
	Label      string   `json:"label,omitempty"`
	AllowTools []string `json:"allow_tools,omitempty"`
	DenyTools  []string `json:"deny_tools,omitempty"`
	AllowPaths []string `json:"allow_paths,omitempty"`
	DenyPaths  []string `json:"deny_paths,omitempty"`
}

func (p Policy) JSON() (string, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ParsePolicy reads a session's stored policy string. A plain non-JSON string
// (the old free-text label) is preserved as Label and otherwise treated as
// permissive, so existing sessions keep working unchanged.
func ParsePolicy(s string) Policy {
	if s == "" {
		return Policy{}
	}
	var p Policy
	if err := json.Unmarshal([]byte(s), &p); err != nil {
		return Policy{Label: s}
	}
	return p
}

func matchesFold(pattern, value string) bool {
	return pattern == "*" || strings.EqualFold(pattern, value)
}

// matchesPath matches a path pattern where '*' matches any run of characters,
// including '/' — so "internal/*" matches "internal/store/x.go". This is
// deliberately simpler than shell globbing: one wildcard behavior, no '**',
// no character classes, easy to reason about in a policy file.
func matchesPath(pattern, value string) bool {
	re, ok := pathPatternCache.Load(pattern)
	if !ok {
		compiled, err := regexp.Compile("^" + strings.ReplaceAll(regexp.QuoteMeta(pattern), `\*`, ".*") + "$")
		if err != nil {
			return false
		}
		re, _ = pathPatternCache.LoadOrStore(pattern, compiled)
	}
	return re.(*regexp.Regexp).MatchString(value)
}

// pathPatternCache holds compiled policy patterns: a hook checks the same few
// patterns on every tool call.
var pathPatternCache sync.Map // string -> *regexp.Regexp

// Check evaluates one action against the policy, resolving relative paths
// and patterns against the current working directory. See CheckIn.
func (p Policy) Check(tool, filePath string) (allowed bool, reason string) {
	return p.CheckIn("", tool, filePath)
}

// CheckIn evaluates one action against the policy. path may be empty when the
// action has no filesystem target (e.g. a network call recorded as a
// pseudo-tool). DenyTools/DenyPaths take precedence over the allow-lists.
//
// Paths are compared in normalized form: a relative path or pattern is
// resolved against root (the project root; the working directory when root is
// empty) and cleaned, so `secrets/*` denies both `secrets/a` and
// `/abs/proj/secrets/a` (hook payloads carry absolute paths), and
// `internal/../etc/passwd` can no longer satisfy an `internal/*` allow rule.
func (p Policy) CheckIn(root, tool, filePath string) (allowed bool, reason string) {
	tool = strings.TrimSpace(tool)

	for _, d := range p.DenyTools {
		if matchesFold(d, tool) {
			return false, fmt.Sprintf("tool %q is explicitly denied by policy", tool)
		}
	}
	if len(p.AllowTools) > 0 {
		ok := false
		for _, a := range p.AllowTools {
			if matchesFold(a, tool) {
				ok = true
				break
			}
		}
		if !ok {
			return false, fmt.Sprintf("tool %q is not in the policy's allow-list", tool)
		}
	}

	if filePath != "" {
		root = policyRoot(root)
		norm := normalizePolicyPath(root, filePath)
		// The OS follows symlinks, so the resolved path is what is really
		// touched. A deny rule applies if either form matches (it may name the
		// link or its target); an allow rule only if the resolved form does.
		real := RealPath(norm)
		for _, d := range p.DenyPaths {
			if matchesPath(normalizePolicyPattern(root, d), norm) || matchesPath(resolvePolicyPattern(root, d), real) {
				return false, fmt.Sprintf("path %q matches denied pattern %q", filePath, d)
			}
		}
		if len(p.AllowPaths) > 0 {
			ok := false
			for _, a := range p.AllowPaths {
				if matchesPath(resolvePolicyPattern(root, a), real) {
					ok = true
					break
				}
			}
			if !ok {
				return false, fmt.Sprintf("path %q does not match any allowed pattern", filePath)
			}
		}
	}

	return true, ""
}

func policyRoot(root string) string {
	if root == "" {
		if wd, err := os.Getwd(); err == nil {
			return wd
		}
		return string(filepath.Separator)
	}
	if abs, err := filepath.Abs(root); err == nil {
		return abs
	}
	return root
}

func normalizePolicyPath(root, p string) string {
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	return filepath.Clean(p)
}

// normalizePolicyPattern anchors a relative pattern at root. A pattern that
// starts with '*' is deliberately left alone: it means "anywhere" (`*` is
// everything, `*.env` any .env file) and anchoring it would shrink its reach.
// A trailing '/' means "everything under this directory".
func normalizePolicyPattern(root, pat string) string {
	if strings.HasPrefix(pat, "*") {
		return pat
	}
	if strings.HasSuffix(pat, "/") {
		pat += "*"
	}
	return normalizePolicyPath(root, pat)
}

// resolvePolicyPattern is normalizePolicyPattern with the pattern's literal
// directory prefix (everything before the first '*') resolved through
// symlinks, so it compares with a RealPath-resolved path.
func resolvePolicyPattern(root, pat string) string {
	pat = normalizePolicyPattern(root, pat)
	if strings.HasPrefix(pat, "*") {
		return pat
	}
	star := strings.Index(pat, "*")
	if star < 0 {
		return RealPath(pat)
	}
	dir := pat[:star]
	if i := strings.LastIndex(dir, string(filepath.Separator)); i >= 0 {
		dir = dir[:i]
	}
	if dir == "" {
		return pat
	}
	return RealPath(dir) + pat[len(dir):]
}

// CheckPolicy evaluates an action against the active session's policy. A
// denial is recorded as a policy_violation event — the point of the check is
// evidence as much as the answer.
func (s *Store) CheckPolicy(tool, filePath string) (allowed bool, reason string, err error) {
	sess, err := s.CurrentSession()
	if err != nil {
		return false, "", err
	}
	p := ParsePolicy(sess.Policy.String)
	allowed, reason = p.CheckIn(s.sessionRoot(sess), tool, filePath)
	if !allowed {
		msg := fmt.Sprintf("tool=%s path=%s: %s", tool, filePath, reason)
		if _, err := s.LogEvent(nil, &sess.ID, "policy_violation", msg); err != nil {
			return allowed, reason, err
		}
	}
	return allowed, reason, nil
}

// sessionRoot is the directory a session's relative policy paths are anchored
// at: its project's registered path when it has one, else "" (the working
// directory -- hooks run from the project root).
func (s *Store) sessionRoot(sess *Session) string {
	if !sess.ProjectID.Valid {
		return ""
	}
	var path sql.NullString
	if err := s.DB.QueryRow(`SELECT path FROM projects WHERE id = ?`, sess.ProjectID.Int64).Scan(&path); err != nil || !path.Valid {
		return ""
	}
	return path.String
}
