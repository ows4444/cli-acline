package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"acline/internal/app"
	"acline/internal/store"
)

func newNoteCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "note",
		Short: "Fast, unstructured capture — promote to a decision/memory later with `acline reflect`",
	}
	cmd.AddCommand(newNoteAddCmd(c), newNoteListCmd(c))
	return cmd
}

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

func newNoteAddCmd(c *cli) *cobra.Command {
	var (
		noteSource  string
		noteProject string
		noteRole    string
	)
	cmd := &cobra.Command{
		Use:   "add <text...>",
		Short: "Record a note (source=manual unless --source conversation, used by hooks)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := app.AddNote(c.st, app.AddNoteRequest{
				Body: strings.Join(args, " "), Source: noteSource, RoleArg: noteRole, ProjectArg: noteProject, AllowCwdFallback: true,
			})
			if err != nil {
				return err
			}
			if res.Redacted {
				fmt.Println("note: a pasted secret value was redacted before recording")
			}
			fmt.Printf("note #%d recorded\n", res.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&noteSource, "source", "manual", "manual|conversation")
	cmd.Flags().StringVar(&noteProject, "project", "", "project name (default: resolved from cwd/ACLINE_PROJECT)")
	cmd.Flags().StringVar(&noteRole, "role", "", "role name (default: $ACLINE_ROLE or the active session's role)")
	return cmd
}

func newNoteListCmd(c *cli) *cobra.Command {
	var (
		noteProject    string
		noteUnpromoted bool
		noteJSON       bool
	)
	var allProjects bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List notes",
		RunE: func(cmd *cobra.Command, args []string) error {
			projectID, err := c.resolveListScope(noteProject, allProjects)
			if err != nil {
				return err
			}
			notes, err := c.st.ListNotes(store.NoteFilter{ProjectID: projectID, UnpromotedOnly: noteUnpromoted})
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
	cmd.Flags().BoolVar(&noteUnpromoted, "unpromoted", false, "only show notes not yet promoted")
	cmd.Flags().StringVar(&noteProject, "project", "", "project name (default: the current project, else every project)")
	cmd.Flags().BoolVar(&allProjects, "all-projects", false, allProjectsUsage)
	cmd.Flags().BoolVar(&noteJSON, "json", false, "print results as a JSON array instead of text")
	return cmd
}
