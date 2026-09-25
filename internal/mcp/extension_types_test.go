package mcp

import (
	"os"
	"testing"
)

// TestGeneratedTypeScriptIsUpToDate is the drift guard: the TypeScript
// interfaces in vscode-acline/src/generated.ts are no longer hand-written --
// they're rendered straight from the Go structs listed in
// internal/mcp/tsgen.go by GenerateTypeScript, the same function `go
// generate ./internal/mcp` runs. If someone edits a *Out struct and forgets
// to regenerate, this fails with a byte-for-byte diff instead of the old
// one-directional field-subset check silently missing renames, reorders, or
// newly-required fields.
func TestGeneratedTypeScriptIsUpToDate(t *testing.T) {
	const path = "../../vscode-acline/src/generated.ts"
	got, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("extension source not present: %v", err)
	}
	want := GenerateTypeScript()
	if string(got) != want {
		t.Errorf("%s is stale -- run `go generate ./internal/mcp` and commit the result", path)
	}
}
