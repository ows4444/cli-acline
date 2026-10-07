package cmd

import (
	"bufio"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
)

// --- export ---

func newExportCmd(c *cli) *cobra.Command {
	var (
		exportSince string
		exportOut   string
	)
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export the full audit trail as JSONL (one record per line)",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Dates are compared as text: anything else would silently match
			// every record or none.
			if exportSince != "" {
				if _, err := time.Parse(time.RFC3339, exportSince); err != nil {
					if _, err := time.Parse(time.DateOnly, exportSince); err != nil {
						return fmt.Errorf("--since %q is not a date: use 2026-01-01 or 2026-01-01T00:00:00Z", exportSince)
					}
				}
			}
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
			n, err := c.st.ExportJSONL(w, exportSince)
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
	cmd.Flags().StringVar(&exportSince, "since", "", "only records at/after this RFC3339 date, e.g. 2026-01-01")
	cmd.Flags().StringVarP(&exportOut, "out", "o", "", "write to a file instead of stdout")
	return cmd
}

// --- metrics ---

func newMetricsCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "metrics",
		Short: "Verification-tax view: what was produced, and what checking it took",
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := c.st.ComputeMetrics()
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
	return cmd
}
