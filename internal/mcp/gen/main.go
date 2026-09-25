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
	out := filepath.Join("..", "..", "vscode-acline", "src", "generated.ts")
	if err := os.WriteFile(out, []byte(mcp.GenerateTypeScript()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
	fmt.Println("wrote", out)
}
