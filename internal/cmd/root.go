package cmd

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"acline/internal/embed"
	"acline/internal/store"
	"acline/internal/worktree"
)

// cli is what every command shares: the --db flag and the store opened from
// it. Commands are built by constructors that close over one cli, so a test
// builds a fresh command tree around its own store (see newTestCLI).
type cli struct {
	dbPath string
	st     *store.Store
	// opened is true when open created st, so close closes only what it opened
	// and a test's injected store outlives the command.
	opened bool

	// What a test replaces on its own cli; newCLI sets the real ones.
	hashTree        func(dir string) string // fingerprints a directory
	openTerminal    func() (terminal, error)
	stdioTerminal   func() bool                                      // stdin and stdout are a terminal, for the TUI
	runSelf         func(dir string, args ...string) (string, error) // this binary, for the hooks
	guard           func(s *store.Store, vaultRoot string, in io.Reader, out io.Writer) error
	hookTimeout     time.Duration // how long the guard may take before the call is denied
	stopTree        func() string // the working tree, for the Stop hook
	stopTreeTimeout time.Duration // past it the turn ends unblocked
}

// newCLI is a cli with the real terminal, tree hash, guard and subprocesses.
func newCLI() *cli {
	c := &cli{
		hashTree:        worktree.Hash,
		openTerminal:    openTTY,
		stdioTerminal:   stdioIsTerminal,
		runSelf:         runSelfExec,
		guard:           guardCheckTool,
		hookTimeout:     10 * time.Second,
		stopTreeTimeout: 5 * time.Second,
	}
	c.stopTree = c.currentTree
	return c
}

// open opens the store for cmd, unless cmd does not need one or a store was
// injected.
func (c *cli) open(cmd *cobra.Command) error {
	if c.st != nil || !needsStore(cmd) {
		return nil
	}
	path := c.dbPath
	if path == "" {
		p, err := store.DefaultPath()
		if err != nil {
			return err
		}
		path = p
	}
	s, err := store.Open(path)
	if err != nil {
		return err
	}
	// Semantic search is opt-in: only wired up when VOYAGE_API_KEY is
	// set, so a plain `acline` with no key configured never makes a
	// network call and stays fully local/offline (see internal/embed
	// and internal/store/embedding.go).
	if e, ok := embed.FromEnv(); ok {
		s.Embedder = e
	}
	c.st, c.opened = s, true
	return nil
}

func (c *cli) close() error {
	if !c.opened {
		return nil
	}
	c.opened = false
	return c.st.Close()
}

// newRootCmd builds the acline command tree around c.
func newRootCmd(c *cli) *cobra.Command {
	root := &cobra.Command{
		Use:   "acline",
		Short: "Local SDLC tracker: specs, tasks, verification, and an append-only audit trail in SQLite",
		// Errors are printed once by Execute; a failed command is not a usage problem.
		SilenceUsage:       true,
		SilenceErrors:      true,
		PersistentPreRunE:  func(cmd *cobra.Command, args []string) error { return c.open(cmd) },
		PersistentPostRunE: func(cmd *cobra.Command, args []string) error { return c.close() },
	}
	root.Version = shortVersionString()
	root.PersistentFlags().StringVar(&c.dbPath, "db", "", "path to sqlite db (default: ~/.acline/store.db, shared across all tracked projects; override via $ACLINE_DB)")
	root.AddCommand(
		newProjectCmd(c), newAuthCmd(c), newBriefCmd(c), newDashboardCmd(c), newStatusCmd(c), newNoteCmd(c),
		newLogCmd(c), newHistoryCmd(c), newDepCmd(c), newRoleCmd(c), newFeatureCmd(c), newSpecCmd(c),
		newDecisionCmd(c), newRoadmapCmd(c), newExportCmd(c), newMetricsCmd(c), newSearchCmd(c),
		newSnapshotCmd(c), newPolicyCmd(c), newNextCmd(c), newMcpCmd(c), newEvalCmd(c), newMemoryCmd(c),
		newReflectCmd(c), newPlanCmd(c), newTaskCmd(c), newApproveCmd(c), newRejectCmd(c), newCheckCmd(c),
		newContextCmd(c), newVerifyCmd(c), newSessionCmd(c), newDoctorCmd(c), newVersionCmd(c), newPluginCmd(c),
		newInitCmd(c), newOrchestrateCmd(c), newGuardCmd(c), newHookCmd(c), newTUICmd(c),
	)
	return root
}

// needsStore reports whether cmd reads or writes the store. Opening it for
// commands that don't (version, help, shell completion, `guard doctor`, and
// `init`, which may be creating the project the store will track) had a real
// side effect: merely asking for the version created ~/.acline/store.db.
func needsStore(cmd *cobra.Command) bool {
	switch cmd.CommandPath() {
	case "acline init", "acline version", "acline help", "acline guard doctor", "acline plugin export":
		return false
	}
	for c := cmd; c != nil; c = c.Parent() {
		if c.Name() == "completion" || c.Name() == cobra.ShellCompRequestCmd || c.Name() == cobra.ShellCompNoDescRequestCmd {
			return false
		}
	}
	return true
}

func Execute() {
	if err := newRootCmd(newCLI()).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
