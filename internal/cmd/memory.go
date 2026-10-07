package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"acline/internal/app"
	"acline/internal/store"
)

func newMemoryCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "memory",
		Short: "Durable, non-obvious lessons/pitfalls/constraints not derivable from code",
	}
	cmd.AddCommand(newMemoryAddCmd(c), newMemoryListCmd(c), newMemoryReviewCmd(c), newMemoryApproveCmd(c), newMemoryRejectCmd(c), newMemoryForgetCmd(c), newMemoryRestoreCmd(c), newMemoryTouchCmd(c), newMemoryDecayCmd(c))
	return cmd
}

// memoryJSONView is the --json view of a store.MemoryEntry: plain types
// only, so nullable columns serialize as a value or JSON null instead of
// database/sql's {"String":"x","Valid":true} shape.
type memoryJSONView struct {
	ID         int64   `json:"id"`
	Area       *string `json:"area"`
	Kind       string  `json:"kind"`
	Body       string  `json:"body"`
	Status     string  `json:"status"`
	Stale      bool    `json:"stale"`
	ProjectID  *int64  `json:"project_id"`
	ActorID    *string `json:"actor_id"`
	ReviewedAt *string `json:"reviewed_at"`
	SourceKind *string `json:"source_kind"`
	SourceID   *int64  `json:"source_id"`
	CreatedAt  string  `json:"created_at"`
}

func newMemoryJSONView(m store.MemoryEntry) memoryJSONView {
	return memoryJSONView{
		ID: m.ID, Area: nullStrPtr(m.Area), Kind: m.Kind, Body: m.Body, Status: m.Status, Stale: m.Stale,
		ProjectID: nullIntPtr(m.ProjectID), ActorID: nullStrPtr(m.ActorID), ReviewedAt: nullStrPtr(m.ReviewedAt),
		SourceKind: nullStrPtr(m.SourceKind), SourceID: nullIntPtr(m.SourceID), CreatedAt: m.CreatedAt,
	}
}

func newMemoryAddCmd(c *cli) *cobra.Command {
	var (
		memoryArea    string
		memoryKind    string
		memoryProject string
	)
	cmd := &cobra.Command{
		Use:   "add <body...>",
		Short: "Record a durable memory entry (agent-written entries need review)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body := strings.Join(args, " ")
			res, err := app.AddMemory(c.st, app.AddMemoryRequest{
				Area: memoryArea, Kind: memoryKind, Body: body, ProjectArg: memoryProject, AllowCwdFallback: true,
			})
			if err != nil {
				return err
			}
			if res.Similar != nil {
				fmt.Printf("note: memory #%d already says something similar: %s\n", res.Similar.ID, res.Similar.Body)
			}
			if res.Redacted {
				fmt.Println("note: a pasted secret value was redacted before recording")
			}
			if res.Pending {
				fmt.Printf("memory #%d recorded (pending review)\n", res.ID)
			} else {
				fmt.Printf("memory #%d recorded\n", res.ID)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&memoryArea, "area", "", "ownership area this memory applies to")
	cmd.Flags().StringVar(&memoryKind, "kind", "lesson", "constraint|lesson|pitfall|operational|failure_pattern")
	cmd.Flags().StringVar(&memoryProject, "project", "", "project name (default: resolved from cwd/ACLINE_PROJECT)")
	return cmd
}

func newMemoryListCmd(c *cli) *cobra.Command {
	var (
		memoryProject      string
		memoryIncludeStale bool
		memoryStatusFilter string
		memoryJSON         bool
	)
	var allProjects bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List memory entries",
		RunE: func(cmd *cobra.Command, args []string) error {
			projectID, err := c.resolveListScope(memoryProject, allProjects)
			if err != nil {
				return err
			}
			entries, err := c.st.ListMemory(store.MemoryFilter{
				Status: memoryStatusFilter, IncludeStale: memoryIncludeStale, ProjectID: projectID,
			})
			if err != nil {
				return err
			}
			if memoryJSON {
				out := make([]memoryJSONView, len(entries))
				for i, m := range entries {
					out[i] = newMemoryJSONView(m)
				}
				return printJSON(out)
			}
			if len(entries) == 0 {
				fmt.Println("no memory entries")
				return nil
			}
			for _, m := range entries {
				stale := ""
				if m.Stale {
					stale = " [stale]"
				}
				areaPart := ""
				if m.Area.Valid {
					areaPart = " (" + m.Area.String + ")"
				}
				status := ""
				if m.Status != "approved" {
					status = " {" + m.Status + "}"
				}
				fmt.Printf("#%d [%s]%s%s%s %s\n", m.ID, m.Kind, areaPart, status, stale, m.Body)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&memoryIncludeStale, "all", false, "include entries marked stale")
	cmd.Flags().StringVarP(&memoryStatusFilter, "status", "s", "", "pending|approved|rejected")
	cmd.Flags().StringVar(&memoryProject, "project", "", "project name (default: the current project, else every project)")
	cmd.Flags().BoolVar(&allProjects, "all-projects", false, allProjectsUsage)
	cmd.Flags().BoolVar(&memoryJSON, "json", false, "print results as a JSON array instead of text")
	return cmd
}

func newMemoryReviewCmd(c *cli) *cobra.Command {
	var (
		reviewProject string
		allProjects   bool
	)
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Show memory entries awaiting review (drain this at session end)",
		RunE: func(cmd *cobra.Command, args []string) error {
			projectID, err := c.resolveListScope(reviewProject, allProjects)
			if err != nil {
				return err
			}
			entries, err := c.st.ListMemory(store.MemoryFilter{Status: "pending", ProjectID: projectID})
			if err != nil {
				return err
			}
			if len(entries) == 0 {
				fmt.Println("nothing pending review")
				return nil
			}
			fmt.Printf("%d entr%s pending review:\n", len(entries), plural(len(entries), "y", "ies"))
			for _, m := range entries {
				by := ""
				if m.ActorID.Valid {
					by = " <" + m.ActorID.String + ">"
				}
				fmt.Printf("  #%d [%s]%s %s\n", m.ID, m.Kind, by, m.Body)
			}
			fmt.Println("\napprove with: acline memory approve <id>   reject with: acline memory reject <id>")
			return nil
		},
	}
	cmd.Flags().StringVar(&reviewProject, "project", "", "project name (default: the current project, else every project)")
	cmd.Flags().BoolVar(&allProjects, "all-projects", false, allProjectsUsage)
	return cmd
}

// reviewMemory adapts store.ReviewMemory to runStatusTransition's setter shape.
func (c *cli) reviewMemory(id int64, status string) error {
	return c.withApprovalToken(fmt.Sprintf("memory #%d", id), func(token string) error {
		return c.st.ReviewMemory(id, status == "approved", token)
	})
}

func newMemoryApproveCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "approve <id>",
		Short: "Approve a pending memory entry",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatusTransition(args, "memory", c.reviewMemory, "approved", "approved")
		},
	}
	return cmd
}

func newMemoryRejectCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reject <id>",
		Short: "Reject a pending memory entry",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatusTransition(args, "memory", c.reviewMemory, "rejected", "rejected")
		},
	}
	return cmd
}

func newMemoryForgetCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "forget <id>",
		Short: "Mark a memory entry stale, dropping it from context (a person's decision; undo with `memory restore`)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], "memory")
			if err != nil {
				return err
			}
			if err := c.withApprovalToken(fmt.Sprintf("forgetting memory #%d", id), func(token string) error {
				return c.st.SetMemoryStaleWithToken(id, true, token)
			}); err != nil {
				return err
			}
			fmt.Printf("memory #%d marked stale (restore with: acline memory restore %d)\n", id, id)
			return nil
		},
	}
	return cmd
}

func newMemoryRestoreCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "restore <id>",
		Short: "Undo `memory forget`: make a stale entry live again (a person's decision)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], "memory")
			if err != nil {
				return err
			}
			if err := c.withApprovalToken(fmt.Sprintf("restoring memory #%d", id), func(token string) error {
				return c.st.SetMemoryStaleWithToken(id, false, token)
			}); err != nil {
				return err
			}
			fmt.Printf("memory #%d restored\n", id)
			return nil
		},
	}
	return cmd
}

func newMemoryTouchCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "touch <id>",
		Short: "Reconfirm a memory entry is still true, resetting its decay clock (a person's decision)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], "memory")
			if err != nil {
				return err
			}
			if err := c.withApprovalToken(fmt.Sprintf("reconfirming memory #%d", id), func(token string) error {
				return c.st.TouchMemoryWithToken(id, token)
			}); err != nil {
				return err
			}
			fmt.Printf("memory #%d reconfirmed\n", id)
			return nil
		},
	}
	return cmd
}

// defaultMemoryDecayDays is how long an approved memory entry can go
// without being reconfirmed (see TouchMemory) before `acline memory decay`
// and the dashboard start surfacing it. 90 days: long enough that routine
// project work doesn't touch every entry, short enough that a lesson from
// a project that's since changed shape gets a second look at least a
// couple of times a year.
const defaultMemoryDecayDays = store.DefaultMemoryDecayDays

func newMemoryDecayCmd(c *cli) *cobra.Command {
	var (
		memoryDecayDays    int
		memoryDecayProject string
	)
	var allProjects bool
	cmd := &cobra.Command{
		Use:   "decay",
		Short: "List approved memory entries not reconfirmed in --days days (default 90)",
		RunE: func(cmd *cobra.Command, args []string) error {
			projectID, err := c.resolveListScope(memoryDecayProject, allProjects)
			if err != nil {
				return err
			}
			entries, err := c.st.DecayCandidates(memoryDecayDays, projectID)
			if err != nil {
				return err
			}
			if len(entries) == 0 {
				fmt.Printf("nothing older than %d days awaiting reconfirmation\n", memoryDecayDays)
				return nil
			}
			fmt.Printf("%d entr%s not reconfirmed in %d+ days:\n", len(entries), plural(len(entries), "y", "ies"), memoryDecayDays)
			for _, m := range entries {
				areaPart := ""
				if m.Area.Valid {
					areaPart = " (" + m.Area.String + ")"
				}
				last := m.CreatedAt
				if m.ReviewedAt.Valid {
					last = m.ReviewedAt.String
				}
				fmt.Printf("  #%-4d [%s]%s last confirmed %s: %s\n", m.ID, m.Kind, areaPart, last, m.Body)
			}
			fmt.Println("\nreconfirm with: acline memory touch <id>   mark stale with: acline memory forget <id>")
			return nil
		},
	}
	cmd.Flags().IntVar(&memoryDecayDays, "days", defaultMemoryDecayDays, "minimum days since last reconfirmation")
	cmd.Flags().StringVar(&memoryDecayProject, "project", "", "project name (default: the current project, else every project)")
	cmd.Flags().BoolVar(&allProjects, "all-projects", false, allProjectsUsage)
	return cmd
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
