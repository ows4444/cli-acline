package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"acline/internal/redact"
	"acline/internal/store"
)

var memoryCmd = &cobra.Command{
	Use:   "memory",
	Short: "Durable, non-obvious lessons/pitfalls/constraints not derivable from code",
}

var (
	memoryArea    string
	memoryKind    string
	memoryProject string
)

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

var memoryAddCmd = &cobra.Command{
	Use:   "add <body...>",
	Short: "Record a durable memory entry (agent-written entries need review)",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		body := strings.Join(args, " ")
		var secretFound bool
		if redactedBody, found := redact.Secrets(body); found {
			body = redactedBody
			secretFound = true
		}
		projectID, err := resolveProjectFlag(memoryProject)
		if err != nil {
			return err
		}
		similar, _ := st.SimilarMemory(body, projectID)
		id, err := st.AddMemory(memoryArea, memoryKind, body, store.MemoryOpts{ProjectID: projectID})
		if err != nil {
			return err
		}
		if similar != nil {
			fmt.Printf("note: memory #%d already says something similar: %s\n", similar.ID, similar.Body)
		}
		logEventGlobal("memory_recorded", fmt.Sprintf("memory #%d recorded (%s)", id, memoryKind))
		if secretFound {
			logEventGlobal("secret_redacted", fmt.Sprintf("memory #%d: a pasted secret value was redacted before recording", id))
		}
		if st.Actor.Type == "agent" {
			fmt.Printf("memory #%d recorded (pending review)\n", id)
		} else {
			fmt.Printf("memory #%d recorded\n", id)
		}
		return nil
	},
}

var (
	memoryIncludeStale bool
	memoryStatusFilter string
	memoryJSON         bool
)

var memoryListCmd = &cobra.Command{
	Use:   "list",
	Short: "List memory entries",
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, err := resolveProjectFlagOptional(memoryProject)
		if err != nil {
			return err
		}
		entries, err := st.ListMemory(store.MemoryFilter{
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

var memoryReviewCmd = &cobra.Command{
	Use:   "review",
	Short: "Show memory entries awaiting review (drain this at session end)",
	RunE: func(cmd *cobra.Command, args []string) error {
		entries, err := st.ListMemory(store.MemoryFilter{Status: "pending"})
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

// reviewMemory adapts store.ReviewMemory to runStatusTransition's setter shape.
func reviewMemory(id int64, status string) error {
	return withApprovalToken(fmt.Sprintf("memory #%d", id), func(token string) error {
		return st.ReviewMemory(id, status == "approved", token)
	})
}

var memoryApproveCmd = &cobra.Command{
	Use:   "approve <id>",
	Short: "Approve a pending memory entry",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runStatusTransition(args, "memory", reviewMemory, "approved", "", "approved")
	},
}

var memoryRejectCmd = &cobra.Command{
	Use:   "reject <id>",
	Short: "Reject a pending memory entry",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runStatusTransition(args, "memory", reviewMemory, "rejected", "", "rejected")
	},
}

var memoryForgetCmd = &cobra.Command{
	Use:   "forget <id>",
	Short: "Mark a memory entry stale (excluded from default list)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := parseID(args[0], "memory")
		if err != nil {
			return err
		}
		if err := st.SetMemoryStale(id, true); err != nil {
			return err
		}
		fmt.Printf("memory #%d marked stale\n", id)
		return nil
	},
}

var memoryTouchCmd = &cobra.Command{
	Use:   "touch <id>",
	Short: "Reconfirm a memory entry is still true, resetting its decay clock",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := parseID(args[0], "memory")
		if err != nil {
			return err
		}
		if err := st.TouchMemory(id); err != nil {
			return err
		}
		fmt.Printf("memory #%d reconfirmed\n", id)
		return nil
	},
}

// defaultMemoryDecayDays is how long an approved memory entry can go
// without being reconfirmed (see TouchMemory) before `acline memory decay`
// and the dashboard start surfacing it. 90 days: long enough that routine
// project work doesn't touch every entry, short enough that a lesson from
// a project that's since changed shape gets a second look at least a
// couple of times a year.
const defaultMemoryDecayDays = store.DefaultMemoryDecayDays

var (
	memoryDecayDays    int
	memoryDecayProject string
)

var memoryDecayCmd = &cobra.Command{
	Use:   "decay",
	Short: "List approved memory entries not reconfirmed in --days days (default 90)",
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, err := resolveProjectFlagOptional(memoryDecayProject)
		if err != nil {
			return err
		}
		entries, err := st.DecayCandidates(memoryDecayDays, projectID)
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

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func init() {
	memoryAddCmd.Flags().StringVar(&memoryArea, "area", "", "ownership area this memory applies to")
	memoryAddCmd.Flags().StringVar(&memoryKind, "kind", "lesson", "constraint|lesson|pitfall|operational|failure_pattern")
	memoryAddCmd.Flags().StringVar(&memoryProject, "project", "", "project name (default: resolved from cwd/ACLINE_PROJECT)")

	memoryListCmd.Flags().BoolVar(&memoryIncludeStale, "all", false, "include entries marked stale")
	memoryListCmd.Flags().StringVarP(&memoryStatusFilter, "status", "s", "", "pending|approved|rejected")
	memoryListCmd.Flags().StringVar(&memoryProject, "project", "", "filter by project name")
	memoryListCmd.Flags().BoolVar(&memoryJSON, "json", false, "print results as a JSON array instead of text")

	memoryDecayCmd.Flags().IntVar(&memoryDecayDays, "days", defaultMemoryDecayDays, "minimum days since last reconfirmation")
	memoryDecayCmd.Flags().StringVar(&memoryDecayProject, "project", "", "filter by project name")

	memoryCmd.AddCommand(memoryAddCmd, memoryListCmd, memoryReviewCmd, memoryApproveCmd, memoryRejectCmd, memoryForgetCmd, memoryTouchCmd, memoryDecayCmd)
	rootCmd.AddCommand(memoryCmd)
}
