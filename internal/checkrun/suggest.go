package checkrun

import (
	"os"
	"path/filepath"
)

// Suggestion is a runner command a person might set for a project whose
// ecosystem has no built-in default. It is only ever printed: a runner decides
// what counts as evidence, so acline never sets one itself.
type Suggestion struct {
	Kind    string
	Command string
}

// ecosystems maps a marker file to conventional runner commands. Go is absent:
// it has built-in defaults (see defaults).
var ecosystems = []struct {
	marker  string
	name    string
	runners []Suggestion
}{
	{"package.json", "Node.js", []Suggestion{{"test", "npm test"}, {"lint", "npm run lint"}, {"sca", "npm audit --audit-level=high"}}},
	{"pyproject.toml", "Python", []Suggestion{{"test", "pytest"}, {"lint", "ruff check ."}, {"sast", "bandit -r ."}, {"sca", "pip-audit"}}},
	{"requirements.txt", "Python", []Suggestion{{"test", "pytest"}, {"lint", "ruff check ."}, {"sast", "bandit -r ."}, {"sca", "pip-audit"}}},
	{"Cargo.toml", "Rust", []Suggestion{{"test", "cargo test"}, {"lint", "cargo clippy -- -D warnings"}, {"sca", "cargo audit"}}},
}

// SuggestRunners returns the ecosystem detected in dir and conventional runner
// commands for it, or ("", nil) when dir is a Go module (which has defaults) or
// holds no marker acline recognises.
func SuggestRunners(dir string) (string, []Suggestion) {
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
		return "", nil
	}
	for _, e := range ecosystems {
		if _, err := os.Stat(filepath.Join(dir, e.marker)); err == nil {
			return e.name, e.runners
		}
	}
	return "", nil
}
