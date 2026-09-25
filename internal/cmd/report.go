package cmd

import (
	"bufio"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// --- export ---

var (
	exportSince string
	exportOut   string
)

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export the full audit trail as JSONL (one record per line)",
	RunE: func(cmd *cobra.Command, args []string) error {
		out := os.Stdout
		if exportOut != "" {
			f, err := createPrivate(exportOut) // the full audit trail
			if err != nil {
				return fmt.Errorf("creating export file: %w", err)
			}
			defer f.Close()
			out = f
		}
		w := bufio.NewWriter(out)
		n, err := st.ExportJSONL(w, exportSince)
		if err != nil {
			return err
		}
		if err := w.Flush(); err != nil {
			return err
		}
		if exportOut != "" {
			fmt.Printf("exported %d records to %s\n", n, exportOut)
		}
		return nil
	},
}

// --- metrics ---

var metricsCmd = &cobra.Command{
	Use:   "metrics",
	Short: "Verification-tax view: what was produced, and what checking it took",
	RunE: func(cmd *cobra.Command, args []string) error {
		m, err := st.ComputeMetrics()
		if err != nil {
			return err
		}
		fmt.Printf("tasks:          %d total, %d done\n", m.TasksTotal, m.TasksDone)
		fmt.Printf("  by actor:     ")
		if len(m.TasksByActor) == 0 {
			fmt.Print("none")
		}
		for k, v := range m.TasksByActor {
			fmt.Printf("%s=%d ", k, v)
		}
		fmt.Println()
		fmt.Printf("  reworked:     %d (reached done, then moved back)\n", m.Reworked)
		fmt.Printf("checks:         %d recorded, %d failing\n", m.ChecksTotal, m.ChecksFailed)
		fmt.Printf("approvals:      %d recorded, %d overrides\n", m.ApprovalsTotal, m.Overrides)
		fmt.Printf("events by actor:")
		if len(m.EventsByActor) == 0 {
			fmt.Print(" none")
		}
		for k, v := range m.EventsByActor {
			fmt.Printf(" %s=%d", k, v)
		}
		fmt.Println()
		fmt.Printf("unverified deps: %d\n", m.UnverifiedDeps)
		fmt.Printf("memory pending review: %d\n", m.PendingMemory)
		fmt.Printf("policy violations: %d\n", m.PolicyViolations)
		fmt.Printf("guard denials:  %d\n", m.GuardDenials)
		fmt.Printf("sessions:       %d", m.Sessions)
		if m.TokensIn+m.TokensOut > 0 || m.CostUSD > 0 {
			fmt.Printf(", %d in / %d out tokens, $%.2f", m.TokensIn, m.TokensOut, m.CostUSD)
		}
		fmt.Println()
		if len(m.LatestEvals) > 0 {
			fmt.Println("latest evals:")
			for _, e := range m.LatestEvals {
				fmt.Printf("  %-20s %.1f%%\n", e.Suite, e.PassRate*100)
			}
		}

		if m.ChecksTotal == 0 && m.TasksDone > 0 {
			fmt.Println("\nnote: tasks completed with no verification recorded at all")
		}
		if m.Overrides > 0 {
			fmt.Printf("\nnote: %d gate override(s) recorded — review these\n", m.Overrides)
		}
		if m.PolicyViolations > 0 {
			fmt.Printf("\nnote: %d policy violation(s) recorded — review these\n", m.PolicyViolations)
		}
		if m.GuardDenials > 0 {
			fmt.Printf("\nnote: %d guard denial(s) recorded (blocked tool calls) — review these\n", m.GuardDenials)
		}
		return nil
	},
}

func init() {
	exportCmd.Flags().StringVar(&exportSince, "since", "", "only records at/after this RFC3339 date, e.g. 2026-01-01")
	exportCmd.Flags().StringVarP(&exportOut, "out", "o", "", "write to a file instead of stdout")

	rootCmd.AddCommand(exportCmd, metricsCmd)
}
