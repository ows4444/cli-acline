package cmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"acline/internal/app"
)

var roleCmd = &cobra.Command{
	Use:   "role",
	Short: "Manage roles (developer/qa/designer/manager/scrummaster/architect/security, or a project's own)",
}

var (
	roleAddKind        string
	roleAddCanApprove  bool
	roleAddStage       int
	roleAddDescription string
	roleProject        string
)

var roleAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Add a project-scoped role",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, err := resolveProjectFlag(roleProject)
		if err != nil {
			return err
		}
		if projectID == nil {
			return fmt.Errorf("a role needs a project (use --project or run from inside a tracked project) -- the 7 built-in roles (developer/qa/designer/manager/scrummaster/architect/security) already cover the global case")
		}
		var stageOrder *int64
		if cmd.Flags().Changed("stage") {
			s := int64(roleAddStage)
			stageOrder = &s
		}
		var id int64
		err = withApprovalToken("creating a can_approve role", func(token string) error {
			var aerr error
			id, aerr = st.AddRoleWithToken(*projectID, args[0], roleAddKind, roleAddCanApprove, stageOrder, roleAddDescription, token)
			return aerr
		})
		if err != nil {
			return err
		}
		fmt.Printf("role #%d added: %s\n", id, args[0])
		return nil
	},
}

var roleListCmd = &cobra.Command{
	Use:   "list",
	Short: "List roles (global built-ins plus this project's own)",
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, err := resolveProjectFlagOptional(roleProject)
		if err != nil {
			return err
		}
		roles, err := st.ListRoles(projectID)
		if err != nil {
			return err
		}
		if len(roles) == 0 {
			fmt.Println("no roles (this shouldn't happen -- the 7 built-ins are seeded on every store)")
			return nil
		}
		fmt.Printf("%-4s %-14s %-8s %-11s %-6s %s\n", "ID", "NAME", "KIND", "CAN_APPROVE", "STAGE", "SCOPE")
		for _, r := range roles {
			scope := "global"
			if r.ProjectID.Valid {
				scope = "project"
			}
			stage := "-"
			if r.StageOrder.Valid {
				stage = strconv.FormatInt(r.StageOrder.Int64, 10)
			}
			canApprove := "no"
			if r.CanApprove {
				canApprove = "yes"
			}
			fmt.Printf("%-4d %-14s %-8s %-11s %-6s %s\n", r.ID, r.Name, r.Kind, canApprove, stage, scope)
		}
		return nil
	},
}

// resolveRoleFlag is the shared --role resolver every command that accepts
// a role calls: st.ResolveRole already implements the flag > $ACLINE_ROLE >
// active session precedence (see internal/store/role.go); this just
// threads the current project scope through (with cwd fallback, the CLI's
// behavior) so a project's own role can shadow a global one of the same
// name. Thin wrapper over app.ResolveRole, shared with internal/mcp.
func resolveRoleFlag(explicit, projectFlag string) (*int64, error) {
	return app.ResolveRole(st, explicit, projectFlag, true)
}

func init() {
	roleCmd.PersistentFlags().StringVar(&roleProject, "project", "", "project name (default: resolved from cwd/ACLINE_PROJECT)")
	roleAddCmd.Flags().StringVar(&roleAddKind, "kind", "both", "human|agent|both")
	roleAddCmd.Flags().BoolVar(&roleAddCanApprove, "can-approve", false, "may satisfy the gate's approval requirement")
	roleAddCmd.Flags().IntVar(&roleAddStage, "stage", 0, "advisory pipeline position (a hint only, never enforced)")
	roleAddCmd.Flags().StringVar(&roleAddDescription, "description", "", "what this role is for")
	roleCmd.AddCommand(roleAddCmd, roleListCmd)
	rootCmd.AddCommand(roleCmd)
}
