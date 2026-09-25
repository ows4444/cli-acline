package store

import "strings"

const (
	// duplicateThreshold is the cosine similarity at which a new memory is
	// reported as saying the same thing as an existing one.
	duplicateThreshold = 0.9
	// recallThreshold is the (lower) similarity at which a lesson counts as
	// relevant to a task it shares no area with.
	recallThreshold = 0.5
	// recallSemanticLimit caps how many semantic matches are added to a recall.
	recallSemanticLimit = 5
)

func normalizeBody(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// SimilarMemory returns an existing, non-rejected memory entry that says the
// same thing as body, or nil. It matches on identical text (ignoring case and
// whitespace) always, and additionally on embedding similarity when an
// Embedder is configured. It is advisory: callers report the match, they do
// not refuse the write. An embedding failure just means no semantic match.
func (s *Store) SimilarMemory(body string, projectID *int64) (*MemoryEntry, error) {
	want := normalizeBody(scrubText(body))
	rows, err := s.DB.Query(`SELECT `+memoryColumns+` FROM memory
		WHERE status != 'rejected' AND (project_id IS NULL OR project_id = ?) ORDER BY id`, nullInt64Arg(nullInt(projectID)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries, err := scanMemory(rows)
	if err != nil {
		return nil, err
	}
	for i := range entries {
		if normalizeBody(entries[i].Body) == want {
			return &entries[i], nil
		}
	}
	if s.Embedder == nil {
		return nil, nil
	}
	hits, err := s.SemanticSearchQuery(body, projectID, 5)
	if err != nil {
		return nil, nil
	}
	for _, h := range hits {
		if h.Kind != "memory" || h.Score < duplicateThreshold {
			continue
		}
		if m := s.liveMemoryByID(h.RefID, ""); m != nil && m.Status != "rejected" {
			return m, nil
		}
	}
	return nil, nil
}

// semanticRecall returns approved, non-stale pitfall/failure_pattern entries
// whose meaning is close to query, best first, skipping ids in exclude.
func (s *Store) semanticRecall(query string, projectID *int64, exclude map[int64]bool) []MemoryEntry {
	if s.Embedder == nil {
		return nil
	}
	hits, err := s.SemanticSearchQuery(query, projectID, 20)
	if err != nil {
		return nil
	}
	var out []MemoryEntry
	for _, h := range hits {
		if len(out) >= recallSemanticLimit {
			break
		}
		if h.Kind != "memory" || h.Score < recallThreshold || exclude[h.RefID] {
			continue
		}
		if m := s.liveMemoryByID(h.RefID, "approved"); m != nil && (m.Kind == "pitfall" || m.Kind == "failure_pattern") {
			out = append(out, *m)
		}
	}
	return out
}

// liveMemoryByID loads a non-stale memory entry, optionally requiring a status.
func (s *Store) liveMemoryByID(id int64, status string) *MemoryEntry {
	q := `SELECT ` + memoryColumns + ` FROM memory WHERE id = ? AND stale = 0`
	args := []any{id}
	if status != "" {
		q += ` AND status = ?`
		args = append(args, status)
	}
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	entries, err := scanMemory(rows)
	if err != nil || len(entries) == 0 {
		return nil
	}
	return &entries[0]
}
