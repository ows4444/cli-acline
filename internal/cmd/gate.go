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
	"acline/internal/worktree"
)

// --- approvals ---

var (
	approveKind string
	approveBy   string
	approveNote string
	approveRole string
)

var approveCmd = &cobra.Command{
	Use:   "approve <task-id>",
	Short: "Record human approval of a task (oversight evidence, not an implication)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := parseID(args[0], "task")
		if err != nil {
			return err
		}
		var aid int64
		err = withApprovalToken("approving task #"+args[0], func(token string) error {
			var rerr error
			aid, rerr = app.Approve(st, app.ApproveRequest{
				TaskID: id, Kind: approveKind, By: approveBy, Note: approveNote, Token: token,
				RoleArg: approveRole, AllowCwdFallback: true,
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

var rejectNote string

var rejectCmd = &cobra.Command{
	Use:   "reject <task-id>",
	Short: "Record a rejection at review",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := parseID(args[0], "task")
		if err != nil {
			return err
		}
		aid, err := app.Reject(st, app.RejectRequest{
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

// --- checks ---

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Record and inspect verification results",
}

var (
	checkKind   string
	checkStatus string
	checkDetail string
	checkRole   string
)

var checkRecordCmd = &cobra.Command{
	Use:   "record <task-id>",
	Short: "Record a verification result against a task",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := parseID(args[0], "task")
		if err != nil {
			return err
		}
		if _, err := st.GetTask(id); err != nil {
			return err
		}
		roleID, err := resolveRoleFlag(checkRole, "")
		if err != nil {
			return err
		}
		var cid int64
		err = withApprovalToken("recording a human_review check", func(token string) error {
			var cerr error
			// A hand-recorded result is still tied to the tree it was recorded against, so
			// the gate can warn when the code has changed since.
			cid, cerr = st.AddCheckWithMeta(id, roleID, checkKind, checkStatus, checkDetail, token,
				store.CheckMeta{Source: store.CheckSourceManual, TreeHash: currentTree()})
			return cerr
		})
		if errors.Is(err, store.ErrAgentCannotRecordHumanReview) {
			return fmt.Errorf("%w (an agent records test/lint/sast/sca results with `acline check run`)", err)
		}
		if err != nil {
			return err
		}
		logTaskEvent(id, "check", fmt.Sprintf("%s: %s", checkKind, checkStatus))
		fmt.Printf("check #%d recorded: %s %s\n", cid, checkKind, checkStatus)
		return nil
	},
}

var (
	checkRunKind    string
	checkRunCommand string
	checkRunTimeout time.Duration
)

var checkRunCmd = &cobra.Command{
	Use:   "run <task-id>",
	Short: "Run a real tool (test|sast|sca|lint) and record its result against a task",
	Long: "Runs the kind's tool in the current directory and records pass (exit 0), fail (non-zero exit or timeout),\n" +
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
		if _, err := st.GetTask(id); err != nil {
			return err
		}
		roleID, err := resolveRoleFlag(checkRole, "")
		if err != nil {
			return err
		}
		dir, err := os.Getwd()
		if err != nil {
			return err
		}
		command := checkRunCommand // precedence: --cmd, then the project's configured runner, then the Go default
		// A command the caller names is not runner evidence unless a person chose
		// it: authorize before running it, so a refused agent never executes it.
		var token string
		adHoc := command != ""
		if adHoc {
			if err := withApprovalToken("running a check with --cmd", func(tok string) error {
				token = tok
				return st.AuthorizeAdHocCheckCommand(tok)
			}); err != nil {
				return err
			}
		}
		if command == "" {
			task, terr := st.GetTask(id)
			if terr != nil {
				return terr
			}
			if command, err = st.CheckRunnerCommand(nullIntPtr(task.ProjectID), checkRunKind); err != nil {
				return err
			}
		}
		res, err := checkrun.Run(context.Background(), checkRunKind, dir, command, checkRunTimeout)
		if err != nil {
			return err
		}
		// Fingerprint the tree after the run, so it matches what the gate sees later
		// (the tool may have written files that stay in the working directory).
		tree := hashTree(dir)
		cid, err := st.AddCheckWithMeta(id, roleID, checkRunKind, res.Status, res.Detail+treeNote(tree), token,
			store.CheckMeta{Source: store.CheckSourceRunner, TreeHash: tree, AdHocCommand: adHoc})
		if err != nil {
			return err
		}
		logTaskEvent(id, "check", fmt.Sprintf("%s: %s", checkRunKind, res.Status))
		fmt.Printf("check #%d recorded: %s %s\n%s\n", cid, checkRunKind, res.Status, res.Detail)
		return nil
	},
}

var checkListCmd = &cobra.Command{
	Use:   "list <task-id>",
	Short: "List verification results for a task",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := parseID(args[0], "task")
		if err != nil {
			return err
		}
		checks, err := st.ListChecks(id)
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

func init() {
	approveCmd.Flags().StringVar(&approveKind, "kind", "code_review", "code_review|override")
	approveCmd.Flags().StringVar(&approveBy, "by", "", "who approved (required when running as an agent)")
	approveCmd.Flags().StringVar(&approveNote, "note", "", "approval note")
	approveCmd.Flags().StringVar(&approveRole, "role", "", "role name (default: $ACLINE_ROLE or the active session's role) -- once a project has a can_approve role, the gate requires one")

	rejectCmd.Flags().StringVar(&approveBy, "by", "", "who rejected")
	rejectCmd.Flags().StringVar(&rejectNote, "note", "", "why it was rejected")
	rejectCmd.Flags().StringVar(&approveRole, "role", "", "role name (default: $ACLINE_ROLE or the active session's role)")

	checkRecordCmd.Flags().StringVar(&checkKind, "kind", "", "test|sast|sca|lint|human_review|eval")
	checkRecordCmd.Flags().StringVar(&checkStatus, "status", "", "pass|fail|skipped")
	checkRecordCmd.Flags().StringVar(&checkDetail, "detail", "", "tool output, coverage, finding count, etc.")
	checkRecordCmd.Flags().StringVar(&checkRole, "role", "", "role name (default: $ACLINE_ROLE or the active session's role)")
	_ = checkRecordCmd.MarkFlagRequired("kind")
	_ = checkRecordCmd.MarkFlagRequired("status")

	checkRunCmd.Flags().StringVar(&checkRunKind, "kind", "", "test|sast|sca|lint")
	checkRunCmd.Flags().StringVar(&checkRunCommand, "cmd", "", "command to run instead of the default (no shell)")
	checkRunCmd.Flags().DurationVar(&checkRunTimeout, "timeout", checkrun.DefaultTimeout, "give up (and record fail) after this long")
	checkRunCmd.Flags().StringVar(&checkRole, "role", "", "role name (default: $ACLINE_ROLE or the active session's role)")
	_ = checkRunCmd.MarkFlagRequired("kind")

	checkCmd.AddCommand(checkRecordCmd, checkRunCmd, checkListCmd, checkRunnerCmd)
	rootCmd.AddCommand(approveCmd, rejectCmd, checkCmd)
}

// currentTree fingerprints the working tree in the current directory ("" if it
// cannot be determined). `check run`, `check record`, `task gate` and `task done`
// all use the current directory, so a result and the gate compare like with like.
func currentTree() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	return hashTree(dir)
}

// hashTree fingerprints a directory; a variable so tests can make it fail.
var hashTree = worktree.Hash

// gateTree is currentTree for the completion gate: when the directory is known
// but cannot be fingerprinted it says so (store.TreeUnavailable) instead of "",
// which the gate would read as "nothing to compare" and let stale evidence pass.
func gateTree() string {
	if _, err := os.Getwd(); err != nil {
		return ""
	}
	if tree := currentTree(); tree != "" {
		return tree
	}
	return store.TreeUnavailable
}

// treeNote is appended to a check's detail when its tree could not be
// fingerprinted, so the record says why it is not bound to one.
func treeNote(tree string) string {
	if tree != "" {
		return ""
	}
	return store.UnboundTreeNote
}
