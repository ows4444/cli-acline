package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"acline/internal/app"
	"acline/internal/checkrun"
	"acline/internal/store"
)

// --- approvals ---

func newApproveCmd(c *cli) *cobra.Command {
	var (
		approveKind string
		approveBy   string
		approveNote string
		approveRole string
	)
	cmd := &cobra.Command{
		Use:   "approve <task-id>",
		Short: "Record human approval of a task (oversight evidence, not an implication)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], "task")
			if err != nil {
				return err
			}
			var aid int64
			err = c.withApprovalToken("approving task #"+args[0], func(token string) error {
				var rerr error
				aid, rerr = app.Approve(c.st, app.ApproveRequest{
					TaskID: id, Kind: approveKind, By: approveBy, Note: approveNote, Token: token,
					RoleArg: approveRole, AllowCwdFallback: true, Tree: c.taskTree(id),
				})
				return rerr
			})
			if errors.Is(err, store.ErrAgentCannotApprove) {
				return fmt.Errorf("%w: a person approves from their own terminal (`acline approve %d`); an agent needs the approval token — see `acline auth init`", err, id)
			}
			if err != nil {
				return err
			}
			fmt.Printf("approval #%d recorded for task #%d\n", aid, id)
			return nil
		},
	}
	cmd.Flags().StringVar(&approveKind, "kind", "code_review", "code_review|override")
	cmd.Flags().StringVar(&approveBy, "by", "", "who approved (required when running as an agent)")
	cmd.Flags().StringVar(&approveNote, "note", "", "approval note")
	cmd.Flags().StringVar(&approveRole, "role", "", "role name (default: $ACLINE_ROLE or the active session's role) -- once a project has a can_approve role, the gate requires one")
	return cmd
}

func newRejectCmd(c *cli) *cobra.Command {
	var (
		approveBy   string
		approveRole string
	)
	var (
		rejectNote string
	)
	cmd := &cobra.Command{
		Use:   "reject <task-id>",
		Short: "Record a rejection at review",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], "task")
			if err != nil {
				return err
			}
			aid, err := app.Reject(c.st, app.RejectRequest{
				TaskID: id, By: approveBy, Note: rejectNote,
				RoleArg: approveRole, AllowCwdFallback: true,
			})
			if err != nil {
				return err
			}
			fmt.Printf("rejection #%d recorded for task #%d\n", aid, id)
			return nil
		},
	}
	cmd.Flags().StringVar(&approveBy, "by", "", "who rejected")
	cmd.Flags().StringVar(&rejectNote, "note", "", "why it was rejected")
	cmd.Flags().StringVar(&approveRole, "role", "", "role name (default: $ACLINE_ROLE or the active session's role)")
	return cmd
}

// --- checks ---

func newCheckCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Record and inspect verification results",
	}
	cmd.AddCommand(newCheckRecordCmd(c), newCheckRunCmd(c), newCheckListCmd(c), newCheckRunnerCmd(c))
	return cmd
}

func newCheckRecordCmd(c *cli) *cobra.Command {
	var (
		checkKind   string
		checkStatus string
		checkDetail string
		checkRole   string
	)
	cmd := &cobra.Command{
		Use:   "record <task-id>",
		Short: "Record a verification result against a task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], "task")
			if err != nil {
				return err
			}
			var cid int64
			err = c.withApprovalToken("recording a human_review check", func(token string) error {
				var cerr error
				cid, cerr = app.RecordCheck(c.st, app.RecordCheckRequest{
					TaskID: id, Kind: checkKind, Status: checkStatus, Detail: checkDetail, Token: token,
					RoleArg: checkRole, AllowCwdFallback: true, Hash: c.hashTree,
				})
				return cerr
			})
			if errors.Is(err, store.ErrAgentCannotRecordHumanReview) {
				return fmt.Errorf("%w (an agent records test/lint/sast/sca results with `acline check run`)", err)
			}
			if err != nil {
				return err
			}
			fmt.Printf("check #%d recorded: %s %s\n", cid, checkKind, checkStatus)
			return nil
		},
	}
	cmd.Flags().StringVar(&checkKind, "kind", "", "test|sast|sca|lint|human_review|eval")
	cmd.Flags().StringVar(&checkStatus, "status", "", "pass|fail|skipped")
	cmd.Flags().StringVar(&checkDetail, "detail", "", "tool output, coverage, finding count, etc.")
	cmd.Flags().StringVar(&checkRole, "role", "", "role name (default: $ACLINE_ROLE or the active session's role)")
	_ = cmd.MarkFlagRequired("kind")
	_ = cmd.MarkFlagRequired("status")
	return cmd
}

func newCheckRunCmd(c *cli) *cobra.Command {
	var (
		checkRole       string
		checkRunKind    string
		checkRunCommand string
		checkRunTimeout time.Duration
	)
	cmd := &cobra.Command{
		Use:   "run <task-id>",
		Short: "Run a real tool (test|sast|sca|lint) and record its result against a task",
		Long: "Runs the kind's tool in the task's project directory (the current directory for a task with no registered path) and records pass (exit 0), fail (non-zero exit or timeout),\n" +
			"or skipped (tool not installed, or no default runner for this project). Skipped is never pass.\n" +
			"Defaults for a Go project (go.mod): test=go test ./..., lint=go vet ./..., sast=gosec ./..., sca=govulncheck ./...\n" +
			"A project can set its own command per kind (`acline check runner set`), which replaces the default.\n" +
			"Precedence: --cmd, then the project's runner, then the default. Commands run directly, not through a shell.\n" +
			"--cmd is a person's choice: an agent is refused (or needs the approval token), since a command the caller\n" +
			"picks, like `true`, would otherwise count as runner evidence.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], "task")
			if err != nil {
				return err
			}
			req := app.RunCheckRequest{
				TaskID: id, Kind: checkRunKind, Command: checkRunCommand, Timeout: checkRunTimeout,
				RoleArg: checkRole, AllowCwdFallback: true, Hash: c.hashTree,
			}
			var cid int64
			var res checkrun.Result
			run := func() error {
				var rerr error
				cid, res, rerr = app.RunCheck(context.Background(), c.st, req)
				return rerr
			}
			if req.Command != "" {
				// --cmd is a person's choice: an agent needs the approval token.
				err = c.withApprovalToken("running a check with --cmd", func(tok string) error {
					req.Token = tok
					return run()
				})
			} else {
				err = run()
			}
			if err != nil {
				return err
			}
			fmt.Printf("check #%d recorded: %s %s\n%s\n", cid, checkRunKind, res.Status, res.Detail)
			return nil
		},
	}
	cmd.Flags().StringVar(&checkRunKind, "kind", "", "test|sast|sca|lint")
	cmd.Flags().StringVar(&checkRunCommand, "cmd", "", "command to run instead of the default (no shell)")
	cmd.Flags().DurationVar(&checkRunTimeout, "timeout", checkrun.DefaultTimeout, "give up (and record fail) after this long")
	cmd.Flags().StringVar(&checkRole, "role", "", "role name (default: $ACLINE_ROLE or the active session's role)")
	_ = cmd.MarkFlagRequired("kind")
	return cmd
}

func newCheckListCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list <task-id>",
		Short: "List verification results for a task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], "task")
			if err != nil {
				return err
			}
			checks, err := c.st.ListChecks(id)
			if err != nil {
				return err
			}
			if len(checks) == 0 {
				fmt.Println("no checks recorded")
				return nil
			}
			for _, c := range checks {
				fmt.Printf("#%-4d %-6s %-13s %s %s\n", c.ID, c.Status, c.Kind, c.CreatedAt, c.Detail.String)
			}
			return nil
		},
	}
	return cmd
}

// currentTree fingerprints the current directory ("" if it cannot be
// determined). Only the Stop hook uses it: hooks run from the project root
// (CLAUDE_PROJECT_DIR). Commands about a task use taskTree / taskGateTree.
func (c *cli) currentTree() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	return c.hashTree(dir)
}

// taskTree fingerprints the task's project root (app.TaskDir), the directory
// `check run` runs in, so a recorded result and the gate compare like with like
// from any subfolder.
func (c *cli) taskTree(taskID int64) string { return app.TaskTree(c.st, taskID, c.hashTree) }

// taskGateTree is taskTree for the completion gate: a directory that cannot be
// fingerprinted is store.TreeUnavailable rather than "", which the gate would
// read as "nothing to compare" and let stale evidence pass.
func (c *cli) taskGateTree(taskID int64) string { return app.GateTree(c.st, taskID, c.hashTree) }
