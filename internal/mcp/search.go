package mcp

import (
	"context"
	"fmt"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"acline/internal/store"
)

type searchArgs struct {
	Query    string `json:"query" jsonschema:"the search query"`
	Semantic bool   `json:"semantic,omitempty" jsonschema:"rank by embedding similarity instead of exact-token match (requires the server to have VOYAGE_API_KEY set)"`
	Project  string `json:"project,omitempty" jsonschema:"restrict to a project name (omit for no project scope)"`
	Limit    int    `json:"limit,omitempty" jsonschema:"max results (default 20)"`
}

type searchHitOut struct {
	Kind      string  `json:"kind"`
	RefID     int64   `json:"ref_id"`
	ProjectID *int64  `json:"project_id,omitempty"`
	Snippet   string  `json:"snippet"`
	Status    string  `json:"status,omitempty"`
	Score     float64 `json:"score,omitempty"`
}

type searchOut struct {
	Hits []searchHitOut `json:"hits"`
}

func toSearchHitOut(h store.SearchHit) searchHitOut {
	return searchHitOut{Kind: h.Kind, RefID: h.RefID, ProjectID: nullIntPtr(h.ProjectID), Snippet: h.Snippet, Status: h.Status, Score: h.Score}
}

func registerSearchTool(s *sdkmcp.Server, st *store.Store) {
	addTool(s, &sdkmcp.Tool{
		Name: "acline_search",
		Description: "Search decisions, memory, notes and specs by keyword (exact-token FTS5) or, with " +
			"semantic=true, by embedding similarity — finds a row whose wording differs from the query, " +
			"e.g. \"why postgres\" matching a decision whose rationale only says \"durability concerns\". " +
			"Semantic search requires the server to have VOYAGE_API_KEY configured; it errors otherwise.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args searchArgs) (*sdkmcp.CallToolResult, searchOut, error) {
		if args.Query == "" {
			return nil, searchOut{}, fmt.Errorf("query is required")
		}
		projectID, err := resolveProject(st, args.Project)
		if err != nil {
			return nil, searchOut{}, err
		}
		limit := args.Limit
		if limit <= 0 {
			limit = 20
		}

		var hits []store.SearchHit
		if args.Semantic {
			if !st.EmbeddingsEnabled() {
				return nil, searchOut{}, fmt.Errorf("semantic search requires VOYAGE_API_KEY to be set on the server")
			}
			hits, err = st.SemanticSearchQuery(args.Query, projectID, limit)
		} else {
			hits, err = st.Search(args.Query, projectID, limit)
		}
		if err != nil {
			return nil, searchOut{}, err
		}

		out := searchOut{Hits: make([]searchHitOut, len(hits))}
		for i, h := range hits {
			out.Hits[i] = toSearchHitOut(h)
		}
		return textResult(fmt.Sprintf("%d hit(s) for %q", len(hits), args.Query)), out, nil
	})
}
