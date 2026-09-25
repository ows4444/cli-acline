package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"acline/internal/app"
)

var dashboardProject string

var dashboardCmd = &cobra.Command{
	Use:   "dashboard",
	Short: "Bootstrap view for a new session: session, open work, decisions, memory, pending review",
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, err := resolveProjectFlag(dashboardProject)
		if err != nil {
			return err
		}
		d, err := app.Dashboard(st, projectID)
		if err != nil {
			return err
		}

		switch {
		case d.Session != nil:
			sess := d.Session
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
		default:
			fmt.Println("no active session")
		}
		fmt.Printf("acting as: %s/%s", st.Actor.Type, st.Actor.ID)
		if st.Actor.Model != "" {
			fmt.Printf(" (%s)", st.Actor.Model)
		}
		fmt.Println()

		if !d.ApprovalTokenEnabled {
			fmt.Println("WARNING: no approval token is enabled, so identity is self-declared: anything that sets ACLINE_ACTOR_TYPE=human")
			fmt.Println("         can approve work and override gates. Enforce it: run `acline auth init` in a terminal.")
		}

		fmt.Printf("\nopen tasks: %d total", len(d.Tasks))
		if len(d.TasksNeedingAttention) > 0 {
			fmt.Printf(", %d needing attention:\n", len(d.TasksNeedingAttention))
			for _, t := range d.TasksNeedingAttention {
				fmt.Printf("  #%-4d [%-11s] %-6s risk=%-8s %s\n", t.ID, t.Status, t.Priority, t.Risk, t.Title)
			}
		} else {
			fmt.Println(", none urgent/high-risk")
		}

		if len(d.ApprovedSpecs) > 0 {
			fmt.Printf("\napproved specs (%d):\n", len(d.ApprovedSpecs))
			for _, sp := range d.ApprovedSpecs {
				fmt.Printf("  #%-4d v%-3d %s\n", sp.ID, sp.Version, sp.Title)
			}
		}

		if len(d.AcceptedDecisions) > 0 {
			fmt.Printf("\naccepted decisions (%d):\n", len(d.AcceptedDecisions))
			for _, dec := range d.AcceptedDecisions {
				fmt.Printf("  #%-4d %s\n", dec.ID, dec.Title)
			}
		}

		if len(d.Memory) > 0 {
			fmt.Printf("\nmemory (%d):\n", len(d.Memory))
			for _, m := range d.Memory {
				fmt.Printf("  #%-4d [%s] %s\n", m.ID, m.Kind, m.Body)
			}
		}

		if len(d.SpecsAwaitingPlan) > 0 {
			fmt.Printf("\n%d approved spec(s) have no plan or tasks yet:\n", len(d.SpecsAwaitingPlan))
			for _, sp := range d.SpecsAwaitingPlan {
				fmt.Printf("  #%-4d %s — propose one: acline plan propose %d --file plan.json\n", sp.ID, sp.Title, sp.ID)
			}
		}
		if len(d.DraftPlans) > 0 {
			fmt.Printf("\n%d draft plan(s) awaiting review:\n", len(d.DraftPlans))
			for _, p := range d.DraftPlans {
				fmt.Printf("  plan #%-4d for spec #%d — review: acline plan show %d\n", p.ID, p.SpecID, p.ID)
			}
		}

		if d.PendingMemoryCount > 0 {
			fmt.Printf("\n%d memory entr%s pending review — run: acline memory review\n",
				d.PendingMemoryCount, plural(d.PendingMemoryCount, "y", "ies"))
			if d.DraftedMemoryCount > 0 {
				fmt.Printf("  (%d auto-drafted from failed checks/rejections)\n", d.DraftedMemoryCount)
			}
		}

		if d.DecayingMemoryCount > 0 {
			fmt.Printf("\n%d memory entr%s not reconfirmed in %d+ days — run: acline memory decay\n",
				d.DecayingMemoryCount, plural(d.DecayingMemoryCount, "y", "ies"), defaultMemoryDecayDays)
		}

		if len(d.UnverifiedDeps) > 0 {
			fmt.Printf("\n%d unverified dependenc%s — run: acline dep list --unverified\n",
				len(d.UnverifiedDeps), plural(len(d.UnverifiedDeps), "y", "ies"))
		}
		return nil
	},
}

func init() {
	dashboardCmd.Flags().StringVar(&dashboardProject, "project", "", "project name (default: resolved from cwd/ACLINE_PROJECT; unset falls back to unscoped)")
	rootCmd.AddCommand(dashboardCmd)
}
