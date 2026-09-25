package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"acline/internal/scaffold"
)

var (
	pluginWithMCP bool
	pluginForce   bool
)

var pluginCmd = &cobra.Command{
	Use:   "plugin",
	Short: "Package acline's skills and agents as a Claude Code plugin",
}

var pluginExportCmd = &cobra.Command{
	Use:   "export <dir>",
	Short: "Write a Claude Code plugin with acline's skills and agents to <dir>",
	Long: "Writes a Claude Code plugin (.claude-plugin/plugin.json, skills/, agents/) from the files this build embeds, " +
		"so every repo uses one versioned copy instead of the one `acline init` made. The plugin carries no hooks " +
		"and no settings: a plugin cannot set permissions.deny or the agent identity environment, so `acline init` " +
		"is still what installs the guard in each project. --with-mcp adds a .mcp.json that runs `acline mcp serve --as agent`.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		files, err := scaffold.ExportPlugin(args[0], scaffold.PluginOptions{Version: shortVersionString(), WithMCP: pluginWithMCP, Force: pluginForce})
		if err != nil {
			return err
		}
		fmt.Printf("wrote the acline plugin to %s (%d files)\n", args[0], len(files))
		fmt.Printf("load it with: claude --plugin-dir %s   (still run `acline init` in each project for the guard)\n", args[0])
		return nil
	},
}

func init() {
	pluginExportCmd.Flags().BoolVar(&pluginWithMCP, "with-mcp", false, "also register acline's MCP server (as an agent, capture toolset) in the plugin")
	pluginExportCmd.Flags().BoolVar(&pluginForce, "force", false, "write into a directory that already has files")
	pluginCmd.AddCommand(pluginExportCmd)
	rootCmd.AddCommand(pluginCmd)
}
