package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"acline/internal/app"
	"acline/internal/store"
)

func newSpecCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "spec",
		Short: "Manage specs — the versioned intent tasks derive from",
	}
	cmd.AddCommand(newSpecAddCmd(c), newSpecListCmd(c), newSpecShowCmd(c), newSpecApproveCmd(c), newSpecReviseCmd(c), newSpecSupersedeCmd(c))
	return cmd
}

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

func newSpecAddCmd(c *cli) *cobra.Command {
	var (
		specBody     string
		specBodyFile string
		specProject  string
	)
	cmd := &cobra.Command{
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
			res, err := app.AddSpec(c.st, app.AddSpecRequest{Title: title, Body: body, ProjectArg: specProject, AllowCwdFallback: true})
			if err != nil {
				return err
			}
			if res.Redacted {
				fmt.Println("note: a pasted secret value was redacted before recording")
			}
			fmt.Printf("spec #%d created: %s\n", res.ID, title)
			return nil
		},
	}
	cmd.Flags().StringVar(&specBody, "body", "", "spec text")
	cmd.Flags().StringVar(&specBodyFile, "body-file", "", "read spec text from a file, or - for stdin")
	cmd.Flags().StringVar(&specProject, "project", "", "project name (default: resolved from cwd/ACLINE_PROJECT)")
	return cmd
}

// specJSONView is the --json view of a store.Spec: plain types only, so
// nullable columns serialize as a value or JSON null instead of
// database/sql's {"String":"x","Valid":true} shape.
type specJSONView struct {
	ID           int64   `json:"id"`
	Title        string  `json:"title"`
	Body         *string `json:"body"`
	Status       string  `json:"status"`
	Version      int     `json:"version"`
	SupersededBy *int64  `json:"superseded_by"`
	ProjectID    *int64  `json:"project_id"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
}

func newSpecJSONView(sp store.Spec) specJSONView {
	return specJSONView{
		ID: sp.ID, Title: sp.Title, Body: nullStrPtr(sp.Body), Status: sp.Status, Version: sp.Version,
		SupersededBy: nullIntPtr(sp.SupersededBy), ProjectID: nullIntPtr(sp.ProjectID), CreatedAt: sp.CreatedAt, UpdatedAt: sp.UpdatedAt,
	}
}

func newSpecListCmd(c *cli) *cobra.Command {
	var (
		specStatusFilter string
		specListProject  string
		specListJSON     bool
	)
	var allProjects bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List specs",
		RunE: func(cmd *cobra.Command, args []string) error {
			projectID, err := c.resolveListScope(specListProject, allProjects)
			if err != nil {
				return err
			}
			specs, err := c.st.ListSpecs(specStatusFilter, projectID)
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
	cmd.Flags().StringVarP(&specStatusFilter, "status", "s", "", "draft|approved|superseded")
	cmd.Flags().StringVar(&specListProject, "project", "", "project name (default: the current project, else every project)")
	cmd.Flags().BoolVar(&allProjects, "all-projects", false, allProjectsUsage)
	cmd.Flags().BoolVar(&specListJSON, "json", false, "print results as a JSON array instead of text")
	return cmd
}

func newSpecShowCmd(c *cli) *cobra.Command {
	var (
		specShowVersion int
	)
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show a spec and the tasks derived from it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], "spec")
			if err != nil {
				return err
			}
			sp, err := c.st.GetSpec(id)
			if err != nil {
				return err
			}
			versions, err := c.st.ListSpecVersions(id)
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
			if sp.SupersededBy.Valid {
				fmt.Printf("superseded by: #%d\n", sp.SupersededBy.Int64)
			}
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

			tasks, err := c.st.TasksForSpec(id)
			if err != nil {
				return err
			}
			if len(tasks) > 0 {
				fmt.Printf("\nderived tasks (%d):\n", len(tasks))
				for _, t := range tasks {
					fmt.Printf("  #%-4d [%-11s] %s\n", t.ID, t.Status, t.Title)
				}
				implemented, err := c.st.SpecImplemented(id)
				if err != nil {
					return err
				}
				if implemented {
					fmt.Println("implemented: every derived task is done")
				}
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&specShowVersion, "version", 0, "show an earlier version's text instead of the current one")
	return cmd
}

func newSpecApproveCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "approve <id>",
		Short: "Mark a spec approved (ready to derive tasks or a plan from) — a person's decision",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], "spec")
			if err != nil {
				return err
			}
			err = c.withApprovalToken(fmt.Sprintf("approving spec #%d", id), func(token string) error {
				return c.st.ApproveSpec(id, token)
			})
			if err != nil {
				return err
			}
			fmt.Printf("spec #%d approved\n", id)
			return nil
		},
	}
	return cmd
}

func newSpecReviseCmd(c *cli) *cobra.Command {
	var (
		specBody     string
		specBodyFile string
	)
	cmd := &cobra.Command{
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
			res, err := app.ReviseSpec(c.st, id, body)
			if errors.Is(err, app.ErrBodyRequired) {
				return fmt.Errorf("pass --body or --body-file")
			}
			if err != nil {
				return err
			}
			if res.Redacted {
				fmt.Println("note: a pasted secret value was redacted before recording")
			}
			fmt.Printf("spec #%d revised to v%d\n", id, res.Version)
			if res.ApprovalWithdrawn {
				fmt.Printf("its approval was withdrawn (the text changed): it is a draft again — re-approve with: acline spec approve %d\n", id)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&specBody, "body", "", "replacement spec text")
	cmd.Flags().StringVar(&specBodyFile, "body-file", "", "read replacement text from a file, or - for stdin")
	return cmd
}

func newSpecSupersedeCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "supersede <old-id> <new-id>",
		Short: "Mark old-id superseded by new-id (a person's decision when old-id is approved)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			oldID, err := parseID(args[0], "spec")
			if err != nil {
				return err
			}
			newID, err := parseID(args[1], "spec")
			if err != nil {
				return err
			}
			err = c.withApprovalToken(fmt.Sprintf("superseding spec #%d", oldID), func(token string) error {
				return c.st.SupersedeSpec(oldID, newID, token)
			})
			if err != nil {
				return err
			}
			fmt.Printf("spec #%d superseded by #%d\n", oldID, newID)
			return nil
		},
	}
	return cmd
}
