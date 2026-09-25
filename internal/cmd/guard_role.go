package cmd

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"acline/internal/scaffold"
)

// guard_role.go holds a session running as a read-only role to its contract.
//
// The security, qa, architect and designer agent stubs list no Edit/Write, and
// their role files say so, but that list only reaches Claude Code's agent picker:
// nothing stopped the session from writing through Bash or the file tools. The
// role's tools are read from the same embedded stub (scaffold.AgentTools), so the
// contract and the enforcement cannot drift apart.
//
// This is best-effort, like the rest of the guard: an interpreter running a
// script file, or a program that writes as a side effect, is not caught. It
// closes the plain routes (the file tools, redirects, sed -i, rm/mv/cp/tee...).

// roleWriteCommands write, create, move or delete files when run.
var roleWriteCommands = map[string]bool{
	"rm": true, "rmdir": true, "mv": true, "cp": true, "tee": true, "touch": true, "mkdir": true, "truncate": true,
	"ln": true, "install": true, "dd": true, "patch": true, "chmod": true, "chown": true, "shred": true, "rsync": true,
}

// gitWritingSubcommands change the working tree or the repository.
var gitWritingSubcommands = map[string]bool{
	"add": true, "am": true, "apply": true, "checkout": true, "cherry-pick": true, "clean": true, "commit": true,
	"merge": true, "mv": true, "pull": true, "rebase": true, "reset": true, "restore": true, "revert": true,
	"rm": true, "stash": true, "switch": true, "worktree": true,
}

// formatterWriteFlags are the flags that make a formatter or linter rewrite
// files; without one it only reports.
var formatterWriteFlags = map[string][]string{
	"gofmt": {"-w"}, "goimports": {"-w"}, "prettier": {"--write", "-w"}, "eslint": {"--fix"},
	"ruff": {"--fix", "format"}, "rubocop": {"-a", "-A", "--autocorrect", "--autocorrect-all"},
}

// alwaysWritingTools rewrite or generate files whatever their arguments, or run
// arbitrary project scripts that may.
var alwaysWritingTools = map[string]bool{
	"black": true, "rustfmt": true, "make": true, "gmake": true, "unzip": true, "gunzip": true, "wget": true,
}

// writingInvocation reports, in words, how cmd with these fields writes files
// through a tool whose other uses are read-only ("" when it does not).
func writingInvocation(cmd string, fields []string) string {
	args := fields
	for i, f := range fields { // the arguments after the command word
		if filepath.Base(strings.TrimLeft(f, "({!")) == cmd {
			args = fields[i+1:]
			break
		}
	}
	// npx/bunx run the tool named next; judge that tool.
	if (cmd == "npx" || cmd == "bunx") && len(args) > 0 {
		for i, a := range args {
			if !strings.HasPrefix(a, "-") {
				return writingInvocation(filepath.Base(a), args[i:])
			}
		}
		return ""
	}
	first := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		if cmd == "git" && (a == "-C" || a == "-c" || a == "--git-dir" || a == "--work-tree") {
			i++ // the option's value is not the subcommand
			continue
		}
		if !strings.HasPrefix(a, "-") {
			first = a
			break
		}
	}
	has := func(flags ...string) bool {
		for _, a := range args {
			for _, f := range flags {
				if a == f || strings.HasPrefix(a, f+"=") {
					return true
				}
			}
		}
		return false
	}
	switch {
	case alwaysWritingTools[cmd]:
		return "runs " + cmd
	case cmd == "git" && gitWritingSubcommands[first]:
		return "runs git " + first
	case cmd == "go" && (first == "generate" || first == "fmt" || first == "get" || (first == "mod" && has("tidy", "vendor", "edit", "init"))):
		return "runs go " + first
	case (cmd == "npm" || cmd == "pnpm" || cmd == "yarn" || cmd == "bun") && first != "" && first != "test" && first != "ls" && first != "list" && first != "view" && first != "outdated" && first != "audit":
		return "runs " + cmd + " " + first
	case cmd == "cargo" && (first == "fmt" || first == "fix" || first == "add" || first == "update"):
		return "runs cargo " + first
	case cmd == "tar" && tarWrites(args):
		return "extracts or creates an archive (tar)"
	case cmd == "curl" && has("-o", "-O", "--output", "--remote-name", "--output-dir"):
		return "downloads into a file (curl)"
	}
	if flags, ok := formatterWriteFlags[cmd]; ok && has(flags...) {
		return "rewrites files (" + cmd + ")"
	}
	return ""
}

// tarWrites reports whether tar extracts or creates (x/c, bundled or dashed,
// or --extract/--create); listing (t) only reads.
func tarWrites(args []string) bool {
	for i, a := range args {
		switch {
		case a == "--extract" || a == "--get" || a == "--create":
			return true
		case strings.HasPrefix(a, "--"):
		case strings.HasPrefix(a, "-") || i == 0: // "-xzf", or bundled "xzf" as the first word
			if strings.ContainsAny(strings.TrimPrefix(a, "-"), "xc") {
				return true
			}
		}
	}
	return false
}

// roleInlineInterpreters run code given on the command line, which can write.
var roleInlineInterpreters = map[string]string{
	"python": "-c", "python2": "-c", "python3": "-c", "node": "-e", "nodejs": "-e", "perl": "-e", "ruby": "-e", "php": "-r", "deno": "eval",
}

// roleSessionSwitchRe finds `acline session end|start`: the way out of a
// read-only role. The store also refuses an agent ending such a session.
var roleSessionSwitchRe = regexp.MustCompile(`(?i)\bacline\b[^;&|\n]*\bsession\s+(?:end|start)\b`)

// roleRedirectRe finds an output redirect and its target (`> f`, `>> f`, `&> f`).
var roleRedirectRe = regexp.MustCompile(`(?:^|[\s\d&])>{1,2}\s*([^\s;&|<>()]+)`)

// checkRoleScope denies a write when the active role's contract grants no
// Edit/Write. A session with no role, a role acline ships no stub for, or an
// unknown role name is not restricted here.
func checkRoleScope(tool string, input map[string]any) (bool, string) {
	if st == nil {
		return false, ""
	}
	roleID, err := st.ResolveRole("", nil)
	if err != nil || roleID == nil {
		return false, ""
	}
	role, err := st.GetRole(*roleID)
	if err != nil {
		return false, ""
	}
	if !scaffold.RoleIsReadOnly(role.Name) {
		return false, ""
	}
	switch {
	case writeTools[tool]:
		return true, fmt.Sprintf("blocked: role %s is read-only (its contract grants no Edit/Write), so %s is not available to it", role.Name, tool)
	case tool == "Bash":
		cmd, _ := input["command"].(string)
		for _, target := range bashScanTargets(cmd, 0) {
			if roleSessionSwitchRe.MatchString(target.text) {
				return true, fmt.Sprintf("blocked: role %s is read-only, and ending or restarting the session would drop that role — ask the person to end it", role.Name)
			}
		}
		if why := bashWritesFiles(cmd); why != "" {
			return true, fmt.Sprintf("blocked: role %s is read-only (its contract grants no Edit/Write), and this command %s — hand the change to a role that may edit", role.Name, why)
		}
	}
	return false, ""
}

// bashWritesFiles reports, in words, how cmd writes to files; "" when it does not
// (as far as a static look can tell).
func bashWritesFiles(cmd string) string {
	for _, target := range bashScanTargets(cmd, 0) {
		for _, segment := range segmentSplitRe.Split(target.text, -1) {
			fields := strings.Fields(segment)
			cw := commandWord(fields)
			if cw == "" {
				continue
			}
			if roleWriteCommands[cw] {
				return "runs " + cw
			}
			if why := writingInvocation(cw, fields); why != "" {
				return why
			}
			if flag, inline := roleInlineInterpreters[cw]; inline {
				for _, f := range fields {
					if f == flag {
						return "runs inline " + cw + " code"
					}
				}
			}
			if cw == "sed" || cw == "gsed" || cw == "perl" || cw == "ruby" {
				for _, f := range fields {
					if f == "--in-place" || (strings.HasPrefix(f, "-") && !strings.HasPrefix(f, "--") && strings.ContainsRune(f, 'i')) {
						return "edits files in place (" + cw + " " + f + ")"
					}
				}
			}
			for _, m := range roleRedirectRe.FindAllStringSubmatch(segment, -1) {
				if dest := m[1]; dest != "/dev/null" && !strings.HasPrefix(dest, "&") {
					return "redirects output into " + dest
				}
			}
		}
	}
	return ""
}
