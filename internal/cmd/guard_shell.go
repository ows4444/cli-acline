package cmd

import (
	"path/filepath"
	"regexp"
	"strings"
)

// guard_shell.go gives checkBashCommand a shell-aware view of a command so
// the regex policy stops firing on text that is only data: a commit message
// in quotes, a heredoc body fed to `cat`, backticks inside a single-quoted
// string. The raw-string scan it replaces denied `git commit -m "fix rm -rf
// handling"` and any heredoc that merely mentioned `export`, `rm -rf /`, or
// `>> SOUL.md`.
//
// The view is built the way bash reads the command, not by deleting quoted
// text, so quoting a flag or a filename cannot hide it:
//
//   - quotes are removed from words, so `rm "-rf" /` reads as `rm -rf /` and
//     `echo x > "SOUL.md"` still shows the redirect target;
//   - inside quotes, whitespace and shell metacharacters become '_', so a
//     quoted sentence stays one inert word (`never_run_rm_-rf_/_here`);
//   - `$(...)` and backticks are pulled out and checked as commands of their
//     own wherever bash would expand them (unquoted, double quotes, unquoted
//     heredoc bodies), but not inside single quotes or quoted heredocs;
//   - heredoc bodies are dropped from the view, since they are stdin data.
//
// Data is only inert if nothing runs it. When any word in the command is an
// interpreter or evaluator (bash -c, python -, eval, ssh, ...), or the
// command name itself is computed ($X, $(...)), the whole command falls back
// to the raw-string scan, exactly as before this file existed.

// shellExecutors are commands that can run text given as an argument or on
// stdin. Any of them appearing as a word anywhere in the command (so wrappers
// like sudo, timeout, docker exec, xargs and find -exec are covered too)
// switches the command back to the conservative raw scan.
var shellExecutors = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "fish": true, "csh": true, "tcsh": true,
	"eval": true, "exec": true, "source": true, ".": true, "trap": true, "alias": true,
	"env": true, "su": true, "ssh": true, "watch": true, "script": true, "flock": true, "parallel": true,
	"python": true, "python2": true, "python3": true, "node": true, "nodejs": true, "deno": true, "bun": true,
	"perl": true, "ruby": true, "php": true, "lua": true, "osascript": true,
	"awk": true, "gawk": true, "nawk": true, "mawk": true, "sed": true, "gsed": true,
	"npx": true, "bunx": true, "xargs": true,
}

var assignmentRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// substitutionMark stands in for an extracted `$(...)`/backtick in the view,
// so `rm -rf $(pwd)` still reads as rm with its flags and one argument.
const substitutionMark = "_S_"

type shellScan struct {
	view    strings.Builder
	subs    []string // raw text of each command substitution bash would expand
	pending []heredocSpec
}

type heredocSpec struct {
	delim  string
	quoted bool // <<'EOF' / <<"EOF" / <<\EOF: body is literal, no expansion
	strip  bool // <<-EOF: leading tabs stripped from body lines
}

// scanTarget is one string for checkBashCommand's patterns. raw marks the
// conservative fallback (the command can execute text), where
// interpreterSecretPatterns apply too; in a shell view they can only ever
// match data, since nothing in that command evaluates Python or C.
type scanTarget struct {
	text string
	raw  bool
}

// bashScanTargets returns every target checkBashCommand's patterns should
// run against: the shell view of cmd plus, recursively, each substitution
// bash would expand. Commands that can execute text fall back to the raw
// string and its regex-extracted subshells (the pre-lexer behavior).
func bashScanTargets(cmd string, depth int) []scanTarget {
	normalized := normalizeCmd(cmd)
	if depth > 5 {
		return []scanTarget{{normalized, true}}
	}
	sc := &shellScan{}
	sc.scan(normalized, 0, false)
	view := sc.view.String()
	if shellViewExecutes(view) {
		targets := []scanTarget{{normalized, true}}
		for _, sub := range extractSubshells(normalized, 0) {
			targets = append(targets, scanTarget{normalizeCmd(sub), true})
		}
		return targets
	}
	targets := []scanTarget{{view, false}}
	for _, sub := range sc.subs {
		targets = append(targets, bashScanTargets(sub, depth+1)...)
	}
	return targets
}

// shellViewExecutes reports whether any word of the view names an executor,
// or any segment's command position is computed rather than literal.
func shellViewExecutes(view string) bool {
	for _, segment := range segmentSplitRe.Split(view, -1) {
		commandSeen := false
		for _, field := range strings.Fields(segment) {
			word := strings.TrimLeft(field, "({!")
			if word == "" {
				continue
			}
			if shellExecutors[filepath.Base(word)] {
				return true
			}
			if commandSeen || assignmentRe.MatchString(word) {
				continue
			}
			commandSeen = true
			if strings.Contains(word, "$") || strings.Contains(word, substitutionMark) {
				return true
			}
		}
	}
	return false
}

// scan lexes s from i in unquoted context, writing the view. With inSub it
// stops at the ')' closing a `$(`, returning that index; otherwise it runs to
// the end of s.
func (sc *shellScan) scan(s string, i int, inSub bool) int {
	depth := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == '\\':
			if i+1 < len(s) && s[i+1] != '\n' {
				sc.view.WriteByte(s[i+1])
			}
			i += 2
		case c == '\'':
			end := strings.IndexByte(s[i+1:], '\'')
			if end < 0 {
				end = len(s) - i - 1
			}
			sc.view.WriteString(inertText(s[i+1:i+1+end], false))
			i += end + 2
		case c == '"':
			i = sc.scanDouble(s, i+1, &sc.view, true)
		case c == '`':
			i = sc.scanBacktick(s, i+1, &sc.view)
		case c == '$' && i+1 < len(s) && s[i+1] == '(':
			i = sc.scanSubstitution(s, i+2, &sc.view)
		case c == '(':
			depth++
			sc.view.WriteByte(c)
			i++
		case c == ')':
			if inSub && depth == 0 {
				return i
			}
			depth--
			sc.view.WriteByte(c)
			i++
		case strings.HasPrefix(s[i:], "<<<"):
			sc.view.WriteString("<<<")
			i += 3
		case strings.HasPrefix(s[i:], "<<"):
			i = sc.scanHeredocOperator(s, i+2)
		case c == '\n':
			sc.view.WriteByte(c)
			i = sc.consumeHeredocBodies(s, i+1)
		default:
			sc.view.WriteByte(c)
			i++
		}
	}
	return i
}

// scanDouble lexes a double-quoted string (or, with !terminated, a heredoc
// body that expands) starting after its opening quote. Literal text is made
// inert; `$` stays so `echo "$SECRET"` still reads as an env-var echo.
func (sc *shellScan) scanDouble(s string, i int, out *strings.Builder, terminated bool) int {
	for i < len(s) {
		c := s[i]
		switch {
		case terminated && c == '"':
			return i + 1
		case c == '\\':
			if i+1 < len(s) {
				out.WriteString(inertText(s[i+1:i+2], true))
			}
			i += 2
		case c == '`':
			i = sc.scanBacktick(s, i+1, out)
		case c == '$' && i+1 < len(s) && s[i+1] == '(':
			i = sc.scanSubstitution(s, i+2, out)
		default:
			out.WriteString(inertText(s[i:i+1], true))
			i++
		}
	}
	return i
}

func (sc *shellScan) scanBacktick(s string, i int, out *strings.Builder) int {
	start := i
	for i < len(s) && s[i] != '`' {
		if s[i] == '\\' {
			i++
		}
		i++
	}
	end := min(i, len(s))
	sc.subs = append(sc.subs, s[start:end])
	out.WriteString(substitutionMark)
	return end + 1
}

// scanSubstitution lexes a `$(...)` body with a nested scan, so quotes and
// heredocs inside it can't end it early at a ')' that bash would not.
func (sc *shellScan) scanSubstitution(s string, i int, out *strings.Builder) int {
	inner := &shellScan{}
	end := inner.scan(s, i, true)
	sc.subs = append(sc.subs, s[i:min(end, len(s))])
	out.WriteString(substitutionMark)
	return end + 1
}

func (sc *shellScan) scanHeredocOperator(s string, i int) int {
	spec := heredocSpec{}
	if i < len(s) && s[i] == '-' {
		spec.strip = true
		i++
	}
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	var delim strings.Builder
	for i < len(s) && !strings.ContainsRune(" \t\n;&|<>()", rune(s[i])) {
		switch s[i] {
		case '\'', '"':
			spec.quoted = true
			end := strings.IndexByte(s[i+1:], s[i])
			if end < 0 {
				end = len(s) - i - 1
			}
			delim.WriteString(s[i+1 : i+1+end])
			i += end + 2
		case '\\':
			spec.quoted = true
			if i+1 < len(s) {
				delim.WriteByte(s[i+1])
			}
			i += 2
		default:
			delim.WriteByte(s[i])
			i++
		}
	}
	spec.delim = delim.String()
	sc.pending = append(sc.pending, spec)
	sc.view.WriteString("<<" + spec.delim)
	return min(i, len(s))
}

// consumeHeredocBodies skips the bodies of heredocs opened on the line that
// just ended. Bodies stay out of the view; an unquoted one still has its
// substitutions extracted, since bash expands those.
func (sc *shellScan) consumeHeredocBodies(s string, i int) int {
	for _, spec := range sc.pending {
		var body strings.Builder
		for i < len(s) {
			lineEnd := strings.IndexByte(s[i:], '\n')
			next := len(s)
			if lineEnd >= 0 {
				next = i + lineEnd + 1
				lineEnd += i
			} else {
				lineEnd = len(s)
			}
			line := s[i:lineEnd]
			i = next
			if spec.strip {
				line = strings.TrimLeft(line, "\t")
			}
			if line == spec.delim {
				break
			}
			body.WriteString(line + "\n")
		}
		if !spec.quoted {
			var discard strings.Builder
			sc.scanDouble(body.String(), 0, &discard, false)
		}
	}
	sc.pending = nil
	return i
}

// inertText rewrites quoted literal text so no regex can read a command,
// flag, redirect or separator in it: whitespace and shell metacharacters
// become '_'. In double quotes `$` is kept, because bash expands it there.
func inertText(text string, keepDollar bool) string {
	var b strings.Builder
	for _, r := range text {
		switch {
		case r == '$' && keepDollar:
			b.WriteRune(r)
		case r == ' ' || r == '\t' || r == '\n' || r == '\r' || strings.ContainsRune("<>|;&()`$'\"\\", r):
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
