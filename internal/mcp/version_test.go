package mcp

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"acline/internal/store"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.2.0", "0.2.0", 0},
		{"0.1.9", "0.2.0", -1},
		{"0.3.0", "0.2.0", 1},
		{"0.2", "0.2.0", 0},
		{"1.0.0", "0.9.9", 1},
	}
	for _, c := range cases {
		got := compareVersions(c.a, c.b)
		switch {
		case c.want < 0 && got >= 0, c.want > 0 && got <= 0, c.want == 0 && got != 0:
			t.Errorf("compareVersions(%q, %q) = %d, want sign %d", c.a, c.b, got, c.want)
		}
	}
}

// connectWithClientInfo is connectedTestServer's client-Implementation-
// parameterized twin, needed here (rather than reusing connectedTestServer,
// which hardcodes an Implementation the version check never matches) to
// exercise versionMiddleware against a client actually named "acline".
func connectWithClientInfo(t *testing.T, impl *sdkmcp.Implementation) *sdkmcp.ClientSession {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	st.Actor = store.Actor{Type: "human", ID: "tester"}
	t.Cleanup(func() { st.Close() })

	server := NewServer(st)
	client := sdkmcp.NewClient(impl, nil)

	ctx := context.Background()
	t1, t2 := sdkmcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, t1, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	cs, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

// captureStderr redirects os.Stderr for the duration of fn and returns
// whatever was written to it.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	fn()
	w.Close()
	os.Stderr = orig
	out, _ := io.ReadAll(r)
	return string(out)
}

func TestVersionMiddlewareWarnsOnOldAclineClient(t *testing.T) {
	out := captureStderr(t, func() {
		cs := connectWithClientInfo(t, &sdkmcp.Implementation{Name: "acline", Version: "0.1.0"})
		// A full round trip guarantees the server has processed the session
		// (and so populated ServerSession.InitializeParams()) before we inspect
		// stderr -- Connect() alone only guarantees the initialize call/response,
		// not that the client's fire-and-forget "notifications/initialized" (or
		// anything after it) has reached the server yet.
		callTool[serverInfoOut](t, cs, "acline_server_info", struct{}{})
	})
	if !strings.Contains(out, "older than") {
		t.Errorf("expected a version-mismatch warning on stderr, got: %q", out)
	}
}

func TestVersionMiddlewareSilentOnCurrentAclineClient(t *testing.T) {
	out := captureStderr(t, func() {
		cs := connectWithClientInfo(t, &sdkmcp.Implementation{Name: "acline", Version: minExtensionVersion})
		callTool[serverInfoOut](t, cs, "acline_server_info", struct{}{})
	})
	if strings.Contains(out, "older than") {
		t.Errorf("expected no version-mismatch warning, got: %q", out)
	}
}

func TestVersionMiddlewareIgnoresNonAclineClient(t *testing.T) {
	out := captureStderr(t, func() {
		cs := connectWithClientInfo(t, &sdkmcp.Implementation{Name: "some-other-client", Version: "0.0.1"})
		callTool[serverInfoOut](t, cs, "acline_server_info", struct{}{})
	})
	if strings.Contains(out, "older than") {
		t.Errorf("expected no version-mismatch warning for a non-acline client, got: %q", out)
	}
}
