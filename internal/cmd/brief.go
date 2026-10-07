package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"acline/internal/brief"
)

// briefView is the --json shape: the route plus the rendered document.
type briefView struct {
	routeView
	Markdown string `json:"markdown"`
}

func newBriefCmd(c *cli) *cobra.Command {
	var (
		briefProject string
		briefJSON    bool
	)
	var allProjects bool
	cmd := &cobra.Command{
		Use:   "brief [task-id]",
		Short: "Assemble everything an agent needs for a task's next step",
		Long: "Prints one document with the routed step, the task, its spec and decision, acceptance criteria, failing checks,\n" +
			"approved lessons, the role's behavior contract and the standing rules. It only reads: nothing is stored or launched.\n" +
			"Without a task id it briefs the task `acline next` would pick. Paste it into any agent session.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var id int64
			if len(args) == 1 {
				var err error
				if id, err = parseID(args[0], "task"); err != nil {
					return err
				}
			} else {
				projectID, err := c.resolveListScope(briefProject, allProjects)
				if err != nil {
					return err
				}
				next, err := c.st.NextTask(projectID)
				if err != nil {
					return err
				}
				if next == nil {
					fmt.Println("no open tasks")
					return nil
				}
				id = next.TaskID
			}
			b, err := brief.Build(c.st, id)
			if err != nil {
				return err
			}
			if briefJSON {
				return printJSON(briefView{routeView: newRouteView(b.Route), Markdown: b.Markdown()})
			}
			fmt.Print(b.Markdown())
			return nil
		},
	}
	cmd.Flags().StringVar(&briefProject, "project", "", "project name (only when no task id is given)")
	cmd.Flags().BoolVar(&allProjects, "all-projects", false, allProjectsUsage)
	cmd.Flags().BoolVar(&briefJSON, "json", false, "print the route and the rendered document as JSON")
	return cmd
}
