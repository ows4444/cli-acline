package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"acline/internal/embed"
	"acline/internal/store"
)

var (
	dbPath string
	st     *store.Store
)

var rootCmd = &cobra.Command{
	Use:   "acline",
	Short: "Local SDLC tracker: specs, tasks, verification, and an append-only audit trail in SQLite",
	// Errors are printed once by Execute; a failed command is not a usage problem.
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if !needsStore(cmd) {
			return nil
		}
		path := dbPath
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
		if c, ok := embed.FromEnv(); ok {
			s.Embedder = c
		}
		st = s
		return nil
	},
	PersistentPostRunE: func(cmd *cobra.Command, args []string) error {
		if st != nil {
			return st.Close()
		}
		return nil
	},
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
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&dbPath, "db", "", "path to sqlite db (default: ~/.acline/store.db, shared across all tracked projects; override via $ACLINE_DB)")
}
