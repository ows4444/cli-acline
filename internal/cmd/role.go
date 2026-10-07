package cmd

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"acline/internal/app"
)

func newRoleCmd(c *cli) *cobra.Command {
	var (
		roleProject string
	)
	cmd := &cobra.Command{
		Use:   "role",
		Short: "Manage roles (developer/qa/designer/manager/scrummaster/architect/security, or a project's own)",
	}
	cmd.PersistentFlags().StringVar(&roleProject, "project", "", "project name (default: resolved from cwd/ACLINE_PROJECT)")
	cmd.AddCommand(newRoleAddCmd(c, &roleProject), newRoleListCmd(c, &roleProject))
	return cmd
}

func newRoleAddCmd(c *cli, roleProject *string) *cobra.Command {
	var (
		roleAddKind        string
		roleAddCanApprove  bool
		roleAddStage       int
		roleAddDescription string
	)
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add a project-scoped role",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var stageOrder *int64
			if cmd.Flags().Changed("stage") {
				s := int64(roleAddStage)
				stageOrder = &s
			}
			var id int64
			err := c.withApprovalToken("creating a can_approve role", func(token string) error {
				var aerr error
				id, aerr = app.AddRole(c.st, app.AddRoleRequest{
					Name: args[0], Kind: roleAddKind, CanApprove: roleAddCanApprove, StageOrder: stageOrder,
					Description: roleAddDescription, Token: token, ProjectArg: *roleProject, AllowCwdFallback: true,
				})
				return aerr
			})
			if errors.Is(err, app.ErrRoleNeedsProject) {
				return fmt.Errorf("a role needs a project (use --project or run from inside a tracked project) -- the 7 built-in roles (developer/qa/designer/manager/scrummaster/architect/security) already cover the global case")
			}
			if err != nil {
				return err
			}
			fmt.Printf("role #%d added: %s\n", id, args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&roleAddKind, "kind", "both", "human|agent|both")
	cmd.Flags().BoolVar(&roleAddCanApprove, "can-approve", false, "may satisfy the gate's approval requirement")
	cmd.Flags().IntVar(&roleAddStage, "stage", 0, "advisory pipeline position (a hint only, never enforced)")
	cmd.Flags().StringVar(&roleAddDescription, "description", "", "what this role is for")
	return cmd
}

func newRoleListCmd(c *cli, roleProject *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List roles (global built-ins plus this project's own)",
		RunE: func(cmd *cobra.Command, args []string) error {
			projectID, err := c.resolveProjectFlag(*roleProject)
			if err != nil {
				return err
			}
			roles, err := c.st.ListRoles(projectID)
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
	return cmd
}
