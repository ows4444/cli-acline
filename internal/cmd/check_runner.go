package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"acline/internal/store"
)

var runnerProject string

var checkRunnerCmd = &cobra.Command{
	Use:   "runner",
	Short: "Set the command `check run` uses for a project (human-only)",
	Long: "By default `check run` uses Go tooling (see `check run --help`). A project of any other kind sets its own\\n" +
		"command per check kind here. Agents cannot set one: a runner is code that later runs on request, and\\n" +
		"the agent whose work is being verified must not choose it.",
}

func runnerProjectID() (int64, error) {
	pid, err := resolveProjectFlag(runnerProject)
	if err != nil {
		return 0, err
	}
	if pid == nil {
		return 0, errors.New("no project: pass --project or run inside a registered project")
	}
	return *pid, nil
}

var checkRunnerSetCmd = &cobra.Command{
	Use:   "set <kind> <command...>",
	Short: "Set the command for a check kind (test|lint|sast|sca)",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		pid, err := runnerProjectID()
		if err != nil {
			return err
		}
		command := joinCommand(args[1:])
		err = withApprovalToken("setting the "+args[0]+" runner", func(token string) error {
			return st.SetCheckRunner(pid, args[0], command, token)
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

var checkRunnerUnsetCmd = &cobra.Command{
	Use:   "unset <kind>",
	Short: "Remove a project's override, restoring the default",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		pid, err := runnerProjectID()
		if err != nil {
			return err
		}
		if err := withApprovalToken("removing the "+args[0]+" runner", func(token string) error {
			return st.UnsetCheckRunner(pid, args[0], token)
		}); err != nil {
			return err
		}
		fmt.Printf("project #%d %s runner removed\n", pid, args[0])
		return nil
	},
}

var checkRunnerListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the project's configured runners",
	RunE: func(cmd *cobra.Command, args []string) error {
		pid, err := runnerProjectID()
		if err != nil {
			return err
		}
		runners, err := st.ListCheckRunners(pid)
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

func init() {
	checkRunnerCmd.PersistentFlags().StringVar(&runnerProject, "project", "", "project name (default: resolved from cwd/ACLINE_PROJECT)")
	checkRunnerCmd.AddCommand(checkRunnerSetCmd, checkRunnerUnsetCmd, checkRunnerListCmd)
}

// joinCommand joins argv so checkrun.SplitCommand gives the same words back: an
// argument holding whitespace, quotes or a backslash is single-quoted (a ' in it
// closes, escapes and reopens the quote).
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
