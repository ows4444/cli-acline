package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"acline/internal/store"
)

var reflectProject string

var reflectCmd = &cobra.Command{
	Use:   "reflect",
	Short: "Review unpromoted notes for the current project and promote the durable ones",
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, err := resolveProjectFlag(reflectProject)
		if err != nil {
			return err
		}
		notes, err := st.ListNotes(store.NoteFilter{ProjectID: projectID, UnpromotedOnly: true})
		if err != nil {
			return err
		}
		if len(notes) == 0 {
			fmt.Println("nothing to reflect on")
			return nil
		}
		fmt.Printf("%d unpromoted note(s):\n", len(notes))
		for _, n := range notes {
			fmt.Printf("  #%d [%s] %s\n", n.ID, n.Source, n.Body)
		}
		fmt.Println("\npromote with: acline reflect promote <note-id> decision \"<title>\" [--decision .. --rationale ..]")
		fmt.Println("          or: acline reflect promote <note-id> memory [--area .. --kind ..]")
		return nil
	},
}

var (
	reflectDecisionScope     string
	reflectDecisionContext   string
	reflectDecisionText      string
	reflectDecisionRationale string
	reflectMemoryArea        string
	reflectMemoryKind        string
)

var reflectPromoteCmd = &cobra.Command{
	Use:   "promote <note-id> <decision|memory> [title...]",
	Short: "Promote a note into a decision or memory entry",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		noteID, err := parseID(args[0], "note")
		if err != nil {
			return err
		}
		kind := args[1]
		title := strings.Join(args[2:], " ")
		id, err := st.PromoteNote(noteID, kind, store.PromoteOpts{
			Title: title, Scope: reflectDecisionScope, Context: reflectDecisionContext,
			Decision: reflectDecisionText, Rationale: reflectDecisionRationale,
			MemoryArea: reflectMemoryArea, MemoryKind: reflectMemoryKind,
		})
		if errors.Is(err, store.ErrNoteAlreadyPromoted) {
			if n, gerr := st.GetNote(noteID); gerr == nil && n.PromotedTo.Valid {
				return fmt.Errorf("note #%d was already promoted to %s #%d", n.ID, n.PromotedTo.String, n.PromotedID.Int64)
			}
		}
		if err != nil {
			return err
		}
		fmt.Printf("note #%d promoted to %s #%d\n", noteID, kind, id)
		return nil
	},
}

func init() {
	reflectCmd.Flags().StringVar(&reflectProject, "project", "", "project name (default: resolved from cwd/ACLINE_PROJECT)")
	reflectPromoteCmd.Flags().StringVar(&reflectDecisionScope, "scope", "", "e.g. system|backend|api|security|infrastructure")
	reflectPromoteCmd.Flags().StringVar(&reflectDecisionContext, "context", "", "why this decision is being made")
	reflectPromoteCmd.Flags().StringVar(&reflectDecisionText, "decision", "", "what was decided (default: the note body)")
	reflectPromoteCmd.Flags().StringVar(&reflectDecisionRationale, "rationale", "", "why this option over alternatives")
	reflectPromoteCmd.Flags().StringVar(&reflectMemoryArea, "area", "", "ownership area this memory applies to")
	reflectPromoteCmd.Flags().StringVar(&reflectMemoryKind, "kind", "lesson", "constraint|lesson|pitfall|operational|failure_pattern")

	reflectCmd.AddCommand(reflectPromoteCmd)
	rootCmd.AddCommand(reflectCmd)
}
