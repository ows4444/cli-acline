package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"acline/internal/store"
)

var featureCmd = &cobra.Command{
	Use:   "feature",
	Short: "Code-derived capability catalog (what exists, what doesn't)",
}

var (
	featureStatus  string
	featureOwner   string
	featureSource  string
	featureDesc    string
	featureProject string
)

var featureAddCmd = &cobra.Command{
	Use:   "add <name...>",
	Short: "Add or document a capability (status defaults to live)",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := strings.Join(args, " ")
		projectID, err := resolveProjectFlag(featureProject)
		if err != nil {
			return err
		}
		id, err := st.AddFeature(name, featureStatus, store.FeatureOpts{
			OwnerArea: featureOwner, SourcePointer: featureSource, Description: featureDesc, ProjectID: projectID,
		})
		if err != nil {
			return err
		}
		fmt.Printf("feature #%d recorded: %s\n", id, name)
		return nil
	},
}

var (
	featureListFilter string
	featureListJSON   bool
)

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

var featureListCmd = &cobra.Command{
	Use:   "list",
	Short: "List features",
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, err := resolveProjectFlagOptional(featureProject)
		if err != nil {
			return err
		}
		features, err := st.ListFeatures(featureListFilter, projectID)
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

var updateFeatureStatus string

var featureUpdateCmd = &cobra.Command{
	Use:   "update <id> --status <status>",
	Short: "Update a feature's status: live|deprecated|removed|n_a",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid feature id: %w", err)
		}
		if updateFeatureStatus == "" {
			return fmt.Errorf("--status is required")
		}
		if err := st.SetFeatureStatus(id, updateFeatureStatus); err != nil {
			return err
		}
		logEventGlobal("feature_status_change", fmt.Sprintf("feature #%d -> %s", id, updateFeatureStatus))
		fmt.Printf("feature #%d updated\n", id)
		return nil
	},
}

func init() {
	featureAddCmd.Flags().StringVar(&featureStatus, "status", "live", "live|deprecated|removed|n_a")
	featureAddCmd.Flags().StringVar(&featureOwner, "owner", "", "owning area")
	featureAddCmd.Flags().StringVar(&featureSource, "source", "", "pointer to entry-point code")
	featureAddCmd.Flags().StringVarP(&featureDesc, "desc", "d", "", "1-2 sentence description")

	featureListCmd.Flags().StringVarP(&featureListFilter, "status", "s", "", "filter by status")
	featureListCmd.Flags().StringVar(&featureProject, "project", "", "filter by project name")
	featureListCmd.Flags().BoolVar(&featureListJSON, "json", false, "print results as a JSON array instead of text")
	featureAddCmd.Flags().StringVar(&featureProject, "project", "", "project name (default: resolved from cwd/ACLINE_PROJECT)")

	featureUpdateCmd.Flags().StringVar(&updateFeatureStatus, "status", "", "live|deprecated|removed|n_a")

	featureCmd.AddCommand(featureAddCmd, featureListCmd, featureUpdateCmd)
	rootCmd.AddCommand(featureCmd)
}
