package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"acline/internal/redact"
	"acline/internal/store"
)

var specCmd = &cobra.Command{
	Use:   "spec",
	Short: "Manage specs — the versioned intent tasks derive from",
}

var (
	specBody     string
	specBodyFile string
	specProject  string
)

// readSpecBodyFile reads --body-file's target: a path, or stdin when it is "-"
// (the same convention `acline plan propose --file -` uses), so an agent can
// submit multi-line text without writing a scratch file to disk.
func readSpecBodyFile(path string) (string, error) {
	if path == "-" {
		b, err := io.ReadAll(os.Stdin)
		return string(b), err
	}
	b, err := os.ReadFile(path)
	return string(b), err
}

var specAddCmd = &cobra.Command{
	Use:   "add <title...>",
	Short: "Record a new spec (status: draft)",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		title := strings.Join(args, " ")
		body := specBody
		if specBodyFile != "" {
			b, err := readSpecBodyFile(specBodyFile)
			if err != nil {
				return fmt.Errorf("reading spec body: %w", err)
			}
			body = b
		}
		secretFound := redact.Fields(&title, &body)
		projectID, err := resolveProjectFlag(specProject)
		if err != nil {
			return err
		}
		id, err := st.AddSpec(title, body, store.SpecOpts{ProjectID: projectID})
		if err != nil {
			return err
		}
		logEventGlobal("spec_recorded", fmt.Sprintf("spec #%d drafted: %s", id, title))
		if secretFound {
			logEventGlobal("secret_redacted", fmt.Sprintf("spec #%d: a pasted secret value was redacted before recording", id))
		}
		fmt.Printf("spec #%d created: %s\n", id, title)
		return nil
	},
}

var specStatusFilter string

var specListProject string
var specListJSON bool

// specJSONView is the --json view of a store.Spec: plain types only, so
// nullable columns serialize as a value or JSON null instead of
// database/sql's {"String":"x","Valid":true} shape.
type specJSONView struct {
	ID        int64   `json:"id"`
	Title     string  `json:"title"`
	Body      *string `json:"body"`
	Status    string  `json:"status"`
	Version   int     `json:"version"`
	ProjectID *int64  `json:"project_id"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`
}

func newSpecJSONView(sp store.Spec) specJSONView {
	return specJSONView{
		ID: sp.ID, Title: sp.Title, Body: nullStrPtr(sp.Body), Status: sp.Status, Version: sp.Version,
		ProjectID: nullIntPtr(sp.ProjectID), CreatedAt: sp.CreatedAt, UpdatedAt: sp.UpdatedAt,
	}
}

var specListCmd = &cobra.Command{
	Use:   "list",
	Short: "List specs",
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, err := resolveProjectFlagOptional(specListProject)
		if err != nil {
			return err
		}
		specs, err := st.ListSpecs(specStatusFilter, projectID)
		if err != nil {
			return err
		}
		if specListJSON {
			out := make([]specJSONView, len(specs))
			for i, sp := range specs {
				out[i] = newSpecJSONView(sp)
			}
			return printJSON(out)
		}
		if len(specs) == 0 {
			fmt.Println("no specs")
			return nil
		}
		fmt.Printf("%-4s %-12s %-4s %s\n", "ID", "STATUS", "VER", "TITLE")
		for _, sp := range specs {
			fmt.Printf("%-4d %-12s v%-3d %s\n", sp.ID, sp.Status, sp.Version, sp.Title)
		}
		return nil
	},
}

var specShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show a spec and the tasks derived from it",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := parseID(args[0], "spec")
		if err != nil {
			return err
		}
		sp, err := st.GetSpec(id)
		if err != nil {
			return err
		}
		versions, err := st.ListSpecVersions(id)
		if err != nil {
			return err
		}
		if specShowVersion > 0 && specShowVersion != sp.Version {
			for _, v := range versions {
				if v.Version == specShowVersion {
					fmt.Printf("#%d %s (v%d, replaced %s while %s)\n\n%s\n", sp.ID, v.Title, v.Version, v.CreatedAt, v.Status, v.Body)
					return nil
				}
			}
			return fmt.Errorf("spec #%d has no version %d (current is v%d)", id, specShowVersion, sp.Version)
		}
		fmt.Printf("#%d %s (v%d)\n", sp.ID, sp.Title, sp.Version)
		fmt.Printf("status: %s\n", sp.Status)
		if sp.ActorType.Valid {
			fmt.Printf("author: %s / %s\n", sp.ActorType.String, sp.ActorID.String)
		}
		if sp.Body.Valid {
			fmt.Printf("\n%s\n", sp.Body.String)
		}
		if len(versions) > 0 {
			fmt.Printf("\nearlier versions (show one with --version N):\n")
			for _, v := range versions {
				fmt.Printf("  v%-3d %-11s replaced %s by %s\n", v.Version, v.Status, v.CreatedAt, v.ActorID)
			}
		}

		tasks, err := st.TasksForSpec(id)
		if err != nil {
			return err
		}
		if len(tasks) > 0 {
			fmt.Printf("\nderived tasks (%d):\n", len(tasks))
			for _, t := range tasks {
				fmt.Printf("  #%-4d [%-11s] %s\n", t.ID, t.Status, t.Title)
			}
		}
		return nil
	},
}

var specApproveCmd = &cobra.Command{
	Use:   "approve <id>",
	Short: "Mark a spec approved (ready to derive tasks or a plan from) — a person's decision",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := parseID(args[0], "spec")
		if err != nil {
			return err
		}
		err = withApprovalToken(fmt.Sprintf("approving spec #%d", id), func(token string) error {
			return st.ApproveSpec(id, token)
		})
		if err != nil {
			return err
		}
		fmt.Printf("spec #%d approved\n", id)
		return nil
	},
}

var specReviseCmd = &cobra.Command{
	Use:   "revise <id>",
	Short: "Replace a spec's body and bump its version",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := parseID(args[0], "spec")
		if err != nil {
			return err
		}
		body := specBody
		if specBodyFile != "" {
			b, err := readSpecBodyFile(specBodyFile)
			if err != nil {
				return fmt.Errorf("reading spec body: %w", err)
			}
			body = b
		}
		if body == "" {
			return fmt.Errorf("pass --body or --body-file")
		}
		secretFound := redact.Fields(&body)
		before, err := st.GetSpec(id)
		if err != nil {
			return err
		}
		version, err := st.ReviseSpec(id, body)
		if err != nil {
			return err
		}
		if secretFound {
			logEventGlobal("secret_redacted", fmt.Sprintf("spec #%d: a pasted secret value was redacted before recording", id))
		}
		fmt.Printf("spec #%d revised to v%d\n", id, version)
		if before.Status == "approved" || before.Status == "implemented" {
			fmt.Printf("its approval was withdrawn (the text changed): it is a draft again — re-approve with: acline spec approve %d\n", id)
		}
		return nil
	},
}

var specShowVersion int

func init() {
	specShowCmd.Flags().IntVar(&specShowVersion, "version", 0, "show an earlier version's text instead of the current one")
	specAddCmd.Flags().StringVar(&specBody, "body", "", "spec text")
	specAddCmd.Flags().StringVar(&specBodyFile, "body-file", "", "read spec text from a file, or - for stdin")
	specAddCmd.Flags().StringVar(&specProject, "project", "", "project name (default: resolved from cwd/ACLINE_PROJECT)")

	specReviseCmd.Flags().StringVar(&specBody, "body", "", "replacement spec text")
	specReviseCmd.Flags().StringVar(&specBodyFile, "body-file", "", "read replacement text from a file, or - for stdin")

	specListCmd.Flags().StringVarP(&specStatusFilter, "status", "s", "", "draft|approved|implemented|superseded")
	specListCmd.Flags().StringVar(&specListProject, "project", "", "filter by project name")
	specListCmd.Flags().BoolVar(&specListJSON, "json", false, "print results as a JSON array instead of text")

	specCmd.AddCommand(specAddCmd, specListCmd, specShowCmd, specApproveCmd, specReviseCmd)
	rootCmd.AddCommand(specCmd)
}
