package cmd

import (
	"errors"
	"testing"

	"acline/internal/store"
	"acline/internal/tui"
)

func TestTUIRefusesWithoutATerminal(t *testing.T) {
	c := newTestCLI(t)
	c.stdioTerminal = func() bool { return false }
	if err := c.run("tui"); !errors.Is(err, errTUINeedsTerminal) {
		t.Fatalf("tui with no terminal = %v, want errTUINeedsTerminal", err)
	}
}

func TestTUIRefusesAnAgent(t *testing.T) {
	c := newTestCLI(t)
	c.stdioTerminal = func() bool { return true }
	c.st.Actor = store.Actor{Type: "agent", ID: "claude-code"}
	if err := c.run("tui"); !errors.Is(err, tui.ErrAgent) {
		t.Fatalf("tui as an agent = %v, want tui.ErrAgent", err)
	}
}

func TestTUIRejectsAnUnknownProject(t *testing.T) {
	c := newTestCLI(t)
	c.stdioTerminal = func() bool { return true }
	if err := c.run("tui", "--project", "nope"); err == nil {
		t.Fatal("tui --project nope started")
	}
}
