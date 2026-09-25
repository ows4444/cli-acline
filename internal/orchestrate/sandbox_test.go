package orchestrate

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"
)

func TestSandboxSettingsFailClosedAndKeepCredentialsOut(t *testing.T) {
	env := []string{"HOME=/home/u", "GOCACHE=/cache/go-build", "GOMODCACHE=/home/u/go/pkg/mod"}
	var c sandboxConfig
	if err := json.Unmarshal([]byte(sandboxSettings("/home/u/.acline/store.db", env)), &c); err != nil {
		t.Fatal(err)
	}
	sb := c.Sandbox
	if !sb.Enabled || !sb.FailIfUnavailable || sb.AllowUnsandboxedCommands {
		t.Fatalf("sandbox must be on, fail closed and allow no unsandboxed retry: %+v", sb)
	}
	for _, p := range []string{"~/.ssh", "~/.aws", "~/.config/gh"} {
		if !slices.Contains(sb.Filesystem.DenyRead, p) {
			t.Errorf("denyRead is missing %s: %v", p, sb.Filesystem.DenyRead)
		}
	}
	for _, p := range []string{filepath.Dir("/home/u/.acline/store.db"), "/cache/go-build", "/home/u/go/pkg/mod"} {
		if !slices.Contains(sb.Filesystem.AllowWrite, p) {
			t.Errorf("allowWrite is missing %s: %v", p, sb.Filesystem.AllowWrite)
		}
	}
	if !sb.Network.StrictAllowlist || !slices.Contains(sb.Network.AllowedDomains, "proxy.golang.org") {
		t.Errorf("network must be a strict allow-list with the Go proxy: %+v", sb.Network)
	}
}

func TestProxyDomainsFollowGOPROXY(t *testing.T) {
	if got := proxyDomains("https://goproxy.corp.example/mod,direct"); !slices.Equal(got, []string{"goproxy.corp.example", "sum.golang.org"}) {
		t.Errorf("custom GOPROXY: %v", got)
	}
	if got := proxyDomains("off"); len(got) != 0 {
		t.Errorf("GOPROXY=off should allow no host: %v", got)
	}
	if got := proxyDomains(""); !slices.Contains(got, "proxy.golang.org") {
		t.Errorf("unset GOPROXY should allow the default proxy: %v", got)
	}
}

func TestStepsLaunchSandboxedUnlessTurnedOff(t *testing.T) {
	l := launch{Dir: t.TempDir()}
	on := l.request("/tmp/store.db", "developer", "p", nil, nil)
	if on.Settings == "" || !slices.Contains(ClaudeAgent{}.Args(on), "--settings") {
		t.Fatalf("a default launch must carry the sandbox settings: %v", ClaudeAgent{}.Args(on))
	}
	l.NoSandbox = true
	off := l.request("/tmp/store.db", "developer", "p", nil, nil)
	if off.Settings != "" || slices.Contains(ClaudeAgent{}.Args(off), "--settings") {
		t.Fatalf("--no-sandbox must launch without settings: %v", ClaudeAgent{}.Args(off))
	}
}
