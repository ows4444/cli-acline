package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var depCmd = &cobra.Command{
	Use:   "dep",
	Short: "Track packages introduced into the project (supply-chain provenance)",
}

var (
	depTask     string
	depProject  string
	depVerified bool
)

var depAddCmd = &cobra.Command{
	Use:   "add <ecosystem> <name[@version]>",
	Short: "Record an added dependency; unverified until confirmed real",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		ecosystem := args[0]
		name, version := args[1], ""
		// Handle scoped npm names (@scope/pkg@1.0.0) by splitting on the last '@'.
		if i := strings.LastIndex(name, "@"); i > 0 {
			version = name[i+1:]
			name = name[:i]
		}
		var taskID *int64
		if depTask != "" {
			id, err := parseID(depTask, "task")
			if err != nil {
				return err
			}
			if _, err := st.GetTask(id); err != nil {
				return err
			}
			taskID = &id
		}
		projectID, err := resolveProjectFlag(depProject)
		if err != nil {
			return err
		}
		var id int64
		err = withApprovalToken("recording a dependency as already verified", func(token string) error {
			var derr error
			id, derr = st.AddDependencyWithToken(taskID, projectID, ecosystem, name, version, depVerified, token)
			return derr
		})
		if err != nil {
			return err
		}
		if taskID != nil {
			logTaskEvent(*taskID, "dependency_added", fmt.Sprintf("%s %s@%s", ecosystem, name, version))
		} else {
			logEventGlobal("dependency_added", fmt.Sprintf("%s %s@%s", ecosystem, name, version))
		}
		fmt.Printf("dependency #%d recorded: %s %s@%s\n", id, ecosystem, name, version)
		if !depVerified {
			fmt.Println("warning: unverified — confirm this package exists and is the real published artifact before installing")
		}
		return nil
	},
}

var depUnverifiedOnly bool

var depListCmd = &cobra.Command{
	Use:   "list",
	Short: "List recorded dependencies",
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, err := resolveProjectFlagOptional(depProject)
		if err != nil {
			return err
		}
		deps, err := st.ListDependencies(projectID, depUnverifiedOnly)
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

var depVerifyCmd = &cobra.Command{
	Use:   "verify <id>",
	Short: "Mark a dependency verified as the real published artifact",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := parseID(args[0], "dependency")
		if err != nil {
			return err
		}
		if err := withApprovalToken("verifying a dependency", func(token string) error {
			return st.VerifyDependencyWithToken(id, token)
		}); err != nil {
			return err
		}
		fmt.Printf("dependency #%d verified\n", id)
		return nil
	},
}

func init() {
	depAddCmd.Flags().StringVarP(&depTask, "task", "t", "", "task that introduced this dependency")
	depAddCmd.Flags().StringVar(&depProject, "project", "", "project name (default: resolved from cwd/ACLINE_PROJECT)")
	depAddCmd.Flags().BoolVar(&depVerified, "verified", false, "package confirmed to exist and be the real artifact")

	depListCmd.Flags().BoolVar(&depUnverifiedOnly, "unverified", false, "show only unverified dependencies")
	depListCmd.Flags().StringVar(&depProject, "project", "", "filter by project name")

	depCmd.AddCommand(depAddCmd, depListCmd, depVerifyCmd)
	rootCmd.AddCommand(depCmd)
}
