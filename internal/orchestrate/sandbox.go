package orchestrate

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// An orchestrated step can run arbitrary code (`go test` executes test code the
// agent may have just written). The environment allow-list keeps credential
// *variables* out, but HOME is passed, so without more the code could read
// ~/.ssh or ~/.aws and send it anywhere. sandboxSettings is the Claude Code
// setting (`claude --settings`) that puts every Bash command and its children
// under the OS sandbox (Seatbelt on macOS, bubblewrap on Linux):
//   - failIfUnavailable: no sandbox means no launch, never a silent fallback
//   - allowUnsandboxedCommands false: the agent cannot retry a command outside it
//   - denyRead: credential locations the sandbox's default read policy allows
//   - allowWrite: beyond the working directory and temp, only the store's
//     directory (acline check run records there) and the Go caches
//   - network: only the Go module proxy, and no prompting for anything else
//
// Claude Code's own API traffic is not a Bash command and is unaffected.

// sandboxDenyRead are credential locations under the home directory.
var sandboxDenyRead = []string{
	"~/.ssh", "~/.aws", "~/.gnupg", "~/.netrc", "~/.npmrc", "~/.pypirc", "~/.git-credentials",
	"~/.docker/config.json", "~/.kube", "~/.azure", "~/.config/gh", "~/.config/gcloud", "~/.config/op",
	"~/Library/Keychains",
}

// sandboxDefaultDomains are what `go` needs to fetch modules with the default GOPROXY.
var sandboxDefaultDomains = []string{"proxy.golang.org", "sum.golang.org", "storage.googleapis.com"}

type sandboxConfig struct {
	Sandbox struct {
		Enabled                  bool `json:"enabled"`
		FailIfUnavailable        bool `json:"failIfUnavailable"`
		AllowUnsandboxedCommands bool `json:"allowUnsandboxedCommands"`
		Filesystem               struct {
			DenyRead   []string `json:"denyRead"`
			AllowWrite []string `json:"allowWrite,omitempty"`
		} `json:"filesystem"`
		Network struct {
			AllowedDomains  []string `json:"allowedDomains"`
			StrictAllowlist bool     `json:"strictAllowlist"`
		} `json:"network"`
	} `json:"sandbox"`
}

// sandboxSettings renders the --settings JSON for an agent running with env
// against the store at dbPath.
func sandboxSettings(dbPath string, env []string) string {
	get := func(key string) string {
		for _, kv := range env {
			if k, v, ok := strings.Cut(kv, "="); ok && k == key {
				return v
			}
		}
		return ""
	}
	var c sandboxConfig
	c.Sandbox.Enabled, c.Sandbox.FailIfUnavailable = true, true
	c.Sandbox.Filesystem.DenyRead = sandboxDenyRead
	var write []string
	if dbPath != "" {
		write = append(write, filepath.Dir(dbPath))
	}
	write = append(write, goCacheDirs(get)...)
	c.Sandbox.Filesystem.AllowWrite = write
	c.Sandbox.Network.AllowedDomains = proxyDomains(get("GOPROXY"))
	c.Sandbox.Network.StrictAllowlist = true
	b, _ := json.Marshal(c) // a fixed struct of strings and bools cannot fail
	return string(b)
}

// goCacheDirs are the build and module caches `go build|test` write to, from
// the agent's environment or Go's defaults.
func goCacheDirs(get func(string) string) []string {
	home := get("HOME")
	var dirs []string
	if d := get("GOCACHE"); d != "" && d != "off" {
		dirs = append(dirs, d)
	} else if d, err := os.UserCacheDir(); err == nil {
		dirs = append(dirs, filepath.Join(d, "go-build"))
	}
	switch {
	case get("GOMODCACHE") != "":
		dirs = append(dirs, get("GOMODCACHE"))
	case get("GOPATH") != "":
		dirs = append(dirs, filepath.Join(strings.Split(get("GOPATH"), string(os.PathListSeparator))[0], "pkg", "mod"))
	case home != "":
		dirs = append(dirs, filepath.Join(home, "go", "pkg", "mod"))
	}
	return dirs
}

// proxyDomains are the hosts in GOPROXY (comma or pipe separated, with the
// "direct"/"off" keywords skipped), or the default proxy's when it is unset.
func proxyDomains(goproxy string) []string {
	if goproxy == "" {
		return sandboxDefaultDomains
	}
	var out []string
	for _, entry := range strings.FieldsFunc(goproxy, func(r rune) bool { return r == ',' || r == '|' }) {
		u, err := url.Parse(strings.TrimSpace(entry))
		if err != nil || u.Hostname() == "" {
			continue // direct, off, or not a URL
		}
		out = append(out, u.Hostname())
	}
	if len(out) == 0 {
		return []string{} // GOPROXY=direct/off: no proxy host to allow
	}
	return append(out, "sum.golang.org")
}
