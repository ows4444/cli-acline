package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	aclinemcp "acline/internal/mcp"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Run acline as an MCP (Model Context Protocol) server",
}

var mcpServeCmd = &cobra.Command{
	Use:   "serve",
	Short: "Serve acline's search/browse/capture tools over MCP on stdio",
	Long: "Exposes acline's store (search, memory, tasks, decisions, specs, notes/log, and the audit " +
		"trail) as MCP tools/resources over stdio, for any MCP-compatible client (Claude Desktop, " +
		"another agent, an editor extension) — not just Claude Code's hook-based integration. " +
		"Semantic search is available exactly when VOYAGE_API_KEY is set on this process, same as " +
		"the CLI's `search --semantic`. The server records writes under one identity, which must be " +
		"declared: `--as human` for an editor a person drives, `--as agent` for an agent's client, " +
		"or ACLINE_ACTOR_TYPE / ACLINE_MODEL in the environment. `--as` cannot turn an agent " +
		"environment into a person.",
	RunE: func(cmd *cobra.Command, args []string) error {
		actorType, err := mcpServeActorType(mcpAs, os.Getenv)
		if err != nil {
			return err
		}
		st.Actor.Type = actorType
		toolset := mcpToolset
		if toolset == "" {
			toolset = aclinemcp.DefaultToolset(st.Actor)
		}
		server, err := aclinemcp.NewServerWithToolset(st, toolset)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithCancel(cmd.Context())
		defer cancel()
		go aclinemcp.WatchStore(ctx, server, st, 2*time.Second)
		if err := server.Run(ctx, &sdkmcp.StdioTransport{}); err != nil {
			return fmt.Errorf("mcp server: %w", err)
		}
		return nil
	},
}

var mcpToolset, mcpAs string

// mcpServeActorType decides who a server records writes as. An undeclared
// identity used to mean "a person", so any client that forgot to say it was an
// agent got a person's server; now it has to be said. The environment wins
// over --as when it declares an agent: otherwise an agent's shell could pipe
// requests into `acline mcp serve --as human` and act as a person.
func mcpServeActorType(as string, getenv func(string) string) (string, error) {
	if as != "" && as != "human" && as != "agent" {
		return "", fmt.Errorf("--as must be human or agent, not %q", as)
	}
	env := getenv("ACLINE_ACTOR_TYPE")
	if env == "" && getenv("ACLINE_MODEL") != "" {
		env = "agent"
	}
	switch {
	case env == "" && as == "":
		return "", fmt.Errorf("acline mcp serve needs an explicit identity: pass --as human (an editor a person drives) or --as agent, or set ACLINE_ACTOR_TYPE")
	case env == "":
		return as, nil
	case as == "" || as == env:
		return env, nil
	case as == "agent": // serving with less authority than the environment's is always allowed
		return as, nil
	case env == "agent":
		return "", fmt.Errorf("--as %s refused: this environment declares an agent (ACLINE_ACTOR_TYPE/ACLINE_MODEL), and an agent cannot serve as a person", as)
	default:
		return "", fmt.Errorf("--as %s conflicts with ACLINE_ACTOR_TYPE=%s", as, env)
	}
}

func init() {
	mcpServeCmd.Flags().StringVar(&mcpAs, "as", "", "identity to record writes under when the environment doesn't declare one: human or agent")
	mcpServeCmd.Flags().StringVar(&mcpToolset, "toolset", "", "tools to expose: read, capture (read + recording work, no approvals/decisions) or all (default: capture for an agent actor, all for a person)")
	mcpCmd.AddCommand(mcpServeCmd)
	rootCmd.AddCommand(mcpCmd)
}
