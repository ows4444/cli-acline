package mcp

import (
	"context"
	"os"
	"regexp"
	"testing"
)

// The VS Code extension keeps its own list of the tools it calls
// (REQUIRED_TOOLS in vscode-acline/src/mcpClient.ts) and warns the user when
// the installed binary lacks any of them. If that list names a tool the server
// doesn't register -- a rename, a typo -- every user gets a bogus "your acline
// is out of date" warning. This test reads the TypeScript source and holds it
// to the server's real tool set, so the two can't drift silently.
func TestExtensionRequiredToolsAreAllRegistered(t *testing.T) {
	src, err := os.ReadFile("../../vscode-acline/src/mcpClient.ts")
	if err != nil {
		t.Skipf("extension source not present: %v", err)
	}
	block := regexp.MustCompile(`(?s)export const REQUIRED_TOOLS = \[(.*?)\];`).FindSubmatch(src)
	if block == nil {
		t.Fatal("could not find REQUIRED_TOOLS in mcpClient.ts")
	}
	names := regexp.MustCompile(`"(acline_[a-z_]+)"`).FindAllSubmatch(block[1], -1)
	if len(names) < 30 {
		t.Fatalf("parsed only %d tool names from REQUIRED_TOOLS; the parser is probably broken", len(names))
	}

	cs, _ := connectedTestServer(t)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	registered := map[string]bool{}
	for _, tool := range res.Tools {
		registered[tool.Name] = true
	}
	for _, m := range names {
		if name := string(m[1]); !registered[name] {
			t.Errorf("extension REQUIRED_TOOLS lists %q, which the server does not register", name)
		}
	}
	// and every tool the extension *calls* must be listed (so the version check is complete)
	called := regexp.MustCompile(`callTool[^(]*\(\s*"(acline_[a-z_]+)"`).FindAllSubmatch(src, -1)
	listed := map[string]bool{}
	for _, m := range names {
		listed[string(m[1])] = true
	}
	for _, m := range called {
		if name := string(m[1]); !listed[name] {
			t.Errorf("mcpClient.ts calls %q but REQUIRED_TOOLS does not list it", name)
		}
	}
}
