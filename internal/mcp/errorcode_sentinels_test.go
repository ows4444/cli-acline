package mcp

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every refusal the store gives an agent has a stable error_code, so an MCP
// client can tell "ask a person" from any other failure (docs/ARCHITECTURE.md,
// "Adding a privileged action", step 3).
func TestEveryAgentRefusalHasAnErrorCode(t *testing.T) {
	files, err := filepath.Glob("../store/*.go")
	if err != nil {
		t.Fatal(err)
	}
	mapping, err := os.ReadFile("errorcode.go")
	if err != nil {
		t.Fatal(err)
	}
	decl := regexp.MustCompile(`\b(ErrAgentCannot[A-Za-z]+)\s*=\s*errors\.New`)
	var found int
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range decl.FindAllSubmatch(src, -1) {
			found++
			if !strings.Contains(string(mapping), "store."+string(m[1])+")") {
				t.Errorf("store.%s (%s) has no case in classifyError", m[1], filepath.Base(f))
			}
		}
	}
	if found < 10 {
		t.Fatalf("found only %d ErrAgentCannot sentinels; the scan is broken", found)
	}
}
