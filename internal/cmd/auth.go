package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"acline/internal/store"
)

// auth.go is the CLI half of the approval token (see store/auth.go for why it
// exists). Two properties matter more than convenience:
//
//   - Prompts and the token itself go through /dev/tty, never stdin/stdout. A
//     process an agent spawns can pipe or capture those, but it cannot type at
//     the human's terminal, so it cannot answer a prompt or read a token
//     printed there.
//   - `auth init` needs a human keystroke to proceed. Without that, an agent
//     could enable a token first and lock the human out of their own store.

const approvalTokenEnv = "ACLINE_APPROVAL_TOKEN"

// terminal is what the prompts talk to: the controlling terminal in real use,
// a fake in tests.
type terminal interface {
	io.ReadWriteCloser
}

var openTerminal = func() (terminal, error) {
	return os.OpenFile("/dev/tty", os.O_RDWR, 0)
}

var errNoTerminal = errors.New("no interactive terminal is available to prompt on; run this from a terminal, or set " + approvalTokenEnv)

func promptLine(tty terminal, prompt string, hidden bool) (string, error) {
	// Echo goes off *before* the prompt is shown, so there is no window in
	// which text typed the instant the prompt appears would be displayed.
	if hidden {
		if f, ok := tty.(*os.File); ok {
			if restore := hideInput(f); restore != nil {
				defer restore()
			}
		}
	}
	fmt.Fprint(tty, prompt)
	line, err := bufio.NewReader(tty).ReadString('\n')
	if hidden {
		fmt.Fprintln(tty)
	}
	if err != nil && !(errors.Is(err, io.EOF) && line != "") {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// hideInput turns terminal echo off via stty (no extra dependency). It returns
// nil if it can't, in which case the token is typed visibly -- worse, not fatal.
func hideInput(tty *os.File) func() {
	off := exec.Command("stty", "-echo")
	off.Stdin = tty
	if off.Run() != nil {
		fmt.Fprintln(tty, "(warning: could not hide input)")
		return nil
	}
	return func() {
		on := exec.Command("stty", "echo")
		on.Stdin = tty
		_ = on.Run()
	}
}

// withApprovalToken runs fn with the token from ACLINE_APPROVAL_TOKEN (empty if
// unset). Only if the store says a token is *required* and none was given does
// it prompt on the terminal and retry -- so nothing prompts unless it must, and
// an agent with no terminal just gets the store's "token required" error.
func withApprovalToken(action string, fn func(token string) error) error {
	err := fn(os.Getenv(approvalTokenEnv))
	if !errors.Is(err, store.ErrApprovalTokenRequired) {
		return err
	}
	tty, terr := openTerminal()
	if terr != nil {
		return err
	}
	defer tty.Close()
	token, perr := promptLine(tty, "approval token for "+action+": ", true)
	if perr != nil || token == "" {
		return err
	}
	return fn(token)
}

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage the human approval token that gates approvals and --force",
	Long: "When an approval token is enabled, approving a task, overriding the completion gate with --force, and " +
		"approving agent-written memory all require it. Until a human enables it, none of that changes.\n\n" +
		"The token is a secret only you hold: an agent that runs `acline` in your shell can flip identity " +
		"environment variables, but it cannot supply a token it was never given. Don't export " +
		approvalTokenEnv + " into the shell an agent runs in; let acline prompt you on the terminal instead.",
}

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show whether an approval token is enabled",
	RunE: func(cmd *cobra.Command, args []string) error {
		enabled, err := st.ApprovalTokenEnabled()
		if err != nil {
			return err
		}
		if enabled {
			fmt.Println("approval token: ENABLED (approve, task done --force and memory approve require it)")
		} else {
			fmt.Println("approval token: not enabled — identity is self-declared via ACLINE_ACTOR_TYPE; run `acline auth init` in a terminal to enforce it")
		}
		return nil
	},
}

var authInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Enable the approval token (interactive terminal required)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAuthInit(st)
	},
}

// runAuthInit enables the approval token on s, after a human confirms on the
// controlling terminal. It is shared by `acline auth init` and the offer
// `acline init` makes; both need a real terminal, so an agent's shell cannot
// enable a token it knows.
func runAuthInit(s *store.Store) error {
	tty, err := openTerminal()
	if err != nil {
		return errNoTerminal
	}
	defer tty.Close()
	if enabled, err := s.ApprovalTokenEnabled(); err != nil {
		return err
	} else if enabled {
		return store.ErrApprovalTokenAlreadyEnabled
	}
	fmt.Fprintln(tty, "This will require a secret token to approve tasks, override gates (--force) and approve memory.")
	fmt.Fprintln(tty, "The token is shown once, on this terminal only. Anyone without it cannot approve.")
	answer, err := promptLine(tty, "Type ENABLE to continue: ", false)
	if err != nil {
		return err
	}
	if answer != "ENABLE" {
		return errors.New("cancelled")
	}
	token, err := s.EnableApprovalToken()
	if err != nil {
		return err
	}
	fmt.Fprintf(tty, "\nApproval token (save it in your password manager; it cannot be shown again):\n\n  %s\n\n", token)
	fmt.Println("approval token enabled")
	return nil
}

var authRotateCmd = &cobra.Command{
	Use:   "rotate",
	Short: "Replace the approval token (current token required)",
	RunE: func(cmd *cobra.Command, args []string) error {
		tty, err := openTerminal()
		if err != nil {
			return errNoTerminal
		}
		defer tty.Close()
		current := os.Getenv(approvalTokenEnv)
		if current == "" {
			if current, err = promptLine(tty, "current approval token: ", true); err != nil {
				return err
			}
		}
		token, err := st.RotateApprovalToken(current)
		if err != nil {
			return err
		}
		fmt.Fprintf(tty, "\nNew approval token (the old one no longer works):\n\n  %s\n\n", token)
		fmt.Println("approval token rotated")
		return nil
	},
}

var authDisableCmd = &cobra.Command{
	Use:   "disable",
	Short: "Turn the approval token requirement off (current token required)",
	RunE: func(cmd *cobra.Command, args []string) error {
		tty, err := openTerminal()
		if err != nil {
			return errNoTerminal
		}
		defer tty.Close()
		current := os.Getenv(approvalTokenEnv)
		if current == "" {
			if current, err = promptLine(tty, "current approval token: ", true); err != nil {
				return err
			}
		}
		if err := st.DisableApprovalToken(current); err != nil {
			return err
		}
		fmt.Println("approval token disabled")
		return nil
	},
}

func init() {
	authCmd.AddCommand(authStatusCmd, authInitCmd, authRotateCmd, authDisableCmd)
	rootCmd.AddCommand(authCmd)
}
