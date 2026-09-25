package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"acline/internal/store"
)

var (
	searchProject  string
	searchLimit    int
	searchJSON     bool
	searchSemantic bool
)

// searchHitJSON is the --json view of a store.SearchHit: plain types only
// (a bare *int64 instead of sql.NullInt64) so it serializes the way a
// consumer expects rather than as {"Int64":0,"Valid":false}.
type searchHitJSON struct {
	Kind      string  `json:"kind"`
	RefID     int64   `json:"ref_id"`
	ProjectID *int64  `json:"project_id"`
	Snippet   string  `json:"snippet"`
	Status    string  `json:"status"`
	Score     float64 `json:"score,omitempty"`
}

// isCurrentSearchStatus reports whether a hit's status means "still
// authoritative" for its kind, so the text output can stay quiet for the
// common case and only flag the ones worth a second look — a superseded
// decision, a stale memory entry, a draft spec, an already-promoted note.
func isCurrentSearchStatus(kind, status string) bool {
	switch kind {
	case "decision":
		return status == "accepted"
	case "memory":
		return status == "approved"
	case "spec":
		return status == "approved" || status == "implemented"
	case "note":
		return status == ""
	default:
		return true
	}
}

var searchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Keyword (or --semantic) search across decisions, memory, notes and specs",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query := strings.Join(args, " ")
		projectID, err := resolveProjectFlagOptional(searchProject)
		if err != nil {
			return err
		}

		var hits []store.SearchHit
		if searchSemantic {
			if !st.EmbeddingsEnabled() {
				return fmt.Errorf("--semantic requires VOYAGE_API_KEY to be set (semantic search is opt-in; see README)")
			}
			hits, err = st.SemanticSearchQuery(query, projectID, searchLimit)
		} else {
			hits, err = st.Search(query, projectID, searchLimit)
		}
		if err != nil {
			return err
		}

		if searchJSON {
			out := make([]searchHitJSON, len(hits))
			for i, h := range hits {
				out[i] = searchHitJSON{Kind: h.Kind, RefID: h.RefID, ProjectID: nullIntPtr(h.ProjectID), Snippet: h.Snippet, Status: h.Status, Score: h.Score}
			}
			return printJSON(out)
		}
		if len(hits) == 0 {
			fmt.Println("no results")
			return nil
		}
		for _, h := range hits {
			marker := ""
			if !isCurrentSearchStatus(h.Kind, h.Status) {
				marker = fmt.Sprintf(" {%s}", h.Status)
			}
			scorePart := ""
			if searchSemantic {
				scorePart = fmt.Sprintf(" (%.3f)", h.Score)
			}
			fmt.Printf("[%s #%d]%s%s %s\n", h.Kind, h.RefID, marker, scorePart, h.Snippet)
		}
		return nil
	},
}

var reindexEmbeddingsCmd = &cobra.Command{
	Use:   "reindex-embeddings",
	Short: "Backfill embeddings for indexed rows written before semantic search was enabled, or after a model change",
	RunE: func(cmd *cobra.Command, args []string) error {
		if !st.EmbeddingsEnabled() {
			return fmt.Errorf("VOYAGE_API_KEY is not set — semantic search is opt-in, nothing to reindex")
		}
		model := st.Embedder.Model()
		rows, err := st.RowsMissingEmbeddings(model)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			fmt.Println("nothing to reindex")
			return nil
		}
		var failed int
		for _, r := range rows {
			// Deliberately not embedding notes here either — see the
			// comment on AddNote for why notes are excluded from semantic
			// indexing at write time; a bulk backfill shouldn't route
			// around that.
			if r.Kind == "note" {
				continue
			}
			if err := st.ReindexEmbedding(model, r); err != nil {
				failed++
				fmt.Printf("failed: %s #%d: %v\n", r.Kind, r.RefID, err)
				continue
			}
			fmt.Printf("embedded: %s #%d\n", r.Kind, r.RefID)
		}
		if failed > 0 {
			return fmt.Errorf("%d/%d rows failed to embed", failed, len(rows))
		}
		return nil
	},
}

func init() {
	searchCmd.Flags().StringVar(&searchProject, "project", "", "restrict to a project name")
	searchCmd.Flags().IntVar(&searchLimit, "limit", 20, "max results")
	searchCmd.Flags().BoolVar(&searchJSON, "json", false, "print results as a JSON array instead of text")
	searchCmd.Flags().BoolVar(&searchSemantic, "semantic", false, "semantic (embedding) search instead of keyword — requires VOYAGE_API_KEY")
	searchCmd.AddCommand(reindexEmbeddingsCmd)
	rootCmd.AddCommand(searchCmd)
}
