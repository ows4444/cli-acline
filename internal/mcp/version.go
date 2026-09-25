package mcp

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// minExtensionVersion is the oldest vscode-acline EXTENSION_CLIENT_VERSION
// (mcpClient.ts) this server still expects to behave correctly against. Bump
// it only when a change here would actually break an older extension (a
// removed/renamed tool, an incompatible argument) -- MCP's JSON arguments are
// additive-tolerant, so most changes don't need a bump at all.
const minExtensionVersion = "0.2.0"

// versionMiddleware warns about a stale client: mcpClient.ts's verifyServer() already
// catches "extension newer than server" by diffing listTools() against
// REQUIRED_TOOLS, but nothing previously caught the reverse -- a client
// identifying itself with a version older than this server expects, which
// could see argument/schema drift on a tool it already knows about.
// Full per-tool schema negotiation isn't attempted here (MCP's JSON arguments
// tolerate additions, so drift is rare); this is the proportionate fix -- log once per connection so a stale extension
// shows up in the server's own stderr (which mcpClient.ts already pipes into
// its Output channel), not silently.
//
// It checks ServerSession.InitializeParams().ClientInfo rather than the
// "initialize" request's own params: that request's handler is what actually
// stores InitializeParams on the session, and middleware runs before it, so
// the check can't fire on the very first message -- but every message after
// that (including the client's own "notifications/initialized" and its first
// real call) sees it populated, which is early enough. warned dedupes so a
// long session doesn't repeat the line on every tool call.
func versionMiddleware() sdkmcp.Middleware {
	var warned sync.Map // ServerSession.ID() -> struct{}
	return func(next sdkmcp.MethodHandler) sdkmcp.MethodHandler {
		return func(ctx context.Context, method string, req sdkmcp.Request) (sdkmcp.Result, error) {
			if ss, ok := req.GetSession().(*sdkmcp.ServerSession); ok {
				if _, seen := warned.Load(ss.ID()); !seen {
					if p := ss.InitializeParams(); p != nil {
						warned.Store(ss.ID(), struct{}{})
						if ci := p.ClientInfo; ci != nil && ci.Name == "acline" && compareVersions(ci.Version, minExtensionVersion) < 0 {
							fmt.Fprintf(os.Stderr, "acline mcp serve: connected client is version %s, older than %s -- some tool arguments/results may not match what it expects; update the extension\n", ci.Version, minExtensionVersion)
						}
					}
				}
			}
			return next(ctx, method, req)
		}
	}
}

// compareVersions compares two "major.minor.patch"-style version strings
// numerically component by component (a missing or non-numeric component
// counts as 0), returning <0, 0, or >0 as a < b, a == b, or a > b.
func compareVersions(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var av, bv int
		if i < len(as) {
			av, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			bv, _ = strconv.Atoi(bs[i])
		}
		if av != bv {
			return av - bv
		}
	}
	return 0
}
