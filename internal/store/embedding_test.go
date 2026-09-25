package store

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// fakeEmbedder is a deterministic, network-free Embedder for tests: it
// turns each text into a tiny vector by counting a fixed set of keywords,
// so texts sharing keywords end up with a higher cosine similarity than
// unrelated ones — enough to exercise ranking without a real model.
type fakeEmbedder struct {
	model     string
	calls     int
	failNext  bool
	lastInput string
	lastType  string
}

var fakeVocab = []string{"postgres", "durability", "cache", "redis", "latency", "auth"}

func (f *fakeEmbedder) Embed(texts []string, inputType string) ([][]float32, error) {
	f.calls++
	f.lastType = inputType
	if len(texts) > 0 {
		f.lastInput = texts[0]
	}
	if f.failNext {
		f.failNext = false
		return nil, fmt.Errorf("simulated embedding failure")
	}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		lower := strings.ToLower(t)
		vec := make([]float32, len(fakeVocab))
		for j, word := range fakeVocab {
			if strings.Contains(lower, word) {
				vec[j] = 1
			}
		}
		out[i] = vec
	}
	return out, nil
}

func (f *fakeEmbedder) Model() string { return f.model }

func TestIndexForSemanticSearchNoopWithoutEmbedder(t *testing.T) {
	s := humanStore(t)
	// s.Embedder is nil by default.
	id, err := s.AddMemory("cli", "lesson", "postgres durability concerns drove the choice")
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM embeddings`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected no embeddings with no Embedder configured, got %d (memory id %d)", count, id)
	}
}

func TestAddMemoryIndexesEmbeddingWhenConfigured(t *testing.T) {
	s := humanStore(t)
	fe := &fakeEmbedder{model: "fake-1"}
	s.Embedder = fe

	id, err := s.AddMemory("cli", "lesson", "we picked postgres for durability concerns")
	if err != nil {
		t.Fatal(err)
	}
	if fe.calls != 1 {
		t.Fatalf("expected exactly one Embed call, got %d", fe.calls)
	}
	if fe.lastType != "document" {
		t.Fatalf("expected write-time embedding to use input_type=document, got %q", fe.lastType)
	}
	var count int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM embeddings WHERE kind = 'memory' AND ref_id = ?`, id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected exactly one embedding row for memory #%d, got %d", id, count)
	}
}

func TestAddNoteNeverIndexesEmbedding(t *testing.T) {
	s := humanStore(t)
	fe := &fakeEmbedder{model: "fake-1"}
	s.Embedder = fe

	if _, err := s.AddNote(nil, "postgres durability concerns, noted in passing", "manual"); err != nil {
		t.Fatal(err)
	}
	if fe.calls != 0 {
		t.Fatalf("expected notes to never trigger an embedding call (bulk MEMORY_LOG capture), got %d calls", fe.calls)
	}
}

func TestIndexForSemanticSearchSwallowsEmbedderFailure(t *testing.T) {
	s := humanStore(t)
	fe := &fakeEmbedder{model: "fake-1", failNext: true}
	s.Embedder = fe

	id, err := s.AddDecision("use postgres", DecisionOpts{Rationale: "durability concerns"})
	if err != nil {
		t.Fatalf("AddDecision must succeed even when the embedder fails, got: %v", err)
	}
	var count int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM embeddings WHERE kind = 'decision' AND ref_id = ?`, id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected no embedding row after a simulated failure, got %d", count)
	}
	var eventCount int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE type = 'embedding_failed'`).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 1 {
		t.Fatalf("expected the embedding failure to be logged as an audit event, got %d", eventCount)
	}
}

func TestEncodeDecodeVectorRoundTrips(t *testing.T) {
	v := []float32{0.5, -1.25, 3.0, 0, -0.001}
	decoded := decodeVector(encodeVector(v))
	if len(decoded) != len(v) {
		t.Fatalf("length mismatch: got %d, want %d", len(decoded), len(v))
	}
	for i := range v {
		if decoded[i] != v[i] {
			t.Errorf("index %d: got %v, want %v", i, decoded[i], v[i])
		}
	}
}

func TestCosineSimilarity(t *testing.T) {
	cases := []struct {
		name string
		a, b []float32
		want float64
	}{
		{"identical", []float32{1, 0, 0}, []float32{1, 0, 0}, 1},
		{"orthogonal", []float32{1, 0, 0}, []float32{0, 1, 0}, 0},
		{"opposite", []float32{1, 0, 0}, []float32{-1, 0, 0}, -1},
		{"length mismatch", []float32{1, 0}, []float32{1, 0, 0}, -1},
		{"zero vector", []float32{0, 0}, []float32{1, 1}, -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := cosineSimilarity(c.a, c.b)
			if got != c.want {
				t.Errorf("cosineSimilarity(%v, %v) = %v, want %v", c.a, c.b, got, c.want)
			}
		})
	}
}

func TestSemanticSearchRanksByRelevance(t *testing.T) {
	s := humanStore(t)
	fe := &fakeEmbedder{model: "fake-1"}
	s.Embedder = fe

	pgID, err := s.AddMemory("cli", "lesson", "we picked postgres for durability concerns over other stores")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddMemory("cli", "lesson", "redis cache latency was the deciding factor here"); err != nil {
		t.Fatal(err)
	}

	// A query about "durability" should rank the postgres memory first even
	// though the query text itself never says "postgres" — this is the
	// whole point of semantic vs. exact-token search.
	qv, err := fe.Embed([]string{"why did we choose postgres for durability"}, "query")
	if err != nil {
		t.Fatal(err)
	}
	hits, err := s.SemanticSearch(fe.model, qv[0], nil, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits, got %d", len(hits))
	}
	if hits[0].Kind != "memory" || hits[0].RefID != pgID {
		t.Fatalf("expected the postgres memory (#%d) to rank first, got %+v", pgID, hits[0])
	}
	if hits[0].Score <= hits[1].Score {
		t.Fatalf("expected a higher score for the more relevant hit: %v vs %v", hits[0].Score, hits[1].Score)
	}
	if hits[0].Snippet == "" {
		t.Error("expected a non-empty snippet borrowed from search_index")
	}
}

func TestSemanticSearchScopesToModel(t *testing.T) {
	s := humanStore(t)
	fe := &fakeEmbedder{model: "model-a"}
	s.Embedder = fe
	if _, err := s.AddMemory("cli", "lesson", "postgres durability"); err != nil {
		t.Fatal(err)
	}

	// A search under a different model name should find nothing, even
	// though the only stored embedding is a perfect semantic match --
	// mixing vectors from different models in one cosine scan would be
	// meaningless (see the schema comment on the embeddings table).
	hits, err := s.SemanticSearch("model-b", []float32{1, 0, 0, 0, 0, 0}, nil, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("expected 0 hits under a different model, got %d", len(hits))
	}
}

func TestRowsMissingEmbeddingsBackfillFlow(t *testing.T) {
	s := humanStore(t)
	// No Embedder configured yet: AddMemory indexes for FTS5 but not
	// semantic search, simulating rows written before semantic search was
	// turned on.
	id, err := s.AddMemory("cli", "lesson", "postgres durability concerns")
	if err != nil {
		t.Fatal(err)
	}

	fe := &fakeEmbedder{model: "fake-1"}
	s.Embedder = fe

	missing, err := s.RowsMissingEmbeddings(fe.model)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 1 || missing[0].RefID != id || missing[0].Kind != "memory" {
		t.Fatalf("expected exactly the memory row to be missing an embedding, got %+v", missing)
	}

	if err := s.ReindexEmbedding(fe.model, missing[0]); err != nil {
		t.Fatal(err)
	}

	missingAfter, err := s.RowsMissingEmbeddings(fe.model)
	if err != nil {
		t.Fatal(err)
	}
	if len(missingAfter) != 0 {
		t.Fatalf("expected no rows missing an embedding after reindex, got %d", len(missingAfter))
	}
}

func TestEmbeddingsEnabled(t *testing.T) {
	s := humanStore(t)
	if s.EmbeddingsEnabled() {
		t.Fatal("expected EmbeddingsEnabled() to be false with no Embedder configured")
	}
	s.Embedder = &fakeEmbedder{model: "fake-1"}
	if !s.EmbeddingsEnabled() {
		t.Fatal("expected EmbeddingsEnabled() to be true once an Embedder is configured")
	}
}

// blockingEmbedder never answers until released, like a provider that is down
// and being retried.
type blockingEmbedder struct{ release chan struct{} }

func (b *blockingEmbedder) Embed(texts []string, inputType string) ([][]float32, error) {
	<-b.release
	return [][]float32{make([]float32, len(fakeVocab))}, nil
}
func (b *blockingEmbedder) Model() string { return "blocking" }

func TestSlowEmbeddingProviderDoesNotStallWrites(t *testing.T) {
	s := humanStore(t)
	be := &blockingEmbedder{release: make(chan struct{})}
	t.Cleanup(func() { close(be.release) })
	s.Embedder = be
	s.EmbedBudget = 50 * time.Millisecond

	start := time.Now()
	id, err := s.AddMemory("cli", "lesson", "written while the provider hangs")
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("write took %v; it must not wait out a hung embedding provider", d)
	}
	if got, _ := s.ListMemory(MemoryFilter{}); len(got) != 1 || got[0].ID != id {
		t.Fatal("the memory row itself must still be written")
	}
	if n := count(t, s, `SELECT COUNT(*) FROM embeddings`); n != 0 {
		t.Errorf("embeddings = %d, want 0 (provider never answered)", n)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM events WHERE type='embedding_failed' AND message LIKE '%reindex-embeddings%'`); n != 1 {
		t.Errorf("embedding_failed events mentioning the backfill = %d, want 1", n)
	}
}
