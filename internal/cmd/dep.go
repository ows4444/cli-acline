package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"acline/internal/app"
)

func newDepCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dep",
		Short: "Track packages introduced into the project (supply-chain provenance)",
	}
	cmd.AddCommand(newDepAddCmd(c), newDepListCmd(c), newDepVerifyCmd(c))
	return cmd
}

func newDepAddCmd(c *cli) *cobra.Command {
	var (
		depTask     string
		depProject  string
		depVerified bool
	)
	cmd := &cobra.Command{
		Use:   "add <ecosystem> <name[@version]>",
		Short: "Record an added dependency; unverified until confirmed real",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			taskID, err := parseOptionalID(depTask, "task")
			if err != nil {
				return err
			}
			var (
				id            int64
				name, version string
			)
			err = c.withApprovalToken("recording a dependency as already verified", func(token string) error {
				var derr error
				id, name, version, derr = app.AddDependency(c.st, app.AddDependencyRequest{
					Ecosystem: args[0], Name: args[1], TaskID: taskID, ProjectArg: depProject,
					AllowCwdFallback: true, Verified: depVerified, Token: token,
				})
				return derr
			})
			if err != nil {
				return err
			}
			fmt.Printf("dependency #%d recorded: %s %s@%s\n", id, args[0], name, version)
			if !depVerified {
				fmt.Println("warning: unverified — confirm this package exists and is the real published artifact before installing")
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&depTask, "task", "t", "", "task that introduced this dependency")
	cmd.Flags().StringVar(&depProject, "project", "", "project name (default: the --task's project, else resolved from cwd/ACLINE_PROJECT)")
	cmd.Flags().BoolVar(&depVerified, "verified", false, "package confirmed to exist and be the real artifact")
	return cmd
}

func newDepListCmd(c *cli) *cobra.Command {
	var (
		depProject string
	)
	var (
		depUnverifiedOnly bool
	)
	var allProjects bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List recorded dependencies",
		RunE: func(cmd *cobra.Command, args []string) error {
			projectID, err := c.resolveListScope(depProject, allProjects)
			if err != nil {
				return err
			}
			deps, err := c.st.ListDependencies(projectID, depUnverifiedOnly)
			if err != nil {
				return err
			}
			if len(deps) == 0 {
				fmt.Println("no dependencies recorded")
				return nil
			}
			fmt.Printf("%-4s %-10s %-10s %-30s %s\n", "ID", "VERIFIED", "ECOSYSTEM", "NAME", "VERSION")
			for _, d := range deps {
				verified := "no"
				if d.Verified {
					verified = "yes"
				}
				fmt.Printf("%-4d %-10s %-10s %-30s %s\n", d.ID, verified, d.Ecosystem, d.Name, d.Version.String)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&depUnverifiedOnly, "unverified", false, "show only unverified dependencies")
	cmd.Flags().StringVar(&depProject, "project", "", "project name (default: the current project, else every project)")
	cmd.Flags().BoolVar(&allProjects, "all-projects", false, allProjectsUsage)
	return cmd
}

func newDepVerifyCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "verify <id>",
		Short: "Mark a dependency verified as the real published artifact",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], "dependency")
			if err != nil {
				return err
			}
			if err := c.withApprovalToken("verifying a dependency", func(token string) error {
				return app.VerifyDependency(c.st, id, token)
			}); err != nil {
				return err
			}
			fmt.Printf("dependency #%d verified\n", id)
			return nil
		},
	}
	return cmd
}
