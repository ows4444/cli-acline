package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"acline/internal/checkrun"
	"acline/internal/store"
)

func newCheckRunnerCmd(c *cli) *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   "runner",
		Short: "Set the command `check run` uses for a project (human-only)",
		Long: "By default `check run` uses Go tooling (see `check run --help`). A project of any other kind sets its own\\n" +
			"command per check kind here. Agents cannot set one: a runner is code that later runs on request, and\\n" +
			"the agent whose work is being verified must not choose it.",
	}
	cmd.PersistentFlags().StringVar(&project, "project", "", "project name (default: resolved from cwd/ACLINE_PROJECT)")
	cmd.AddCommand(newCheckRunnerSetCmd(c, &project), newCheckRunnerUnsetCmd(c, &project), newCheckRunnerListCmd(c, &project))
	return cmd
}

// runnerProjectID is the project named by the runner commands' --project.
func (c *cli) runnerProjectID(project string) (int64, error) {
	pid, err := c.resolveProjectFlag(project)
	if err != nil {
		return 0, err
	}
	if pid == nil {
		return 0, errors.New("no project: pass --project or run inside a registered project")
	}
	return *pid, nil
}

func newCheckRunnerSetCmd(c *cli, project *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <kind> <command...>",
		Short: "Set the command for a check kind (test|lint|sast|sca)",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			pid, err := c.runnerProjectID(*project)
			if err != nil {
				return err
			}
			command, err := runnerCommand(args[1:])
			if err != nil {
				return err
			}
			if argv, _ := checkrun.SplitCommand(command); len(argv) > 0 {
				if _, lerr := exec.LookPath(argv[0]); lerr != nil {
					fmt.Fprintf(os.Stderr, "warning: %q is not on PATH here, so `check run --kind %s` will record skipped until it is installed\n", argv[0], args[0])
				}
			}
			err = c.withApprovalToken("setting the "+args[0]+" runner", func(token string) error {
				return c.st.SetCheckRunner(pid, args[0], command, token)
			})
			if errors.Is(err, store.ErrAgentCannotConfigureRunner) {
				return err
			}
			if err != nil {
				return err
			}
			fmt.Printf("project #%d %s runner: %s\n", pid, args[0], command)
			return nil
		},
	}
	return cmd
}

func newCheckRunnerUnsetCmd(c *cli, project *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unset <kind>",
		Short: "Remove a project's override, restoring the default",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pid, err := c.runnerProjectID(*project)
			if err != nil {
				return err
			}
			if err := c.withApprovalToken("removing the "+args[0]+" runner", func(token string) error {
				return c.st.UnsetCheckRunner(pid, args[0], token)
			}); err != nil {
				return err
			}
			fmt.Printf("project #%d %s runner removed\n", pid, args[0])
			return nil
		},
	}
	return cmd
}

func newCheckRunnerListCmd(c *cli, project *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the project's configured runners",
		RunE: func(cmd *cobra.Command, args []string) error {
			pid, err := c.runnerProjectID(*project)
			if err != nil {
				return err
			}
			runners, err := c.st.ListCheckRunners(pid)
			if err != nil {
				return err
			}
			if len(runners) == 0 {
				fmt.Println("no runners configured (check run uses its defaults)")
				return nil
			}
			for _, r := range runners {
				fmt.Printf("%-5s %s\n", r.Kind, r.Command)
			}
			return nil
		},
	}
	return cmd
}

// joinCommand joins argv so checkrun.SplitCommand gives the same words back: an
// argument holding whitespace, quotes or a backslash is single-quoted (a ' in it
// closes, escapes and reopens the quote).
// runnerCommand turns `check runner set`'s arguments into the stored command.
// Several arguments are the command's words, as the shell split them. One
// argument holding whitespace is the whole command typed in quotes
// (`check runner set test "go test ./..."`): it is split the way `check run`
// will split it, rather than stored as a single quoted word that names no program.
func runnerCommand(args []string) (string, error) {
	if len(args) == 1 && strings.ContainsAny(strings.TrimSpace(args[0]), " \t\n") {
		argv, err := checkrun.SplitCommand(args[0])
		if err != nil {
			return "", err
		}
		return joinCommand(argv), nil
	}
	return joinCommand(args), nil
}

func joinCommand(argv []string) string {
	out := make([]string, len(argv))
	for i, a := range argv {
		if a != "" && !strings.ContainsAny(a, " \t\n'\"\\") {
			out[i] = a
			continue
		}
		out[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(out, " ")
}
