package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"acline/internal/store"
)

var (
	routeProject string
	routeJSON    bool
)

// routeView is the --json shape of a store.Route: plain types only.
type routeView struct {
	TaskID        int64            `json:"task_id"`
	Title         string           `json:"title"`
	Status        string           `json:"status"`
	Action        string           `json:"action"`
	Reason        string           `json:"reason"`
	NeedsHuman    bool             `json:"needs_human"`
	Role          *string          `json:"role"`
	SpecID        *int64           `json:"spec_id"`
	WaitingOn     []string         `json:"waiting_on"`
	Blockers      []string         `json:"blockers"`
	OpenCriteria  []string         `json:"open_criteria"`
	FailingChecks []string         `json:"failing_checks"`
	Lessons       []memoryJSONView `json:"lessons"`
}

func newRouteView(r *store.Route) routeView {
	v := routeView{
		TaskID: r.TaskID, Title: r.Title, Status: r.Status, Action: r.Action, Reason: r.Reason,
		NeedsHuman: r.NeedsHuman, SpecID: r.SpecID,
		WaitingOn: nonNil(r.WaitingOn), Blockers: nonNil(r.Blockers), OpenCriteria: nonNil(r.OpenCriteria), FailingChecks: nonNil(r.FailingChecks),
		Lessons: []memoryJSONView{},
	}
	if r.Role != nil {
		v.Role = &r.Role.Name
	}
	for _, m := range r.Lessons {
		v.Lessons = append(v.Lessons, newMemoryJSONView(m))
	}
	return v
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func printRoute(r *store.Route) {
	fmt.Printf("task #%d  %s  [%s]\n", r.TaskID, r.Title, r.Status)
	who := "any agent"
	if r.Role != nil {
		who = "role " + r.Role.Name
	}
	if r.NeedsHuman {
		who = "a human (" + who + ")"
	}
	fmt.Printf("next: %s — %s\n", r.Action, r.Reason)
	if r.Action != store.RouteNone {
		fmt.Printf("who:  %s\n", who)
	}
	for _, p := range r.WaitingOn {
		fmt.Printf("  waiting on: %s\n", p)
	}
	for _, b := range r.Blockers {
		fmt.Printf("  blocker: %s\n", b)
	}
	for _, c := range r.FailingChecks {
		fmt.Printf("  failing: %s\n", c)
	}
	for _, c := range r.OpenCriteria {
		fmt.Printf("  open criterion: %s\n", c)
	}
	for _, m := range r.Lessons {
		fmt.Printf("  lesson #%d [%s] %s\n", m.ID, m.Kind, m.Body)
	}
}

var nextCmd = &cobra.Command{
	Use:     "next [task-id]",
	Aliases: []string{"route"},
	Short:   "Say what should happen next (and by which role) for a task, or pick the next task",
	Long: "With a task id, derive that task's next step from its recorded state: spec, criteria, checks and gate.\n" +
		"Without one, pick the highest-priority open task an agent can act on (falling back to one waiting on a person).\n" +
		"Advisory only: nothing enforces that the suggested role acts.",
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var r *store.Route
		if len(args) == 1 {
			id, err := parseID(args[0], "task")
			if err != nil {
				return err
			}
			if r, err = st.RouteTask(id); err != nil {
				return err
			}
		} else {
			projectID, err := resolveProjectFlagOptional(routeProject)
			if err != nil {
				return err
			}
			if r, err = st.NextTask(projectID); err != nil {
				return err
			}
			if r == nil {
				if routeJSON {
					return printJSON(nil)
				}
				fmt.Println("no open tasks")
				return nil
			}
		}
		if routeJSON {
			return printJSON(newRouteView(r))
		}
		printRoute(r)
		return nil
	},
}

func init() {
	nextCmd.Flags().StringVar(&routeProject, "project", "", "project name (only when no task id is given)")
	nextCmd.Flags().BoolVar(&routeJSON, "json", false, "print the route as JSON")
	rootCmd.AddCommand(nextCmd)
}
