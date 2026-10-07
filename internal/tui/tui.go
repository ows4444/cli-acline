// Package tui is acline's terminal UI: a third adapter next to the CLI and the
// MCP server, for a person at a terminal. It reads and writes through
// internal/app and the store's own checks, like the other two, and it is the
// only package that imports github.com/ows4444/tui (a test enforces that), so
// the rest of acline never depends on the library directly.
package tui

import (
	"errors"
	"io"
	"os"
	"time"

	tk "github.com/ows4444/tui"

	"acline/internal/store"
	"acline/internal/worktree"
)

// ErrAgent is returned when an agent tries to run the TUI. It is a person's
// interface: what it shows and the actions it offers assume a person is acting.
var ErrAgent = errors.New("acline tui is for a person at a terminal; this environment declares an agent (ACLINE_ACTOR_TYPE/ACLINE_MODEL)")

// Options configures Run.
type Options struct {
	Store      *store.Store
	ProjectID  *int64                  // the project shown; nil shows every project
	Accessible bool                    // append-only, unstyled output for screen readers
	Hash       func(dir string) string // fingerprints a project's tree; nil: worktree.Hash
	Self       string                  // the acline binary, run for the orchestrator; "": this executable
	In         io.Reader
	Out        io.Writer // In and Out default to the terminal
}

// watchInterval is how often the TUI checks whether the store changed.
const watchInterval = time.Second

// Run shows the TUI until the person quits.
func Run(o Options) error {
	if o.Store.Actor.Type != "human" {
		return ErrAgent
	}
	hash := o.Hash
	if hash == nil {
		hash = worktree.Hash
	}
	self, err := o.Self, error(nil)
	if self == "" {
		if self, err = os.Executable(); err != nil {
			return err
		}
	}
	done := &cleanups{}
	defer done.run() // a run still going ends with the TUI
	m, err := newShell(o.Store, o.ProjectID, hash, defaultScreens()...)
	if err != nil {
		return err
	}
	m.self, m.cleanup, m.watchEvery = self, done, watchInterval
	if m, err = m.reload(); err != nil {
		return err
	}
	opts := []tk.ProgramOption{tk.WithAltScreen(!o.Accessible), tk.WithAccessible(o.Accessible)}
	if o.In != nil {
		opts = append(opts, tk.WithInput(o.In))
	}
	if o.Out != nil {
		opts = append(opts, tk.WithOutput(o.Out))
	}
	_, err = tk.NewProgram(m, opts...).Run()
	return err
}
