package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"acline/internal/app"
	"acline/internal/store"
)

func newRoadmapCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "roadmap",
		Aliases: []string{"milestone"},
		Short:   "Milestones: where tasks are headed and by when",
	}
	cmd.AddCommand(newRoadmapAddCmd(c), newRoadmapListCmd(c), newRoadmapShowCmd(c), newRoadmapUpdateCmd(c))
	return cmd
}

func newRoadmapAddCmd(c *cli) *cobra.Command {
	var (
		roadmapAddDesc   string
		roadmapAddTarget string
		roadmapProject   string
	)
	cmd := &cobra.Command{
		Use:   "add <name...>",
		Short: "Add a milestone (status defaults to planned)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.Join(args, " ")
			projectID, err := c.resolveProjectFlag(roadmapProject)
			if err != nil {
				return err
			}
			id, err := c.st.AddMilestone(name, store.MilestoneOpts{
				Description: roadmapAddDesc, TargetDate: roadmapAddTarget, ProjectID: projectID,
			})
			if err != nil {
				return err
			}
			fmt.Printf("milestone #%d created: %s\n", id, name)
			return nil
		},
	}
	cmd.Flags().StringVarP(&roadmapAddDesc, "desc", "d", "", "1-2 sentence description")
	cmd.Flags().StringVar(&roadmapAddTarget, "target", "", "target date, e.g. 2026-12-01")
	cmd.Flags().StringVar(&roadmapProject, "project", "", "project name (default: resolved from cwd/ACLINE_PROJECT)")
	return cmd
}

// milestoneJSONView is the --json view of a store.Milestone, plus its
// progress (already computed per-row for the text table below, so no
// extra query cost to include it) — plain types only, so nullable columns
// serialize as a value or JSON null instead of database/sql's
// {"String":"x","Valid":true} shape.
type milestoneJSONView struct {
	ID          int64                     `json:"id"`
	Name        string                    `json:"name"`
	Description *string                   `json:"description"`
	Status      string                    `json:"status"`
	TargetDate  *string                   `json:"target_date"`
	ProjectID   *int64                    `json:"project_id"`
	CreatedAt   string                    `json:"created_at"`
	UpdatedAt   string                    `json:"updated_at"`
	Progress    milestoneProgressJSONView `json:"progress"`
}

type milestoneProgressJSONView struct {
	Total     int `json:"total"`
	Done      int `json:"done"`
	Cancelled int `json:"cancelled"`
}

func newMilestoneJSONView(m store.Milestone, p store.MilestoneProgress) milestoneJSONView {
	return milestoneJSONView{
		ID: m.ID, Name: m.Name, Description: nullStrPtr(m.Description), Status: m.Status,
		TargetDate: nullStrPtr(m.TargetDate), ProjectID: nullIntPtr(m.ProjectID),
		CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt,
		Progress: milestoneProgressJSONView{Total: p.Total, Done: p.Done, Cancelled: p.Cancelled},
	}
}

func newRoadmapListCmd(c *cli) *cobra.Command {
	var (
		roadmapProject    string
		roadmapListStatus string
		roadmapListJSON   bool
	)
	var allProjects bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List milestones with progress",
		RunE: func(cmd *cobra.Command, args []string) error {
			projectID, err := c.resolveListScope(roadmapProject, allProjects)
			if err != nil {
				return err
			}
			milestones, err := c.st.ListMilestones(roadmapListStatus, projectID)
			if err != nil {
				return err
			}
			if roadmapListJSON {
				out := make([]milestoneJSONView, len(milestones))
				for i, m := range milestones {
					progress, err := c.st.GetMilestoneProgress(m.ID)
					if err != nil {
						return err
					}
					out[i] = newMilestoneJSONView(m, progress)
				}
				return printJSON(out)
			}
			if len(milestones) == 0 {
				fmt.Println("no milestones")
				return nil
			}
			fmt.Printf("%-4s %-10s %-11s %-10s %s\n", "ID", "TARGET", "STATUS", "DONE", "NAME")
			for _, m := range milestones {
				progress, err := c.st.GetMilestoneProgress(m.ID)
				if err != nil {
					return err
				}
				target := "-"
				if m.TargetDate.Valid {
					target = m.TargetDate.String
				}
				fmt.Printf("%-4d %-10s %-11s %-10s %s\n", m.ID, target, m.Status, progressStr(progress), m.Name)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&roadmapListStatus, "status", "s", "", "filter by status: planned|active|done|cancelled")
	cmd.Flags().StringVar(&roadmapProject, "project", "", "project name (default: the current project, else every project)")
	cmd.Flags().BoolVar(&allProjects, "all-projects", false, allProjectsUsage)
	cmd.Flags().BoolVar(&roadmapListJSON, "json", false, "print results as a JSON array instead of text")
	return cmd
}

func progressStr(p store.MilestoneProgress) string {
	s := fmt.Sprintf("%d/%d", p.Done, p.Total)
	if p.Cancelled > 0 {
		s += fmt.Sprintf(" (%d cxl)", p.Cancelled)
	}
	return s
}

func newRoadmapShowCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show a milestone and its tasks",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], "milestone")
			if err != nil {
				return err
			}
			m, err := c.st.GetMilestone(id)
			if err != nil {
				return err
			}
			progress, err := c.st.GetMilestoneProgress(id)
			if err != nil {
				return err
			}
			fmt.Printf("#%d %s\n", m.ID, m.Name)
			fmt.Printf("status:   %s\n", m.Status)
			if m.TargetDate.Valid {
				fmt.Printf("target:   %s\n", m.TargetDate.String)
			}
			fmt.Printf("progress: %s\n", progressStr(progress))
			if m.Description.Valid {
				fmt.Printf("desc:     %s\n", m.Description.String)
			}
			fmt.Printf("created:  %s\n", m.CreatedAt)

			tasks, err := c.st.MilestoneTasks(id)
			if err != nil {
				return err
			}
			if len(tasks) == 0 {
				fmt.Println("\nno tasks assigned")
				return nil
			}
			fmt.Printf("\n%-4s %-11s %-7s %s\n", "ID", "STATUS", "PRIO", "TITLE")
			for _, t := range tasks {
				fmt.Printf("%-4d %-11s %-7s %s\n", t.ID, t.Status, t.Priority, t.Title)
			}
			return nil
		},
	}
	return cmd
}

func newRoadmapUpdateCmd(c *cli) *cobra.Command {
	var (
		roadmapUpdateStatus string
		roadmapUpdateTarget string
	)
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a milestone's status or target date",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], "milestone")
			if err != nil {
				return err
			}
			err = app.UpdateMilestone(c.st, id, roadmapUpdateStatus, roadmapUpdateTarget)
			if errors.Is(err, app.ErrNothingToUpdate) {
				return fmt.Errorf("nothing to update: pass --status and/or --target")
			}
			if err != nil {
				return err
			}
			fmt.Printf("milestone #%d updated\n", id)
			return nil
		},
	}
	cmd.Flags().StringVar(&roadmapUpdateStatus, "status", "", "planned|active|done|cancelled")
	cmd.Flags().StringVar(&roadmapUpdateTarget, "target", "", "target date, e.g. 2026-12-01")
	return cmd
}
