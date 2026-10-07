package cmd

import (
	"bufio"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func newContextCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "context",
		Short: "Generate the agent-facing context file from the database",
	}
	cmd.AddCommand(newContextExportCmd(c))
	return cmd
}

func newContextExportCmd(c *cli) *cobra.Command {
	var (
		contextOut     string
		contextTitle   string
		contextVault   string
		contextProject string
		contextMaxRows int
	)
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Render constraints, decisions, specs, capabilities and open work as markdown",
		Long: `Writes an AGENTS.md/CLAUDE.md-style context file built from the database.

The database is the source of truth and the markdown is a build artifact, so the
file agents read cannot drift away from the recorded state the way a
hand-maintained instruction file does. Regenerate it rather than editing it.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			projectID, err := c.resolveProjectFlag(contextProject)
			if err != nil {
				return err
			}
			out := os.Stdout
			if contextOut != "" {
				f, err := os.Create(contextOut)
				if err != nil {
					return fmt.Errorf("creating context file: %w", err)
				}
				defer f.Close()
				out = f
			}
			w := bufio.NewWriter(out)
			vault := contextVault
			if vault == "" {
				vault = defaultVaultPath()
			}
			if err := c.st.RenderContextLimited(w, contextTitle, projectID, contextMaxRows, vault); err != nil {
				return err
			}
			if err := w.Flush(); err != nil {
				return err
			}
			if contextOut != "" {
				fmt.Printf("wrote %s\n", contextOut)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&contextOut, "out", "o", "", "write to a file (e.g. AGENTS.md) instead of stdout")
	cmd.Flags().StringVar(&contextTitle, "title", "", "document title")
	cmd.Flags().StringVar(&contextVault, "vault", "", "path to the vault directory (default: $VAULT_PATH or ./vault)")
	cmd.Flags().IntVar(&contextMaxRows, "max-rows", 0, "list at most this many rows of open work and of capabilities (0 = all); the rest are counted")
	cmd.Flags().StringVar(&contextProject, "project", "", "project name (default: resolved from cwd/ACLINE_PROJECT; unset falls back to unscoped/all-projects)")
	return cmd
}

// --- chain verification ---
