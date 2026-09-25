package store

import (
	"acline/internal/clip"
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"time"
)

// Embedder is implemented by internal/embed.Client. Store depends on this
// interface, not a concrete HTTP client, so the store package never imports
// net/http or knows anything Voyage-specific — the cmd layer decides
// whether semantic search is enabled (VOYAGE_API_KEY set) and wires an
// Embedder in via Store.Embedder, the same way it sets Actor.
// defaultEmbedBudget caps how long a write waits for the embedding provider.
const defaultEmbedBudget = 10 * time.Second

type Embedder interface {
	Embed(texts []string, inputType string) ([][]float32, error)
	Model() string
}

// indexForSemanticSearch embeds body and stores the vector alongside the
// row it describes, when an Embedder is configured. Unlike indexForSearch
// (FTS5, which every Add* call already treats as required), a failure here
// never fails the caller — semantic search is an opt-in enrichment on top
// of always-available keyword search, not a requirement for a write to
// succeed, so acline keeps working fully offline with no embedding provider
// configured. Failures are recorded as an audit event instead of being
// silently dropped or surfaced as a failed write.
func (s *Store) indexForSemanticSearch(kind string, refID int64, projectID sql.NullInt64, body string) {
	if s.Embedder == nil {
		return
	}
	// The provider is a network call with its own retries (up to ~80 s in the
	// worst case) made from inside a write; an MCP client gives up after 60 s.
	// Bound how long a write may wait for it. On timeout the row is simply left
	// without a vector -- recorded as an embedding_failed event, and backfilled
	// by `acline reindex-embeddings` -- instead of stalling the caller. The
	// abandoned call touches nothing but its own buffered channel, so letting it
	// finish in the background is safe.
	budget := s.EmbedBudget
	if budget <= 0 {
		budget = defaultEmbedBudget
	}
	type result struct {
		vectors [][]float32
		err     error
	}
	done := make(chan result, 1)
	go func() {
		v, err := s.Embedder.Embed([]string{body}, "document")
		done <- result{v, err}
	}()
	var r result
	select {
	case r = <-done:
	case <-time.After(budget):
		s.logEmbeddingFailure(kind, refID, fmt.Errorf("embedding provider did not answer within %s (run `acline reindex-embeddings` to backfill)", budget))
		return
	}
	if r.err != nil {
		s.logEmbeddingFailure(kind, refID, r.err)
		return
	}
	if len(r.vectors) == 0 {
		return
	}
	if err := s.upsertEmbedding(kind, refID, projectID, s.Embedder.Model(), r.vectors[0]); err != nil {
		s.logEmbeddingFailure(kind, refID, err)
	}
}

func (s *Store) logEmbeddingFailure(kind string, refID int64, err error) {
	msg := fmt.Sprintf("%s #%d", kind, refID)
	if err != nil {
		msg += ": " + err.Error()
	}
	s.LogEventGlobal("embedding_failed", msg)
}

func (s *Store) upsertEmbedding(kind string, refID int64, projectID sql.NullInt64, model string, vector []float32) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.Exec(
		`INSERT INTO embeddings (kind, ref_id, project_id, model, dims, vector, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(kind, ref_id) DO UPDATE SET
			project_id = excluded.project_id, model = excluded.model,
			dims = excluded.dims, vector = excluded.vector, created_at = excluded.created_at`,
		kind, refID, projectID, model, len(vector), encodeVector(vector), now,
	)
	return err
}

func encodeVector(v []float32) []byte {
	buf := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

func decodeVector(b []byte) []float32 {
	n := len(b) / 4
	out := make([]float32, n)
	for i := 0; i < n; i++ {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4 : i*4+4]))
	}
	return out
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return -1
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return -1
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// EmbeddingsEnabled reports whether an Embedder is configured, so the cmd
// layer can decide whether to attempt a semantic search without duplicating
// the nil check everywhere.
func (s *Store) EmbeddingsEnabled() bool { return s.Embedder != nil }

// SemanticSearch ranks rows in the embeddings table (restricted to model,
// and to projectID when given) by cosine similarity to queryVector,
// returning the top `limit` as SearchHits with Score populated. It's a
// linear scan over every stored vector for model — fine at the row counts a
// local per-project store holds; an ANN index would be premature here, and
// modernc.org/sqlite has no vector extension to lean on regardless.
func (s *Store) SemanticSearch(model string, queryVector []float32, projectID *int64, limit int) ([]SearchHit, error) {
	if limit <= 0 {
		limit = 20
	}
	q := `SELECT kind, ref_id, project_id, vector FROM embeddings WHERE model = ?`
	args := []any{model}
	if projectID != nil {
		q += ` AND project_id = ?`
		args = append(args, *projectID)
	}
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	type scored struct {
		hit   SearchHit
		score float64
	}
	var all []scored
	for rows.Next() {
		var h SearchHit
		var vecBytes []byte
		if err := rows.Scan(&h.Kind, &h.RefID, &h.ProjectID, &vecBytes); err != nil {
			rows.Close()
			return nil, err
		}
		all = append(all, scored{hit: h, score: cosineSimilarity(queryVector, decodeVector(vecBytes))})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	sort.Slice(all, func(i, j int) bool { return all[i].score > all[j].score })
	if len(all) > limit {
		all = all[:limit]
	}

	out := make([]SearchHit, len(all))
	for i, sc := range all {
		out[i] = sc.hit
		out[i].Score = sc.score
	}
	if err := s.attachSearchSnippets(out); err != nil {
		return nil, err
	}
	if err := s.attachSearchStatuses(out); err != nil {
		return nil, err
	}
	return out, nil
}

// SemanticSearchQuery embeds query (input_type="query") and ranks stored
// embeddings against it — the Embed-then-SemanticSearch composition every
// semantic-search caller needs (the CLI's `search --semantic` and the MCP
// server's search tool), kept in one place instead of duplicated in both.
func (s *Store) SemanticSearchQuery(query string, projectID *int64, limit int) ([]SearchHit, error) {
	if s.Embedder == nil {
		return nil, fmt.Errorf("no embedder configured")
	}
	vectors, err := s.Embedder.Embed([]string{query}, "query")
	if err != nil {
		return nil, fmt.Errorf("embedding query: %w", err)
	}
	if len(vectors) == 0 {
		return nil, fmt.Errorf("embedding query: no vector returned")
	}
	return s.SemanticSearch(s.Embedder.Model(), vectors[0], projectID, limit)
}

// attachSearchSnippets fills in each hit's Snippet from search_index's body
// column, truncated. Semantic search has no FTS5 match to generate a
// snippet() from (SearchHit's other path, Search in search.go, uses
// FTS5's own snippet() function instead), so this borrows the same source
// text keyword search already indexes.
func (s *Store) attachSearchSnippets(hits []SearchHit) error {
	for i := range hits {
		var body string
		err := s.DB.QueryRow(`SELECT body FROM search_index WHERE kind = ? AND ref_id = ?`, hits[i].Kind, hits[i].RefID).Scan(&body)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return err
		}
		hits[i].Snippet = truncateSnippet(body, 220)
	}
	return nil
}

func truncateSnippet(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return clip.Bytes(s, n) + "..."
}

// IndexedRow is one row of search_index, surfaced for embedding backfill.
type IndexedRow struct {
	Kind      string
	RefID     int64
	ProjectID sql.NullInt64
	Body      string
}

// ReindexEmbedding embeds row.Body and stores it for (model, row.Kind,
// row.RefID) — used by `acline search reindex-embeddings` to backfill.
// Unlike indexForSemanticSearch (best-effort, failures swallowed into an
// audit event), this returns its error so the reindex command can report
// per-row failures directly to whoever ran it.
func (s *Store) ReindexEmbedding(model string, row IndexedRow) error {
	if s.Embedder == nil {
		return fmt.Errorf("no embedder configured")
	}
	vectors, err := s.Embedder.Embed([]string{row.Body}, "document")
	if err != nil {
		return err
	}
	if len(vectors) == 0 {
		return fmt.Errorf("no vector returned")
	}
	return s.upsertEmbedding(row.Kind, row.RefID, row.ProjectID, model, vectors[0])
}

// RowsMissingEmbeddings lists indexed rows (from search_index) with no
// stored embedding for model — either written before an Embedder was
// configured, or left behind by a model change. Used by `acline search
// reindex-embeddings` to backfill.
func (s *Store) RowsMissingEmbeddings(model string) ([]IndexedRow, error) {
	rows, err := s.DB.Query(
		`SELECT si.kind, si.ref_id, si.project_id, si.body FROM search_index si
		 WHERE NOT EXISTS (
			SELECT 1 FROM embeddings e WHERE e.kind = si.kind AND e.ref_id = si.ref_id AND e.model = ?
		 )`,
		model,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IndexedRow
	for rows.Next() {
		var r IndexedRow
		if err := rows.Scan(&r.Kind, &r.RefID, &r.ProjectID, &r.Body); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
