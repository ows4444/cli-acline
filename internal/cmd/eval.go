package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"acline/internal/app"
	"acline/internal/store"
)

func newEvalCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "eval",
		Short: "Record measured accuracy, and use it to justify autonomy promotion",
	}
	cmd.AddCommand(newEvalRecordCmd(c), newEvalListCmd(c), newTaskPromoteCmd(c))
	return cmd
}

func newEvalRecordCmd(c *cli) *cobra.Command {
	var (
		evalTask       string
		evalProject    string
		evalSuite      string
		evalPassRate   float64
		evalSampleSize int64
		evalNote       string
	)
	cmd := &cobra.Command{
		Use:   "record",
		Short: "Record an eval result for a suite",
		RunE: func(cmd *cobra.Command, args []string) error {
			taskID, err := parseOptionalID(evalTask, "task")
			if err != nil {
				return err
			}
			id, err := app.RecordEval(c.st, app.RecordEvalRequest{
				TaskID: taskID, Suite: evalSuite, PassRate: evalPassRate, SampleSize: evalSampleSize, Note: evalNote,
				ProjectArg: evalProject, AllowCwdFallback: true,
			})
			if err != nil {
				return err
			}
			fmt.Printf("eval #%d recorded: %s at %.1f%%\n", id, evalSuite, evalPassRate*100)
			return nil
		},
	}
	cmd.Flags().StringVarP(&evalTask, "task", "t", "", "task this eval relates to")
	cmd.Flags().StringVar(&evalProject, "project", "", "project name (default: resolved from cwd/ACLINE_PROJECT)")
	cmd.Flags().StringVar(&evalSuite, "suite", "", "eval suite name")
	cmd.Flags().Float64Var(&evalPassRate, "pass-rate", 0, "pass rate as 0..1, e.g. 0.94")
	cmd.Flags().Int64Var(&evalSampleSize, "sample-size", 0, "number of cases evaluated")
	cmd.Flags().StringVar(&evalNote, "note", "", "context for this result")
	_ = cmd.MarkFlagRequired("suite")
	_ = cmd.MarkFlagRequired("pass-rate")
	return cmd
}

func newEvalListCmd(c *cli) *cobra.Command {
	var (
		evalProject string
	)
	var (
		evalListSuite string
	)
	var allProjects bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List recorded evals, newest first",
		RunE: func(cmd *cobra.Command, args []string) error {
			projectID, err := c.resolveListScope(evalProject, allProjects)
			if err != nil {
				return err
			}
			evals, err := c.st.ListEvals(projectID, evalListSuite, 50)
			if err != nil {
				return err
			}
			if len(evals) == 0 {
				fmt.Println("no evals recorded")
				return nil
			}
			fmt.Printf("%-4s %-20s %-8s %-8s %s\n", "ID", "SUITE", "PASS", "SAMPLE", "WHEN")
			for _, e := range evals {
				sample := "-"
				if e.SampleSize.Valid {
					sample = fmt.Sprintf("%d", e.SampleSize.Int64)
				}
				fmt.Printf("%-4d %-20s %-8s %-8s %s\n",
					e.ID, e.Suite, fmt.Sprintf("%.1f%%", e.PassRate*100), sample, e.CreatedAt)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&evalListSuite, "suite", "", "filter by suite")
	cmd.Flags().StringVar(&evalProject, "project", "", "project name (default: the current project, else every project)")
	cmd.Flags().BoolVar(&allProjects, "all-projects", false, allProjectsUsage)
	return cmd
}

func newTaskPromoteCmd(c *cli) *cobra.Command {
	var (
		promoteTo        string
		promoteSuite     string
		promoteThreshold float64
	)
	cmd := &cobra.Command{
		Use:   "promote <task-id>",
		Short: "Promote a task's autonomy level, backed by a measured eval pass rate",
		Long: "Promote a task's autonomy level, backed by a measured eval pass rate (acline eval record).\n" +
			"The same command is `acline task promote` and `acline eval promote`.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], "task")
			if err != nil {
				return err
			}
			if _, err := c.st.GetTask(id); err != nil {
				return err
			}
			if err := c.st.PromoteAutonomy(id, promoteTo, promoteSuite, promoteThreshold); err != nil {
				return err
			}
			fmt.Printf("task #%d promoted to autonomy=%s\n", id, promoteTo)
			return nil
		},
	}
	cmd.Flags().StringVar(&promoteTo, "to", "", "hitl|hotl|auto")
	cmd.Flags().StringVar(&promoteSuite, "suite", "", "eval suite that justifies the promotion")
	cmd.Flags().Float64Var(&promoteThreshold, "threshold", store.DefaultPromotionThreshold,
		"minimum pass rate required (0..1)")
	_ = cmd.MarkFlagRequired("to")
	_ = cmd.MarkFlagRequired("suite")
	return cmd
}
