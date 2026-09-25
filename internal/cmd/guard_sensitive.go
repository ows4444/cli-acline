package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// guard_sensitive.go closes three gaps in checkBashCommand that the Edit/Write/
// Read tools' checks already covered but a shell command sidestepped:
//
//  1. secret files — `Read ~/.ssh/id_rsa` was denied, `cat ~/.ssh/id_rsa` was not;
//  2. the acline store itself — `sqlite3 store.db 'DROP TRIGGER ...'` removes the
//     append-only protections the hash-chained audit trail relies on;
//  3. identity — an agent shell can claim to be a human (ACLINE_ACTOR_TYPE=human)
//     and then approve its own work.
//
// This is a best-effort denylist over the shell view built in guard_shell.go
// (quoted prose stays inert, so commit messages and notes that merely mention
// these things are not blocked). It raises the cost of the obvious routes; it
// is not a sandbox, and identity is not enforceable from here alone.

// secretReaderCommands read, copy or exfiltrate their file arguments; any
// argument naming a secret file is denied for these regardless of how the
// path is spelled.
var secretReaderCommands = map[string]bool{
	"cat": true, "less": true, "more": true, "head": true, "tail": true, "tac": true, "nl": true,
	"cp": true, "mv": true, "scp": true, "rsync": true, "tar": true, "zip": true, "gzip": true, "base64": true,
	"xxd": true, "od": true, "hexdump": true, "strings": true, "grep": true, "egrep": true, "rg": true,
	"ag": true, "awk": true, "sed": true, "cut": true, "sort": true, "diff": true, "openssl": true,
	"vi": true, "vim": true, "nano": true, "emacs": true, "open": true, "code": true, "bat": true,
	"curl": true, "wget": true, "source": true, ".": true, "ssh-add": true,
}

var sqliteCommands = map[string]bool{
	"sqlite3": true, "sqlite": true, "litecli": true, "sqlite-utils": true, "datasette": true,
}

var (
	actorTypeAssignRe = regexp.MustCompile(`(?i)(?:^|[\s;&|(])ACLINE_ACTOR_TYPE=['"]?([^\s'";&|)]*)`)
	// Enabling, rotating or disabling the approval token is a human action done
	// in the human's own terminal. An agent that runs these itself (for example
	// through a pseudo-terminal it allocates) could set a token it knows.
	// \b treats '_' as a word character, so quoted prose such as "run acline
	// auth init" (one inert word in the shell view) does not match.
	authManageRe = regexp.MustCompile(`(?i)\bacline\b[^;&|\n]*\bauth\s+(?:init|rotate|disable)\b`)
	// A snapshot import merges arbitrary rows (approvals, checks, accepted
	// decisions...) into the store. The store refuses an agent unless it holds
	// the token; denying it here as well stops the attempt before it starts.
	snapshotImportRe = regexp.MustCompile(`(?i)\bacline\b[^;&|\n]*\bsnapshot\s+import\b`)
	// Every registered project path is inside the guard's write scope, so
	// registering one decides where agents may write. The store refuses an agent
	// without the token; denying it here stops the attempt before it starts.
	projectAddRe    = regexp.MustCompile(`(?i)\bacline\b[^;&|\n]*\bproject\s+add\b`)
	approvalEnvRe   = regexp.MustCompile(`(?i)(?:^|[\s;&|($="'])ACLINE_APPROVAL_TOKEN`)
	identityUnsetRe = regexp.MustCompile(`(?i)(?:^|[\s;&|(])(?:unset\s+(?:-\w+\s+)*(?:\S+\s+)*|env\s+(?:\S+\s+)*-u\s*)ACLINE_(?:ACTOR_TYPE|ACTOR|MODEL)\b|(?:^|[\s;&|(])env\s+(?:-\S*i\S*|--ignore-environment)\b`)
)

// bashSensitiveAccess returns a deny reason when a segment of c touches a
// secret file, the acline store, or the caller's identity; "" when it doesn't.
// dbPath is the store in use (empty when unknown).
func bashSensitiveAccess(c, dbPath string) string {
	if m := actorTypeAssignRe.FindAllStringSubmatch(c, -1); m != nil {
		for _, match := range m {
			if !strings.EqualFold(match[1], "agent") {
				return "blocked: setting ACLINE_ACTOR_TYPE to anything but \"agent\" would let an agent impersonate a human approver"
			}
		}
	}
	if authManageRe.MatchString(c) {
		return "blocked: enabling, rotating or disabling the approval token is done by a human in their own terminal, not through an agent's shell"
	}
	if snapshotImportRe.MatchString(c) {
		return "blocked: importing a snapshot can forge approvals and other evidence; a human runs `acline snapshot import` in their own terminal"
	}
	if projectAddRe.MatchString(c) {
		return "blocked: registering a project path widens where agents may write; a human runs `acline project add` in their own terminal"
	}
	if approvalEnvRe.MatchString(c) {
		return "blocked: the approval token is a human-held secret; agent commands must not read or set ACLINE_APPROVAL_TOKEN"
	}
	if identityUnsetRe.MatchString(c) {
		return "blocked: unsetting or clearing ACLINE_ACTOR_TYPE/ACLINE_ACTOR/ACLINE_MODEL (or `env -i`) would disguise who is acting"
	}

	dbVariants := dbFileVariants(dbPath)
	for _, segment := range segmentSplitRe.Split(c, -1) {
		fields := strings.Fields(segment)
		cmdWord := commandWord(fields)
		if cmdWord == "" {
			continue
		}
		if sqliteCommands[cmdWord] {
			return "blocked: direct SQLite access bypasses acline's append-only audit trail — use the `acline` CLI"
		}
		if grepGlobCommands[cmdWord] {
			if g := grepSecretGlob(fields); g != "" {
				return fmt.Sprintf("blocked: glob '%s' selects protected secret files", g)
			}
		}
		for _, raw := range fields {
			token := pathTokenOf(raw)
			if token == "" {
				continue
			}
			if cmdWord != "acline" && touchesFile(token, dbVariants) {
				return fmt.Sprintf("blocked: '%s' is SDLC-managed state — mutate it via the `acline` CLI, not directly", token)
			}
			if !(secretReaderCommands[cmdWord] || looksLikePath(token)) {
				continue
			}
			for _, p := range secretPathPatterns {
				if p.MatchString(token) {
					return fmt.Sprintf("blocked: '%s' matches a protected secret-file pattern", token)
				}
			}
		}
	}
	return ""
}

// commandWord is the base name of the first word that isn't an env assignment
// or a shell grouping prefix; "" for an empty segment.
func commandWord(fields []string) string {
	for _, f := range fields {
		w := strings.TrimLeft(f, "({!")
		if w == "" || assignmentRe.MatchString(w) {
			continue
		}
		return filepath.Base(w)
	}
	return ""
}

// pathTokenOf reduces a shell word to the file path it may name: quotes and
// redirection operators are dropped, and `--flag=value` yields the value.
func pathTokenOf(field string) string {
	t := strings.Trim(field, `"'`)
	t = strings.TrimLeft(t, "<>&")
	if strings.HasPrefix(t, "-") {
		i := strings.Index(t, "=")
		if i < 0 {
			return ""
		}
		t = t[i+1:]
	}
	return strings.Trim(t, `"'`)
}

func looksLikePath(t string) bool {
	return strings.HasPrefix(t, "/") || strings.HasPrefix(t, "~") || strings.HasPrefix(t, ".")
}

// dbFileVariants lists the store file and its SQLite sidecars, resolved
// through symlinks, so a tampering command is caught however it is spelled.
func dbFileVariants(dbPath string) []string {
	if dbPath == "" {
		return nil
	}
	base := realPath(dbPath)
	return []string{base, base + "-wal", base + "-shm", base + "-journal"}
}

func touchesFile(token string, variants []string) bool {
	if len(variants) == 0 || !looksLikePath(token) {
		return false
	}
	if strings.HasPrefix(token, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			token = filepath.Join(home, strings.TrimPrefix(token, "~"))
		}
	}
	resolved := realPath(token)
	for _, v := range variants {
		if resolved == v {
			return true
		}
	}
	return false
}
