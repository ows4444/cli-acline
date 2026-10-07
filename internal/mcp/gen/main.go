// Command gen renders vscode-acline/src/generated.ts from the Go structs
// listed in internal/mcp/tsgen.go. Invoked as `go generate ./internal/mcp`
// (which runs it with the working directory set to internal/mcp), never
// run directly.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"acline/internal/mcp"
)

func main() {
	src := filepath.Join(mcp.ExtensionDir(filepath.Join("..", "..")), "src")
	if _, err := os.Stat(src); err != nil {
		fmt.Fprintf(os.Stderr, "gen: VS Code extension not found (%v): clone vscode-acline next to this repository, or set ACLINE_EXT_DIR\n", err)
		os.Exit(1)
	}
	out := filepath.Join(src, "generated.ts")
	if err := os.WriteFile(out, []byte(mcp.GenerateTypeScript()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
	fmt.Println("wrote", out)
}
