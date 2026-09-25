package cmd

import (
	"regexp"
	"strings"
)

// guard_glob.go closes a path around the secret-file checks: a search tool that
// is given a directory and a glob reads the files the glob selects, so
// Grep{path: ".", glob: "**/.env*"} printed secrets although "." is harmless.
//
// A glob is refused when it reaches a secret file and nothing ordinary, i.e.
// when it targets secrets. Broad globs (`*`, `*.json`) behave like a search with
// no glob at all; those rely on the `Read(...)` deny rules in settings.json,
// which Claude Code also applies to Grep and Glob.

// secretGlobSamples are paths the secret-file patterns protect.
var secretGlobSamples = []string{
	".env", ".env.local", ".env.production", "config/.env", "id_rsa", "id_ed25519", "id_ecdsa",
	"server.pem", "server.key", "cert.pfx", "cert.p12", "AuthKey.p8", "credentials.json", "token.json",
	"secrets.yaml", "secret.yml", ".npmrc", ".netrc", ".pypirc", ".pgpass", ".git-credentials",
	"terraform.tfstate", "kubeconfig", ".ssh/id_rsa", ".ssh/config", ".aws/credentials",
	".kube/config", ".docker/config.json", ".config/gcloud/credentials.db", ".azure/accessTokens.json",
}

// ordinaryGlobSamples are files any search over a project may reasonably touch.
var ordinaryGlobSamples = []string{
	"main.go", "main_test.go", "package.json", "README.md", "docs/guide.md", "src/app.ts", "src/app.tsx",
	"index.js", ".gitignore", "config.yaml", "Makefile", "notes.txt", "style.css", "app.py",
}

// secretGlob reports whether glob selects secret files and nothing ordinary.
func secretGlob(glob string) bool {
	glob = strings.TrimSpace(strings.Trim(glob, `"'`))
	if glob == "" {
		return false
	}
	re, err := globRegexp(glob)
	if err != nil {
		return true // a glob we cannot read is not one we can clear
	}
	hitsSecret := false
	for _, p := range secretGlobSamples {
		if re.MatchString(p) {
			hitsSecret = true
			break
		}
	}
	if !hitsSecret {
		return false
	}
	for _, p := range ordinaryGlobSamples {
		if re.MatchString(p) {
			return false
		}
	}
	return true
}

// globRegexp converts a ripgrep-style glob to a regexp over slash-separated
// relative paths: `**` spans directories, `*` and `?` stay within one, `{a,b}`
// is an alternation, and a glob matches at any depth (as ripgrep's do).
func globRegexp(glob string) (*regexp.Regexp, error) {
	glob = strings.TrimPrefix(glob, "./")
	glob = strings.TrimPrefix(glob, "!") // a negated glob still names these files
	var b strings.Builder
	b.WriteString(`^(?:.*/)?`)
	braces := 0
	for i := 0; i < len(glob); i++ {
		c := glob[i]
		switch {
		case c == '*' && i+1 < len(glob) && glob[i+1] == '*':
			i++
			if i+1 < len(glob) && glob[i+1] == '/' {
				i++
				b.WriteString(`(?:.*/)?`)
			} else {
				b.WriteString(`.*`)
			}
		case c == '*':
			b.WriteString(`[^/]*`)
		case c == '?':
			b.WriteString(`[^/]`)
		case c == '{':
			braces++
			b.WriteString(`(?:`)
		case c == '}' && braces > 0:
			braces--
			b.WriteString(`)`)
		case c == ',' && braces > 0:
			b.WriteString(`|`)
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString(`$`)
	return regexp.Compile(b.String())
}

// grepGlobCommands take file-selecting globs (`--include`, `-g`, `--glob`).
var grepGlobCommands = map[string]bool{"grep": true, "egrep": true, "fgrep": true, "rg": true, "ag": true}

// grepSecretGlob returns the first glob in a grep/rg/ag invocation that selects
// secret files, or "".
func grepSecretGlob(fields []string) string {
	for i, f := range fields {
		f = strings.Trim(f, `"'`)
		var value string
		switch {
		case strings.HasPrefix(f, "--include="), strings.HasPrefix(f, "--glob="), strings.HasPrefix(f, "--iglob="):
			value = f[strings.Index(f, "=")+1:]
		case f == "--include" || f == "-g" || f == "--glob" || f == "--iglob":
			if i+1 < len(fields) {
				value = fields[i+1]
			}
		default:
			continue
		}
		if secretGlob(value) {
			return strings.Trim(value, `"'`)
		}
	}
	return ""
}
