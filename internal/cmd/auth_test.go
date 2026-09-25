package cmd

import (
	"bytes"
	"errors"
	"strconv"
	"strings"
	"testing"

	"acline/internal/store"
)

// fakeTerminal stands in for /dev/tty: reads come from `input`, everything
// written to it is captured in `out`.
type fakeTerminal struct {
	in  *strings.Reader
	out bytes.Buffer
}

func (f *fakeTerminal) Read(p []byte) (int, error)  { return f.in.Read(p) }
func (f *fakeTerminal) Write(p []byte) (int, error) { return f.out.Write(p) }
func (f *fakeTerminal) Close() error                { return nil }

func withFakeTerminal(t *testing.T, input string) *fakeTerminal {
	t.Helper()
	tty := &fakeTerminal{in: strings.NewReader(input)}
	prev := openTerminal
	openTerminal = func() (terminal, error) { return tty, nil }
	t.Cleanup(func() { openTerminal = prev })
	return tty
}

func withNoTerminal(t *testing.T) {
	t.Helper()
	prev := openTerminal
	openTerminal = func() (terminal, error) { return nil, errors.New("no tty") }
	t.Cleanup(func() { openTerminal = prev })
}

func TestAuthInitNeedsAHumanAtATerminal(t *testing.T) {
	s := withTestStore(t)

	// An agent's shell has no terminal: it must not be able to enable a token
	// (which would lock the human out).
	withNoTerminal(t)
	if err := authInitCmd.RunE(authInitCmd, nil); !errors.Is(err, errNoTerminal) {
		t.Fatalf("init without a terminal = %v, want errNoTerminal", err)
	}
	if on, _ := s.ApprovalTokenEnabled(); on {
		t.Fatal("token was enabled without a terminal")
	}

	// A terminal, but the human doesn't confirm.
	tty := withFakeTerminal(t, "no\n")
	if err := authInitCmd.RunE(authInitCmd, nil); err == nil {
		t.Fatal("init proceeded without the ENABLE confirmation")
	}
	if on, _ := s.ApprovalTokenEnabled(); on {
		t.Fatal("token enabled without confirmation")
	}
	_ = tty

	// Confirmed: the token is shown on the terminal, never on stdout.
	tty = withFakeTerminal(t, "ENABLE\n")
	stdout := captureStdout(t, func() {
		if err := authInitCmd.RunE(authInitCmd, nil); err != nil {
			t.Fatalf("init: %v", err)
		}
	})
	if on, _ := s.ApprovalTokenEnabled(); !on {
		t.Fatal("token not enabled after confirmation")
	}
	shown := tty.out.String()
	i := strings.Index(shown, "acl_")
	if i < 0 {
		t.Fatalf("token was not shown on the terminal: %q", shown)
	}
	token := strings.Fields(shown[i:])[0]
	if strings.Contains(string(stdout), "acl_") {
		t.Fatalf("token leaked to stdout (capturable by a calling process): %q", stdout)
	}
	if err := s.CheckApprovalToken(token); err != nil {
		t.Fatalf("the token shown on the terminal doesn't work: %v", err)
	}
}

func TestWithApprovalTokenPromptsOnlyWhenRequired(t *testing.T) {
	s := withTestStore(t)
	t.Setenv(approvalTokenEnv, "")
	token, err := s.EnableApprovalToken()
	if err != nil {
		t.Fatal(err)
	}

	do := func(tok string) error {
		return s.CheckApprovalToken(tok)
	}

	// no env, no terminal -> the store's "required" error surfaces (an agent's situation)
	withNoTerminal(t)
	if err := withApprovalToken("x", do); !errors.Is(err, store.ErrApprovalTokenRequired) {
		t.Fatalf("no env/no tty = %v, want required", err)
	}

	// no env, human types it at the terminal
	tty := withFakeTerminal(t, token+"\n")
	if err := withApprovalToken("approving task #1", do); err != nil {
		t.Fatalf("prompted token = %v", err)
	}
	if !strings.Contains(tty.out.String(), "approval token for approving task #1") {
		t.Errorf("prompt text missing: %q", tty.out.String())
	}

	// wrong token typed
	withFakeTerminal(t, "acl_nope\n")
	if err := withApprovalToken("x", do); !errors.Is(err, store.ErrApprovalTokenInvalid) {
		t.Fatalf("wrong typed token = %v", err)
	}

	// env token is used without prompting at all
	t.Setenv(approvalTokenEnv, token)
	withNoTerminal(t)
	if err := withApprovalToken("x", do); err != nil {
		t.Fatalf("env token = %v", err)
	}

	// an invalid env token is an error, not a prompt
	t.Setenv(approvalTokenEnv, "acl_bad")
	withFakeTerminal(t, token+"\n")
	if err := withApprovalToken("x", do); !errors.Is(err, store.ErrApprovalTokenInvalid) {
		t.Fatalf("bad env token = %v, want invalid (no fallback prompt)", err)
	}
}

func TestApproveCommandRequiresTheTokenWhenEnabled(t *testing.T) {
	s := withTestStore(t)
	t.Setenv(approvalTokenEnv, "")
	id, _ := s.AddTask("t", "", "normal", store.TaskOpts{Risk: "high"})
	token, _ := s.EnableApprovalToken()

	prevBy, prevKind := approveBy, approveKind
	approveBy, approveKind = "alice", "code_review"
	t.Cleanup(func() { approveBy, approveKind = prevBy, prevKind })

	withNoTerminal(t)
	err := approveCmd.RunE(approveCmd, []string{itoa(id)})
	if !errors.Is(err, store.ErrApprovalTokenRequired) {
		t.Fatalf("approve without token = %v", err)
	}
	if n := len(mustApprovals(t, s, id)); n != 0 {
		t.Fatalf("%d approval(s) recorded by a refused command", n)
	}

	t.Setenv(approvalTokenEnv, token)
	captureStdout(t, func() {
		if err := approveCmd.RunE(approveCmd, []string{itoa(id)}); err != nil {
			t.Fatalf("approve with token = %v", err)
		}
	})
	if n := len(mustApprovals(t, s, id)); n != 1 {
		t.Fatalf("approvals = %d, want 1", n)
	}
}

func TestTaskDoneForceRequiresTheTokenWhenEnabled(t *testing.T) {
	s := withTestStore(t)
	t.Setenv(approvalTokenEnv, "")
	id, _ := s.AddTask("t", "", "normal", store.TaskOpts{Risk: "high"})
	token, _ := s.EnableApprovalToken()

	prev := doneForce
	doneForce = true
	t.Cleanup(func() { doneForce = prev })

	withNoTerminal(t)
	if err := taskDoneCmd.RunE(taskDoneCmd, []string{itoa(id)}); !errors.Is(err, store.ErrApprovalTokenRequired) {
		t.Fatalf("force without token = %v", err)
	}
	if got, _ := s.GetTask(id); got.Status == "done" {
		t.Fatal("task forced done without a token")
	}
	t.Setenv(approvalTokenEnv, token)
	captureStdout(t, func() {
		if err := taskDoneCmd.RunE(taskDoneCmd, []string{itoa(id)}); err != nil {
			t.Fatalf("force with token = %v", err)
		}
	})
	if got, _ := s.GetTask(id); got.Status != "done" {
		t.Fatal("force with the right token did not complete the task")
	}
}

func itoa(id int64) string { return strconv.FormatInt(id, 10) }

func mustApprovals(t *testing.T, s *store.Store, id int64) []store.Approval {
	t.Helper()
	a, err := s.ListApprovals(id)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
