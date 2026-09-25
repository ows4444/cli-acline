package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"acline/internal/redact"
	"acline/internal/store"
)

var decisionCmd = &cobra.Command{
	Use:   "decision",
	Short: "Manage architecture decisions (ADR-style)",
}

var (
	decisionScope     string
	decisionContext   string
	decisionText      string
	decisionRationale string
	decisionProject   string
	decisionJSON      bool
)

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

var decisionAddCmd = &cobra.Command{
	Use:   "add <title>",
	Short: "Record a new decision (status: proposed)",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		title := strings.Join(args, " ")
		secretFound := redact.Fields(&title, &decisionContext, &decisionText, &decisionRationale)
		projectID, err := resolveProjectFlag(decisionProject)
		if err != nil {
			return err
		}
		id, err := st.AddDecision(title, store.DecisionOpts{
			Scope: decisionScope, Context: decisionContext,
			Decision: decisionText, Rationale: decisionRationale, ProjectID: projectID,
		})
		if err != nil {
			return err
		}
		logEventGlobal("decision_recorded", fmt.Sprintf("decision #%d proposed: %s", id, title))
		if secretFound {
			logEventGlobal("secret_redacted", fmt.Sprintf("decision #%d: a pasted secret value was redacted before recording", id))
		}
		fmt.Printf("decision #%d recorded: %s\n", id, title)
		return nil
	},
}

var decisionListCmd = &cobra.Command{
	Use:   "list",
	Short: "List decisions",
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, err := resolveProjectFlagOptional(decisionProject)
		if err != nil {
			return err
		}
		decisions, err := st.ListDecisions(decisionStatusFilter, projectID)
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

var decisionStatusFilter string

var decisionShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show a decision",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid decision id: %w", err)
		}
		d, err := st.GetDecision(id)
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

var decisionAcceptCmd = &cobra.Command{
	Use:   "accept <id>",
	Short: "Mark a decision accepted — a person's decision",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := parseID(args[0], "decision")
		if err != nil {
			return err
		}
		err = withApprovalToken(fmt.Sprintf("accepting decision #%d", id), func(token string) error {
			return st.AcceptDecision(id, token)
		})
		if err != nil {
			return err
		}
		fmt.Printf("decision #%d accepted\n", id)
		return nil
	},
}

var decisionRejectCmd = &cobra.Command{
	Use:   "reject <id>",
	Short: "Mark a decision rejected (a person's decision when it is accepted)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		reject := func(id int64, _ string) error {
			return withApprovalToken(fmt.Sprintf("rejecting decision #%d", id), func(token string) error {
				return st.RejectDecision(id, token)
			})
		}
		return runStatusTransition(args, "decision", reject, "rejected", "", "rejected") // the store records the event
	},
}

var decisionSupersedeCmd = &cobra.Command{
	Use:   "supersede <old-id> <new-id>",
	Short: "Mark old-id superseded by new-id (a person's decision when old-id is accepted)",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		oldID, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid decision id: %w", err)
		}
		newID, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid decision id: %w", err)
		}
		err = withApprovalToken(fmt.Sprintf("superseding decision #%d", oldID), func(token string) error {
			return st.SupersedeDecision(oldID, newID, token)
		})
		if err != nil {
			return err
		}
		fmt.Printf("decision #%d superseded by #%d\n", oldID, newID)
		return nil
	},
}

func init() {
	decisionAddCmd.Flags().StringVar(&decisionScope, "scope", "", "e.g. system|backend|api|security|infrastructure")
	decisionAddCmd.Flags().StringVar(&decisionContext, "context", "", "why this decision is being made")
	decisionAddCmd.Flags().StringVar(&decisionText, "decision", "", "what was decided")
	decisionAddCmd.Flags().StringVar(&decisionRationale, "rationale", "", "why this option over alternatives")

	decisionListCmd.Flags().StringVarP(&decisionStatusFilter, "status", "s", "", "filter by status")
	decisionAddCmd.Flags().StringVar(&decisionProject, "project", "", "project name (default: resolved from cwd/ACLINE_PROJECT)")
	decisionListCmd.Flags().StringVar(&decisionProject, "project", "", "filter by project name")
	decisionListCmd.Flags().BoolVar(&decisionJSON, "json", false, "print results as a JSON array instead of text")

	decisionCmd.AddCommand(decisionAddCmd, decisionListCmd, decisionShowCmd, decisionAcceptCmd, decisionRejectCmd, decisionSupersedeCmd)
	rootCmd.AddCommand(decisionCmd)
}
