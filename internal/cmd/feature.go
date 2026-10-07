package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"acline/internal/app"
	"acline/internal/store"
)

func newFeatureCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "feature",
		Short: "Code-derived capability catalog (what exists, what doesn't)",
	}
	cmd.AddCommand(newFeatureAddCmd(c), newFeatureListCmd(c), newFeatureUpdateCmd(c))
	return cmd
}

func newFeatureAddCmd(c *cli) *cobra.Command {
	var (
		featureStatus  string
		featureOwner   string
		featureSource  string
		featureDesc    string
		featureProject string
	)
	cmd := &cobra.Command{
		Use:   "add <name...>",
		Short: "Add or document a capability (status defaults to live)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.Join(args, " ")
			id, err := app.AddFeature(c.st, app.AddFeatureRequest{
				Name: name, Status: featureStatus, OwnerArea: featureOwner, SourcePointer: featureSource,
				Description: featureDesc, ProjectArg: featureProject, AllowCwdFallback: true,
			})
			if err != nil {
				return err
			}
			fmt.Printf("feature #%d recorded: %s\n", id, name)
			return nil
		},
	}
	cmd.Flags().StringVar(&featureStatus, "status", "live", "live|deprecated|removed|n_a")
	cmd.Flags().StringVar(&featureOwner, "owner", "", "owning area")
	cmd.Flags().StringVar(&featureSource, "source", "", "pointer to entry-point code")
	cmd.Flags().StringVarP(&featureDesc, "desc", "d", "", "1-2 sentence description")
	cmd.Flags().StringVar(&featureProject, "project", "", "project name (default: resolved from cwd/ACLINE_PROJECT)")
	return cmd
}

// featureJSONView is the --json view of a store.Feature: plain types only,
// so nullable columns serialize as a value or JSON null instead of
// database/sql's {"String":"x","Valid":true} shape.
type featureJSONView struct {
	ID            int64   `json:"id"`
	Name          string  `json:"name"`
	Status        string  `json:"status"`
	OwnerArea     *string `json:"owner_area"`
	SourcePointer *string `json:"source_pointer"`
	Description   *string `json:"description"`
	ProjectID     *int64  `json:"project_id"`
	CreatedAt     string  `json:"created_at"`
	UpdatedAt     string  `json:"updated_at"`
}

func newFeatureJSONView(f store.Feature) featureJSONView {
	return featureJSONView{
		ID: f.ID, Name: f.Name, Status: f.Status, OwnerArea: nullStrPtr(f.OwnerArea),
		SourcePointer: nullStrPtr(f.SourcePointer), Description: nullStrPtr(f.Description),
		ProjectID: nullIntPtr(f.ProjectID), CreatedAt: f.CreatedAt, UpdatedAt: f.UpdatedAt,
	}
}

func newFeatureListCmd(c *cli) *cobra.Command {
	var (
		featureProject    string
		featureListFilter string
		featureListJSON   bool
	)
	var allProjects bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List features",
		RunE: func(cmd *cobra.Command, args []string) error {
			projectID, err := c.resolveListScope(featureProject, allProjects)
			if err != nil {
				return err
			}
			features, err := c.st.ListFeatures(featureListFilter, projectID)
			if err != nil {
				return err
			}
			if featureListJSON {
				out := make([]featureJSONView, len(features))
				for i, f := range features {
					out[i] = newFeatureJSONView(f)
				}
				return printJSON(out)
			}
			if len(features) == 0 {
				fmt.Println("no features")
				return nil
			}
			fmt.Printf("%-4s %-11s %-12s %s\n", "ID", "STATUS", "OWNER", "NAME")
			for _, f := range features {
				fmt.Printf("%-4d %-11s %-12s %s\n", f.ID, f.Status, f.OwnerArea.String, f.Name)
				if f.Description.Valid {
					fmt.Printf("     %s\n", f.Description.String)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&featureListFilter, "status", "s", "", "filter by status")
	cmd.Flags().StringVar(&featureProject, "project", "", "project name (default: the current project, else every project)")
	cmd.Flags().BoolVar(&allProjects, "all-projects", false, allProjectsUsage)
	cmd.Flags().BoolVar(&featureListJSON, "json", false, "print results as a JSON array instead of text")
	return cmd
}

func newFeatureUpdateCmd(c *cli) *cobra.Command {
	var (
		updateFeatureStatus string
	)
	cmd := &cobra.Command{
		Use:   "update <id> --status <status>",
		Short: "Update a feature's status: live|deprecated|removed|n_a",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], "feature")
			if err != nil {
				return err
			}
			if updateFeatureStatus == "" {
				return fmt.Errorf("--status is required")
			}
			if err := c.st.SetFeatureStatus(id, updateFeatureStatus); err != nil {
				return err
			}
			fmt.Printf("feature #%d updated\n", id)
			return nil
		},
	}
	cmd.Flags().StringVar(&updateFeatureStatus, "status", "", "live|deprecated|removed|n_a")
	return cmd
}
