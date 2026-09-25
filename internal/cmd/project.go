package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"acline/internal/app"
	"acline/internal/store"
)

var projectCmd = &cobra.Command{
	Use:   "project",
	Short: "Register and switch between tracked projects",
}

var projectAutonomy string

var projectAddCmd = &cobra.Command{
	Use:   "add <name> <path>",
	Short: "Register a project by name and repo path",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		// A registered path joins the guard's write scope, so it is a person's call.
		var id int64
		err := withApprovalToken("registering project "+args[0], func(token string) error {
			var aerr error
			id, aerr = st.AddProjectWithToken(args[0], args[1], projectAutonomy, token)
			return aerr
		})
		if err != nil {
			return err
		}
		fmt.Printf("project #%d registered: %s (%s)\n", id, args[0], args[1])
		return nil
	},
}

var projectListCmd = &cobra.Command{
	Use:   "list",
	Short: "List registered projects",
	RunE: func(cmd *cobra.Command, args []string) error {
		projects, err := st.ListProjects()
		if err != nil {
			return err
		}
		if len(projects) == 0 {
			fmt.Println("no projects registered")
			return nil
		}
		fmt.Printf("%-4s %-20s %-10s %s\n", "ID", "NAME", "AUTONOMY", "PATH")
		for _, p := range projects {
			fmt.Printf("%-4d %-20s %-10s %s\n", p.ID, p.Name, p.AutonomyDefault, p.Path.String)
		}
		return nil
	},
}

var projectUseCmd = &cobra.Command{
	Use:   "use <name>",
	Short: "Mark <name> as the current project for this directory (writes .acline-project)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := st.GetProjectByName(args[0])
		if err != nil {
			return err
		}
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		// A marker is only honoured inside the project's registered path (see
		// store.ResolveProjectForPath), so writing one elsewhere would silently do nothing.
		if p.Path.Valid && p.Path.String != "" && !store.PathInside(cwd, p.Path.String) {
			return fmt.Errorf("%s is outside project %q's registered path (%s), so a marker here would be ignored: cd into the project, or register this directory with `acline project add`", cwd, p.Name, p.Path.String)
		}
		if err := store.WriteMarker(cwd, args[0]); err != nil {
			return err
		}
		fmt.Printf("current project set to %s (%s)\n", args[0], cwd)
		return nil
	},
}

// resolveProjectFlag is for commands that record something new: an explicit
// --project name wins, otherwise fall back to whatever ResolveCurrentProject
// finds, and finally nil (unscoped) if neither applies. Thin wrapper over
// app.ResolveProject, which is the single implementation shared with
// internal/mcp.
func resolveProjectFlag(explicit string) (*int64, error) {
	return app.ResolveProject(st, explicit, true)
}

// resolveProjectFlagOptional is for list/filter commands: no --project means
// "show everything" (nil, no filter), not "fall back to the current
// project."
func resolveProjectFlagOptional(explicit string) (*int64, error) {
	return app.ResolveProjectOptional(st, explicit)
}

func init() {
	projectAddCmd.Flags().StringVar(&projectAutonomy, "autonomy", "hotl", "default autonomy for this project: hitl|hotl|auto")

	projectCmd.AddCommand(projectAddCmd, projectListCmd, projectUseCmd)
	rootCmd.AddCommand(projectCmd)
}
