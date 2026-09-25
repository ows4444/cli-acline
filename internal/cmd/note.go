package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"acline/internal/store"
)

var noteCmd = &cobra.Command{
	Use:   "note",
	Short: "Fast, unstructured capture — promote to a decision/memory later with `acline reflect`",
}

var (
	noteSource  string
	noteProject string
	noteRole    string
)

// noteJSONView is the --json view of a store.Note: plain types only, so
// nullable columns serialize as a value or JSON null instead of
// database/sql's {"String":"x","Valid":true} shape.
type noteJSONView struct {
	ID         int64   `json:"id"`
	ProjectID  *int64  `json:"project_id"`
	Body       string  `json:"body"`
	Source     string  `json:"source"`
	CreatedAt  string  `json:"created_at"`
	PromotedTo *string `json:"promoted_to"`
	PromotedID *int64  `json:"promoted_id"`
}

func newNoteJSONView(n store.Note) noteJSONView {
	return noteJSONView{
		ID: n.ID, ProjectID: nullIntPtr(n.ProjectID), Body: n.Body, Source: n.Source, CreatedAt: n.CreatedAt,
		PromotedTo: nullStrPtr(n.PromotedTo), PromotedID: nullIntPtr(n.PromotedID),
	}
}

var noteAddCmd = &cobra.Command{
	Use:   "add <text...>",
	Short: "Record a note (source=manual unless --source conversation, used by hooks)",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		body := strings.Join(args, " ")
		projectID, err := resolveProjectFlag(noteProject)
		if err != nil {
			return err
		}
		roleID, err := resolveRoleFlag(noteRole, noteProject)
		if err != nil {
			return err
		}
		id, err := st.AddNoteWithRole(projectID, roleID, body, noteSource)
		if err != nil {
			return err
		}
		fmt.Printf("note #%d recorded\n", id)
		return nil
	},
}

var (
	noteUnpromoted bool
	noteJSON       bool
)

var noteListCmd = &cobra.Command{
	Use:   "list",
	Short: "List notes",
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, err := resolveProjectFlagOptional(noteProject)
		if err != nil {
			return err
		}
		notes, err := st.ListNotes(store.NoteFilter{ProjectID: projectID, UnpromotedOnly: noteUnpromoted})
		if err != nil {
			return err
		}
		if noteJSON {
			out := make([]noteJSONView, len(notes))
			for i, n := range notes {
				out[i] = newNoteJSONView(n)
			}
			return printJSON(out)
		}
		if len(notes) == 0 {
			fmt.Println("no notes")
			return nil
		}
		for _, n := range notes {
			status := ""
			if n.PromotedTo.Valid {
				status = fmt.Sprintf(" [promoted -> %s #%d]", n.PromotedTo.String, n.PromotedID.Int64)
			}
			fmt.Printf("#%d [%s]%s %s\n", n.ID, n.Source, status, n.Body)
		}
		return nil
	},
}

func init() {
	noteAddCmd.Flags().StringVar(&noteSource, "source", "manual", "manual|conversation")
	noteAddCmd.Flags().StringVar(&noteProject, "project", "", "project name (default: resolved from cwd/ACLINE_PROJECT)")
	noteAddCmd.Flags().StringVar(&noteRole, "role", "", "role name (default: $ACLINE_ROLE or the active session's role)")

	noteListCmd.Flags().BoolVar(&noteUnpromoted, "unpromoted", false, "only show notes not yet promoted")
	noteListCmd.Flags().StringVar(&noteProject, "project", "", "filter by project name")
	noteListCmd.Flags().BoolVar(&noteJSON, "json", false, "print results as a JSON array instead of text")

	noteCmd.AddCommand(noteAddCmd, noteListCmd)
	rootCmd.AddCommand(noteCmd)
}
