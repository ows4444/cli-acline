package cmd

import (
	"fmt"
	"os"
	"sort"

	"github.com/spf13/cobra"
)

// defaultSnapshotPath is where `acline init` looks for a cache seed and where
// `acline snapshot export` writes by default. Note: the database is shared
// across every tracked project (see store.DefaultPath), so this dumps the
// *entire* store, not just one project — it's a full-store backup format,
// not a per-repo git-commit target the way it was before project scoping.
const defaultSnapshotPath = "acline.json"

var snapshotCmd = &cobra.Command{
	Use:   "snapshot",
	Short: "Export/import the full database (every tracked project) as one JSON backup file",
}

var snapshotExportPath string

var snapshotExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Write the full database (every tracked project) to a JSON backup file",
	RunE: func(cmd *cobra.Command, args []string) error {
		// The whole store (every project's specs, decisions, audit trail): as
		// private as the store itself.
		f, err := createPrivate(snapshotExportPath)
		if err != nil {
			return fmt.Errorf("creating %s: %w", snapshotExportPath, err)
		}
		if err := st.SnapshotJSON(f); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("writing %s: %w", snapshotExportPath, err)
		}
		fmt.Printf("wrote %s\n", snapshotExportPath)
		return nil
	},
}

var snapshotImportPath string

var snapshotImportCmd = &cobra.Command{
	Use:   "import",
	Short: "Load a JSON snapshot into the current database (idempotent: existing rows are left alone)",
	RunE: func(cmd *cobra.Command, args []string) error {
		f, err := os.Open(snapshotImportPath)
		if err != nil {
			return fmt.Errorf("opening %s: %w", snapshotImportPath, err)
		}
		defer f.Close()
		var counts map[string]int
		err = withApprovalToken("importing a snapshot into a store that already has data", func(token string) error {
			var lerr error
			counts, lerr = st.LoadSnapshotWithToken(f, token)
			return lerr
		})
		if err != nil {
			return err
		}
		printImportSummary(snapshotImportPath, counts)
		return nil
	},
}

func printImportSummary(path string, counts map[string]int) {
	total := 0
	tables := make([]string, 0, len(counts))
	for t, n := range counts {
		total += n
		tables = append(tables, t)
	}
	if total == 0 {
		fmt.Printf("loaded %s: nothing new (already in sync)\n", path)
		return
	}
	sort.Strings(tables)
	fmt.Printf("loaded %s: %d row(s) added\n", path, total)
	for _, t := range tables {
		fmt.Printf("  %-14s %d\n", t, counts[t])
	}
}

func init() {
	snapshotExportCmd.Flags().StringVarP(&snapshotExportPath, "out", "o", defaultSnapshotPath, "output path")
	snapshotImportCmd.Flags().StringVarP(&snapshotImportPath, "in", "i", defaultSnapshotPath, "input path")

	snapshotCmd.AddCommand(snapshotExportCmd, snapshotImportCmd)
	rootCmd.AddCommand(snapshotCmd)
}

// createPrivate creates (or truncates) path readable only by the user, also
// when the file already existed with a wider mode.
func createPrivate(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
