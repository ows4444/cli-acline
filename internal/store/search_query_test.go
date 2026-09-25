package store

import "testing"

func TestSearchAcceptsFreeTextThatIsNotValidFTS5(t *testing.T) {
	s := newTestStore(t, Actor{Type: "human", ID: "u"})
	if _, err := s.AddNote(nil, "we chose foo-bar because what's next matters", "manual"); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`foo-bar`, `what's`, `say "hi`, `foo-bar what's`} {
		hits, err := s.Search(q, nil, 10)
		if err != nil {
			t.Errorf("Search(%q) errored: %v", q, err)
			continue
		}
		if q != `say "hi` && len(hits) != 1 {
			t.Errorf("Search(%q) = %d hits, want 1", q, len(hits))
		}
	}
	if hits, err := s.Search("", nil, 10); err == nil && len(hits) != 0 {
		t.Errorf("empty query returned %d hits", len(hits))
	}
}

func TestSearchStillHonorsFTS5Operators(t *testing.T) {
	s := newTestStore(t, Actor{Type: "human", ID: "u"})
	s.AddNote(nil, "alpha only", "manual")
	s.AddNote(nil, "beta only", "manual")
	hits, err := s.Search("alpha OR beta", nil, 10)
	if err != nil || len(hits) != 2 {
		t.Fatalf("OR query: %d hits, %v; want 2", len(hits), err)
	}
	hits, err = s.Search("alph*", nil, 10)
	if err != nil || len(hits) != 1 {
		t.Fatalf("prefix query: %d hits, %v; want 1", len(hits), err)
	}
}

func TestReviseSpecReindexesSearch(t *testing.T) {
	s := newTestStore(t, Actor{Type: "human", ID: "u"})
	id, err := s.AddSpec("Auth spec", "uses sessions cookies")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReviseSpec(id, "uses hardware tokens"); err != nil {
		t.Fatal(err)
	}
	if hits, _ := s.Search("cookies", nil, 10); len(hits) != 0 {
		t.Errorf("old spec text still searchable: %d hits", len(hits))
	}
	hits, err := s.Search("hardware", nil, 10)
	if err != nil || len(hits) != 1 || hits[0].Kind != "spec" || hits[0].RefID != id {
		t.Fatalf("revised text not searchable: %+v, %v", hits, err)
	}
	// title still indexed; exactly one row per spec
	if hits, _ := s.Search("Auth", nil, 10); len(hits) != 1 {
		t.Errorf("title search = %d hits, want exactly 1 (no duplicate index rows)", len(hits))
	}
}
