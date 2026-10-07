package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"acline/internal/app"
	"acline/internal/store"
)

func newDecisionCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "decision",
		Short: "Manage architecture decisions (ADR-style)",
	}
	cmd.AddCommand(newDecisionAddCmd(c), newDecisionListCmd(c), newDecisionShowCmd(c), newDecisionAcceptCmd(c), newDecisionRejectCmd(c), newDecisionSupersedeCmd(c), newDecisionDeprecateCmd(c))
	return cmd
}

// decisionJSONView is the --json view of a store.Decision: plain types
// only, so nullable columns serialize as a value or JSON null instead of
// database/sql's {"String":"x","Valid":true} shape.
type decisionJSONView struct {
	ID           int64   `json:"id"`
	Title        string  `json:"title"`
	Status       string  `json:"status"`
	Scope        *string `json:"scope"`
	SupersededBy *int64  `json:"superseded_by"`
	Context      *string `json:"context"`
	Decision     *string `json:"decision"`
	Rationale    *string `json:"rationale"`
	ProjectID    *int64  `json:"project_id"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
}

func newDecisionJSONView(d store.Decision) decisionJSONView {
	return decisionJSONView{
		ID: d.ID, Title: d.Title, Status: d.Status,
		Scope: nullStrPtr(d.Scope), SupersededBy: nullIntPtr(d.SupersededBy),
		Context: nullStrPtr(d.Context), Decision: nullStrPtr(d.DecisionText), Rationale: nullStrPtr(d.Rationale),
		ProjectID: nullIntPtr(d.ProjectID), CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
	}
}

func newDecisionAddCmd(c *cli) *cobra.Command {
	var (
		decisionScope     string
		decisionContext   string
		decisionText      string
		decisionRationale string
		decisionProject   string
	)
	cmd := &cobra.Command{
		Use:   "add <title>",
		Short: "Record a new decision (status: proposed)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			title := strings.Join(args, " ")
			res, err := app.AddDecision(c.st, app.AddDecisionRequest{
				Title: title, Scope: decisionScope, Context: decisionContext, Decision: decisionText,
				Rationale: decisionRationale, ProjectArg: decisionProject, AllowCwdFallback: true,
			})
			if err != nil {
				return err
			}
			if res.Redacted {
				fmt.Println("note: a pasted secret value was redacted before recording")
			}
			fmt.Printf("decision #%d recorded: %s\n", res.ID, title)
			return nil
		},
	}
	cmd.Flags().StringVar(&decisionScope, "scope", "", "e.g. system|backend|api|security|infrastructure")
	cmd.Flags().StringVar(&decisionContext, "context", "", "why this decision is being made")
	cmd.Flags().StringVar(&decisionText, "decision", "", "what was decided")
	cmd.Flags().StringVar(&decisionRationale, "rationale", "", "why this option over alternatives")
	cmd.Flags().StringVar(&decisionProject, "project", "", "project name (default: resolved from cwd/ACLINE_PROJECT)")
	return cmd
}

func newDecisionListCmd(c *cli) *cobra.Command {
	var (
		decisionProject string
		decisionJSON    bool
	)
	var (
		decisionStatusFilter string
	)
	var allProjects bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List decisions",
		RunE: func(cmd *cobra.Command, args []string) error {
			projectID, err := c.resolveListScope(decisionProject, allProjects)
			if err != nil {
				return err
			}
			decisions, err := c.st.ListDecisions(decisionStatusFilter, projectID)
			if err != nil {
				return err
			}
			if decisionJSON {
				out := make([]decisionJSONView, len(decisions))
				for i, d := range decisions {
					out[i] = newDecisionJSONView(d)
				}
				return printJSON(out)
			}
			if len(decisions) == 0 {
				fmt.Println("no decisions")
				return nil
			}
			fmt.Printf("%-4s %-11s %-12s %s\n", "ID", "STATUS", "SCOPE", "TITLE")
			for _, d := range decisions {
				fmt.Printf("%-4d %-11s %-12s %s\n", d.ID, d.Status, d.Scope.String, d.Title)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&decisionStatusFilter, "status", "s", "", "filter by status")
	cmd.Flags().StringVar(&decisionProject, "project", "", "project name (default: the current project, else every project)")
	cmd.Flags().BoolVar(&allProjects, "all-projects", false, allProjectsUsage)
	cmd.Flags().BoolVar(&decisionJSON, "json", false, "print results as a JSON array instead of text")
	return cmd
}

func newDecisionShowCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show a decision",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], "decision")
			if err != nil {
				return err
			}
			d, err := c.st.GetDecision(id)
			if err != nil {
				return err
			}
			fmt.Printf("#%d %s\n", d.ID, d.Title)
			fmt.Printf("status: %s\n", d.Status)
			if d.Scope.Valid {
				fmt.Printf("scope:  %s\n", d.Scope.String)
			}
			if d.SupersededBy.Valid {
				fmt.Printf("superseded by: #%d\n", d.SupersededBy.Int64)
			}
			if d.Context.Valid {
				fmt.Printf("context:   %s\n", d.Context.String)
			}
			if d.DecisionText.Valid {
				fmt.Printf("decision:  %s\n", d.DecisionText.String)
			}
			if d.Rationale.Valid {
				fmt.Printf("rationale: %s\n", d.Rationale.String)
			}
			fmt.Printf("created: %s\n", d.CreatedAt)
			return nil
		},
	}
	return cmd
}

func newDecisionAcceptCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "accept <id>",
		Short: "Mark a decision accepted — a person's decision",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], "decision")
			if err != nil {
				return err
			}
			err = c.withApprovalToken(fmt.Sprintf("accepting decision #%d", id), func(token string) error {
				return c.st.AcceptDecision(id, token)
			})
			if err != nil {
				return err
			}
			fmt.Printf("decision #%d accepted\n", id)
			return nil
		},
	}
	return cmd
}

func newDecisionRejectCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reject <id>",
		Short: "Mark a decision rejected (a person's decision when it is accepted)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			reject := func(id int64, _ string) error {
				return c.withApprovalToken(fmt.Sprintf("rejecting decision #%d", id), func(token string) error {
					return c.st.RejectDecision(id, token)
				})
			}
			return runStatusTransition(args, "decision", reject, "rejected", "rejected") // the store records the event
		},
	}
	return cmd
}

func newDecisionSupersedeCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "supersede <old-id> <new-id>",
		Short: "Mark old-id superseded by new-id (a person's decision when old-id is accepted)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			oldID, err := parseID(args[0], "decision")
			if err != nil {
				return err
			}
			newID, err := parseID(args[1], "decision")
			if err != nil {
				return err
			}
			err = c.withApprovalToken(fmt.Sprintf("superseding decision #%d", oldID), func(token string) error {
				return c.st.SupersedeDecision(oldID, newID, token)
			})
			if err != nil {
				return err
			}
			fmt.Printf("decision #%d superseded by #%d\n", oldID, newID)
			return nil
		},
	}
	return cmd
}

func newDecisionDeprecateCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deprecate <id>",
		Short: "Mark an accepted decision deprecated: it no longer applies and nothing replaces it — a person's decision",
		Long: "Mark an accepted decision deprecated: it no longer applies and nothing replaces it.\n" +
			"When another decision replaces it, use `acline decision supersede` instead. Only an accepted\n" +
			"decision can be deprecated, and deprecated is final.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deprecate := func(id int64, _ string) error {
				return c.withApprovalToken(fmt.Sprintf("deprecating decision #%d", id), func(token string) error {
					return c.st.DeprecateDecision(id, token)
				})
			}
			return runStatusTransition(args, "decision", deprecate, "deprecated", "deprecated") // the store records the event
		},
	}
	return cmd
}
