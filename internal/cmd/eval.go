package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"acline/internal/store"
)

var evalCmd = &cobra.Command{
	Use:   "eval",
	Short: "Record measured accuracy, and use it to justify autonomy promotion",
}

var (
	evalTask       string
	evalProject    string
	evalSuite      string
	evalPassRate   float64
	evalSampleSize int64
	evalNote       string
)

var evalRecordCmd = &cobra.Command{
	Use:   "record",
	Short: "Record an eval result for a suite",
	RunE: func(cmd *cobra.Command, args []string) error {
		var taskID *int64
		if evalTask != "" {
			id, err := parseID(evalTask, "task")
			if err != nil {
				return err
			}
			if _, err := st.GetTask(id); err != nil {
				return err
			}
			taskID = &id
		}
		projectID, err := resolveProjectFlag(evalProject)
		if err != nil {
			return err
		}
		id, err := st.AddEval(taskID, projectID, evalSuite, evalPassRate, evalSampleSize, evalNote)
		if err != nil {
			return err
		}
		fmt.Printf("eval #%d recorded: %s at %.1f%%\n", id, evalSuite, evalPassRate*100)
		return nil
	},
}

var evalListSuite string

var evalListCmd = &cobra.Command{
	Use:   "list",
	Short: "List recorded evals, newest first",
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, err := resolveProjectFlagOptional(evalProject)
		if err != nil {
			return err
		}
		evals, err := st.ListEvals(projectID, evalListSuite, 50)
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

var (
	promoteTo        string
	promoteSuite     string
	promoteThreshold float64
)

var taskPromoteCmd = &cobra.Command{
	Use:   "promote <task-id>",
	Short: "Promote a task's autonomy level, backed by a measured eval pass rate",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := parseID(args[0], "task")
		if err != nil {
			return err
		}
		if _, err := st.GetTask(id); err != nil {
			return err
		}
		if err := st.PromoteAutonomy(id, promoteTo, promoteSuite, promoteThreshold); err != nil {
			return err
		}
		fmt.Printf("task #%d promoted to autonomy=%s\n", id, promoteTo)
		return nil
	},
}

func init() {
	evalRecordCmd.Flags().StringVarP(&evalTask, "task", "t", "", "task this eval relates to")
	evalRecordCmd.Flags().StringVar(&evalProject, "project", "", "project name (default: resolved from cwd/ACLINE_PROJECT)")
	evalRecordCmd.Flags().StringVar(&evalSuite, "suite", "", "eval suite name")
	evalRecordCmd.Flags().Float64Var(&evalPassRate, "pass-rate", 0, "pass rate as 0..1, e.g. 0.94")
	evalRecordCmd.Flags().Int64Var(&evalSampleSize, "sample-size", 0, "number of cases evaluated")
	evalRecordCmd.Flags().StringVar(&evalNote, "note", "", "context for this result")
	_ = evalRecordCmd.MarkFlagRequired("suite")
	_ = evalRecordCmd.MarkFlagRequired("pass-rate")

	evalListCmd.Flags().StringVar(&evalListSuite, "suite", "", "filter by suite")
	evalListCmd.Flags().StringVar(&evalProject, "project", "", "filter by project name")

	taskPromoteCmd.Flags().StringVar(&promoteTo, "to", "", "hitl|hotl|auto")
	taskPromoteCmd.Flags().StringVar(&promoteSuite, "suite", "", "eval suite that justifies the promotion")
	taskPromoteCmd.Flags().Float64Var(&promoteThreshold, "threshold", store.DefaultPromotionThreshold,
		"minimum pass rate required (0..1)")
	_ = taskPromoteCmd.MarkFlagRequired("to")
	_ = taskPromoteCmd.MarkFlagRequired("suite")

	evalCmd.AddCommand(evalRecordCmd, evalListCmd)
	taskCmd.AddCommand(taskPromoteCmd)
	rootCmd.AddCommand(evalCmd)
}
