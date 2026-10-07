package cmd

import (
	"errors"
	"os"

	"github.com/spf13/cobra"

	"acline/internal/tui"
)

var errTUINeedsTerminal = errors.New("acline tui needs a terminal on stdin and stdout; for scripts use the other commands, which print plain text")

func newTUICmd(c *cli) *cobra.Command {
	var (
		project    string
		accessible bool
	)
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Full-screen terminal UI for a person: dashboard, tasks, reviews (q quits)",
		Long: "Opens acline's terminal UI. It is a person's interface: it refuses to start\n" +
			"without a terminal on stdin and stdout, or when the environment declares an\n" +
			"agent, and agents' shells are denied it. Colour follows NO_COLOR.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !c.stdioTerminal() {
				return errTUINeedsTerminal
			}
			projectID, err := c.resolveProjectFlag(project)
			if err != nil {
				return err
			}
			return tui.Run(tui.Options{Store: c.st, ProjectID: projectID, Accessible: accessible})
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project to show (default: the current project)")
	cmd.Flags().BoolVar(&accessible, "accessible", false, "append-only, unstyled output for screen readers")
	return cmd
}

// stdioIsTerminal reports whether both stdin and stdout are terminals.
func stdioIsTerminal() bool {
	for _, f := range []*os.File{os.Stdin, os.Stdout} {
		info, err := f.Stat()
		if err != nil || info.Mode()&os.ModeCharDevice == 0 {
			return false
		}
	}
	return true
}
