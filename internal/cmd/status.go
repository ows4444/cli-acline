package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"acline/internal/store"
)

var statusProject string

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Active session and open tasks",
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, err := resolveProjectFlag(statusProject)
		if err != nil {
			return err
		}
		sess, err := st.CurrentSession()
		switch {
		case err == nil:
			fmt.Printf("active session: #%d (started %s", sess.ID, sess.StartedAt)
			if sess.TaskID.Valid {
				fmt.Printf(", task #%d", sess.TaskID.Int64)
			}
			if sess.ProjectID.Valid {
				fmt.Printf(", project #%d", sess.ProjectID.Int64)
				if projectID != nil && *projectID != sess.ProjectID.Int64 {
					fmt.Print(" — different from the project this view is scoped to")
				}
			}
			fmt.Println(")")
		case errors.Is(err, store.ErrNoActiveSession):
			fmt.Println("no active session")
		default:
			return err
		}

		fmt.Println()
		tasks, err := st.ListTasks(store.TaskFilter{ProjectID: projectID})
		if err != nil {
			return err
		}
		if len(tasks) == 0 {
			fmt.Println("no open tasks")
			return nil
		}
		fmt.Printf("open tasks (%d):\n", len(tasks))
		for _, t := range tasks {
			fmt.Printf("  #%-4d [%-11s] %-7s %s\n", t.ID, t.Status, t.Priority, t.Title)
		}
		return nil
	},
}

func init() {
	statusCmd.Flags().StringVar(&statusProject, "project", "", "project name (default: resolved from cwd/ACLINE_PROJECT; unset falls back to unscoped)")
	rootCmd.AddCommand(statusCmd)
}
