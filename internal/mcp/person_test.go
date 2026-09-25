package mcp

import "acline/internal/store"

// asPerson is s acting as a person, for fixtures only a person may create.
func asPerson(s *store.Store) *store.Store {
	p := *s
	p.Actor = store.Actor{Type: "human", ID: "tester"}
	return &p
}
