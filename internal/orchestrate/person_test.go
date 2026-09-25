package orchestrate

import "acline/internal/store"

// personView is s acting as a person, for fixtures only a person may create
// (an approved spec, an accepted decision, reviewed memory). The orchestrator
// itself always runs as an agent.
func personView(s *store.Store) *store.Store {
	p := *s
	p.Actor = store.Actor{Type: "human", ID: "tester"}
	return &p
}
