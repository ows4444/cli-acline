package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"acline/internal/store"
)

var planCmd = &cobra.Command{
	Use:   "plan",
	Short: "Propose a task graph for an approved spec; a person approves it into real tasks",
	Long: "A plan is data, not tasks. `plan propose` records a draft task graph (items, dependencies, parents, criteria,\n" +
		"milestones) for an approved spec; nothing can be routed, briefed or launched from it. `plan approve` -- a person's\n" +
		"decision, never an agent's -- creates the real tasks, links, criteria and milestones in one transaction.\n\n" +
		"Plans are JSON: {\"note\":\"...\",\"items\":[{\"ref\":\"T1\",\"title\":\"...\",\"description\":\"...\",\"area\":\"store\",\n" +
		"\"type\":\"feature\",\"risk\":\"low\",\"autonomy\":\"hotl\",\"parent\":\"T0\",\"milestone\":\"v1\",\"size\":\"M\",\n" +
		"\"depends_on\":[\"T2\"],\"criteria\":[\"When X, the system shall Y\"]}]}. A plan cannot grant autonomy \"auto\"\n" +
		"and is capped at 30 items.",
}

var planFile string

func readPlanFile() (store.PlanInput, error) {
	if planFile == "" {
		return store.PlanInput{}, errors.New("pass --file <plan.json> (or - for stdin)")
	}
	var r io.Reader = os.Stdin
	if planFile != "-" {
		f, err := os.Open(planFile)
		if err != nil {
			return store.PlanInput{}, err
		}
		defer f.Close()
		r = f
	}
	return store.ParsePlanJSON(r)
}

var planProposeCmd = &cobra.Command{
	Use:   "propose <spec-id> --file plan.json",
	Short: "Record a draft plan for an approved spec",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		specID, err := parseID(args[0], "spec")
		if err != nil {
			return err
		}
		in, err := readPlanFile()
		if err != nil {
			return err
		}
		id, err := st.ProposePlan(specID, in)
		if err != nil {
			return err
		}
		fmt.Printf("plan #%d proposed for spec #%d (%d item(s)) — review with: acline plan show %d\n", id, specID, len(in.Items), id)
		return nil
	},
}

var planReviseCmd = &cobra.Command{
	Use:   "revise <plan-id> --file plan.json",
	Short: "Replace a draft plan with a new version",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		planID, err := parseID(args[0], "plan")
		if err != nil {
			return err
		}
		in, err := readPlanFile()
		if err != nil {
			return err
		}
		id, err := st.RevisePlan(planID, in)
		if err != nil {
			return err
		}
		fmt.Printf("plan #%d supersedes #%d — review with: acline plan show %d\n", id, planID, id)
		return nil
	},
}

var planListSpec string
var planListStatus string

var planListCmd = &cobra.Command{
	Use:   "list",
	Short: "List plans",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		var specID *int64
		if planListSpec != "" {
			id, err := parseID(planListSpec, "spec")
			if err != nil {
				return err
			}
			specID = &id
		}
		plans, err := st.ListPlans(specID, planListStatus)
		if err != nil {
			return err
		}
		if len(plans) == 0 {
			fmt.Println("no plans")
			return nil
		}
		for _, p := range plans {
			fmt.Printf("#%-4d spec #%-4d v%-2d %-10s %s\n", p.ID, p.SpecID, p.Version, p.Status, p.CreatedAt)
		}
		return nil
	},
}

var planShowCmd = &cobra.Command{
	Use:   "show <plan-id>",
	Short: "Show a plan's items, dependencies and criteria, and what approving it would create",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := parseID(args[0], "plan")
		if err != nil {
			return err
		}
		p, err := st.GetPlan(id)
		if err != nil {
			return err
		}
		items, err := st.PlanItems(id)
		if err != nil {
			return err
		}
		spec, _ := st.GetSpec(p.SpecID)
		title := ""
		if spec != nil {
			title = " " + spec.Title
		}
		by := ""
		if p.ActorID.Valid {
			by = fmt.Sprintf(" by %s/%s", p.ActorType.String, p.ActorID.String)
		}
		fmt.Printf("plan #%d  v%d  [%s]  spec #%d%s (v%d)%s\n", p.ID, p.Version, p.Status, p.SpecID, title, p.SpecVersion, by)
		if p.Note.Valid {
			fmt.Printf("note: %s\n", p.Note.String)
		}
		if p.Status == "draft" && spec != nil && (spec.Version != p.SpecVersion || spec.Status != "approved") {
			fmt.Printf("STALE: the spec is now v%d (%s); this plan cannot be approved — revise it\n", spec.Version, spec.Status)
		}
		kept := 0
		for _, it := range items {
			if !it.Dropped {
				kept++
			}
		}
		if p.Status == "draft" {
			fmt.Printf("\n%d item(s); approving creates %d task(s):\n", len(items), kept)
		} else {
			fmt.Printf("\n%d item(s):\n", len(items))
		}
		for _, it := range items {
			flags := []string{"risk " + it.Risk, it.Autonomy}
			if it.Size.Valid {
				flags = append(flags, "size "+it.Size.String)
			}
			line := fmt.Sprintf("  %-6s %s  [%s]", it.Ref, it.Title, strings.Join(flags, ", "))
			if it.Area.Valid {
				line += "  area=" + it.Area.String
			}
			if it.Milestone.Valid {
				line += "  milestone=" + it.Milestone.String
			}
			if it.Parent.Valid {
				line += "  parent=" + it.Parent.String
			}
			if len(it.DependsOn) > 0 {
				line += "  after=" + strings.Join(it.DependsOn, ",")
			}
			if it.TaskID.Valid {
				line += fmt.Sprintf("  -> task #%d", it.TaskID.Int64)
			}
			if it.Dropped {
				line += "  (DROPPED)"
			}
			fmt.Println(line)
			for _, c := range it.Criteria {
				fmt.Printf("           - %s\n", c)
			}
		}
		return nil
	},
}

var (
	planEditTitle     string
	planEditRisk      string
	planEditAutonomy  string
	planEditArea      string
	planEditSize      string
	planEditMilestone string
	planEditDrop      bool
	planEditRestore   bool
)

var planEditCmd = &cobra.Command{
	Use:   "edit <plan-id> <ref>",
	Short: "Change or drop one item of a draft plan before approving it",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		planID, err := parseID(args[0], "plan")
		if err != nil {
			return err
		}
		var e store.PlanItemEdit
		f := cmd.Flags()
		if f.Changed("title") {
			e.Title = &planEditTitle
		}
		if f.Changed("risk") {
			e.Risk = &planEditRisk
		}
		if f.Changed("autonomy") {
			e.Autonomy = &planEditAutonomy
		}
		if f.Changed("area") {
			e.Area = &planEditArea
		}
		if f.Changed("size") {
			e.Size = &planEditSize
		}
		if f.Changed("milestone") {
			e.Milestone = &planEditMilestone
		}
		if planEditDrop && planEditRestore {
			return errors.New("--drop and --restore are exclusive")
		}
		if planEditDrop || planEditRestore {
			drop := planEditDrop
			e.Drop = &drop
		}
		if err := withApprovalToken(fmt.Sprintf("editing plan #%d", planID), func(token string) error {
			return st.EditPlanItem(planID, args[1], e, token)
		}); err != nil {
			return err
		}
		fmt.Printf("plan #%d item %s updated\n", planID, args[1])
		return nil
	},
}

var planApproveCmd = &cobra.Command{
	Use:   "approve <plan-id>",
	Short: "Approve a draft plan: create its tasks, links, criteria and milestones (a person's decision)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		planID, err := parseID(args[0], "plan")
		if err != nil {
			return err
		}
		var created []store.CreatedTask
		err = withApprovalToken(fmt.Sprintf("approving plan #%d", planID), func(token string) error {
			var aerr error
			created, aerr = st.ApprovePlan(planID, token)
			return aerr
		})
		if errors.Is(err, store.ErrAgentCannotDecidePlan) {
			return err
		}
		if err != nil {
			return err
		}
		fmt.Printf("plan #%d approved: %d task(s) created\n", planID, len(created))
		for _, c := range created {
			fmt.Printf("  %-6s -> task #%d\n", c.Ref, c.TaskID)
		}
		fmt.Println("see what to do first with: acline next")
		return nil
	},
}

var planRejectNote string

var planRejectCmd = &cobra.Command{
	Use:   "reject <plan-id>",
	Short: "Reject a draft plan (the reason is kept as a note for `acline reflect`)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		planID, err := parseID(args[0], "plan")
		if err != nil {
			return err
		}
		if err := st.RejectPlan(planID, planRejectNote); err != nil {
			return err
		}
		fmt.Printf("plan #%d rejected\n", planID)
		return nil
	},
}

func init() {
	for _, c := range []*cobra.Command{planProposeCmd, planReviseCmd} {
		c.Flags().StringVar(&planFile, "file", "", "plan JSON file, or - for stdin")
	}
	planListCmd.Flags().StringVar(&planListSpec, "spec", "", "only this spec's plans")
	planListCmd.Flags().StringVar(&planListStatus, "status", "", "draft|approved|rejected|superseded")
	f := planEditCmd.Flags()
	f.StringVar(&planEditTitle, "title", "", "new title")
	f.StringVar(&planEditRisk, "risk", "", "low|medium|high|critical")
	f.StringVar(&planEditAutonomy, "autonomy", "", "hitl|hotl (a plan cannot grant auto)")
	f.StringVar(&planEditArea, "area", "", "ownership area")
	f.StringVar(&planEditSize, "size", "", "S|M|L (empty clears it)")
	f.StringVar(&planEditMilestone, "milestone", "", "milestone name (empty clears it)")
	f.BoolVar(&planEditDrop, "drop", false, "leave this item out of what approval creates")
	f.BoolVar(&planEditRestore, "restore", false, "put a dropped item back")
	planRejectCmd.Flags().StringVar(&planRejectNote, "note", "", "why the plan was rejected")

	planCmd.AddCommand(planProposeCmd, planReviseCmd, planListCmd, planShowCmd, planEditCmd, planApproveCmd, planRejectCmd)
	rootCmd.AddCommand(planCmd)
}
