package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"acline/internal/store"
)

var sessionCmd = &cobra.Command{
	Use:   "session",
	Short: "Manage work sessions",
}

var (
	sessionStartTask       string
	sessionStartProject    string
	sessionStartPolicy     string
	sessionStartAllowTools []string
	sessionStartDenyTools  []string
	sessionStartAllowPaths []string
	sessionStartDenyPaths  []string
	sessionStartRole       string
)

var sessionStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start a work session",
	RunE: func(cmd *cobra.Command, args []string) error {
		var taskID *int64
		if sessionStartTask != "" {
			id, err := parseID(sessionStartTask, "task")
			if err != nil {
				return err
			}
			taskID = &id
		}
		// The task's own project is the fallback inside BeginSession.
		projectID, err := resolveProjectFlag(sessionStartProject)
		if err != nil {
			return err
		}
		roleID, err := resolveRoleFlag(sessionStartRole, sessionStartProject)
		if err != nil {
			return err
		}
		id, err := st.BeginSession(store.SessionStart{
			TaskID: taskID, ProjectID: projectID, RoleID: roleID,
			Policy: store.Policy{
				Label:      sessionStartPolicy,
				AllowTools: sessionStartAllowTools,
				DenyTools:  sessionStartDenyTools,
				AllowPaths: sessionStartAllowPaths,
				DenyPaths:  sessionStartDenyPaths,
			},
		})
		if errors.Is(err, store.ErrSessionActive) {
			return fmt.Errorf("a session is already active; end it first with 'acline session end'")
		}
		if err != nil {
			return err
		}
		if projectID == nil && taskID != nil {
			if t, terr := st.GetTask(*taskID); terr == nil && t.ProjectID.Valid {
				projectID = &t.ProjectID.Int64
			}
		}
		fmt.Printf("session #%d started (actor: %s/%s)", id, st.Actor.Type, st.Actor.ID)
		if projectID != nil {
			fmt.Printf(", project #%d", *projectID)
		}
		fmt.Println()
		if taskID != nil {
			printRecall(*taskID)
		}
		return nil
	},
}

// printRecall lists approved pitfall/failure_pattern memory relevant to the
// task so the lessons reach the agent at the moment work starts. Best-effort:
// a recall error must not fail a session that already began.
func printRecall(taskID int64) {
	entries, err := st.RecallForTask(taskID)
	if err != nil || len(entries) == 0 {
		return
	}
	fmt.Printf("\nrelevant lessons (%d):\n", len(entries))
	for _, m := range entries {
		fmt.Printf("  #%d [%s] %s\n", m.ID, m.Kind, m.Body)
	}
}

var (
	sessionEndSummary   string
	sessionEndTokensIn  int64
	sessionEndTokensOut int64
	sessionEndCost      float64
)

var sessionEndCmd = &cobra.Command{
	Use:   "end",
	Short: "End the active work session",
	RunE: func(cmd *cobra.Command, args []string) error {
		var sess *store.Session
		err := withApprovalToken("ending a policy-restricted session", func(token string) error {
			var eerr error
			sess, eerr = st.EndSessionWithToken(sessionEndSummary, store.SessionCost{
				TokensIn: sessionEndTokensIn, TokensOut: sessionEndTokensOut, CostUSD: sessionEndCost,
			}, token)
			return eerr
		})
		if err != nil {
			if errors.Is(err, store.ErrNoActiveSession) {
				return fmt.Errorf("no active session")
			}
			return err
		}
		fmt.Printf("session #%d ended\n", sess.ID)

		// End-of-session ritual: surface the memory review queue.
		pending, err := st.CountPendingMemory()
		if err != nil {
			return err
		}
		if pending > 0 {
			fmt.Printf("%d memory entr%s pending review — run: acline memory review\n",
				pending, plural(pending, "y", "ies"))
		}

		// This project has opted into the JSON-as-source-of-truth workflow
		// (acline.json exists) — remind that the cache has moved ahead of it.
		if _, err := os.Stat(defaultSnapshotPath); err == nil {
			fmt.Printf("run: acline snapshot export   (keep %s in sync before committing)\n", defaultSnapshotPath)
		}
		return nil
	},
}

var sessionCurrentCmd = &cobra.Command{
	Use:   "current",
	Short: "Show the active session",
	RunE: func(cmd *cobra.Command, args []string) error {
		sess, err := st.CurrentSession()
		if err != nil {
			if errors.Is(err, store.ErrNoActiveSession) {
				fmt.Println("no active session")
				return nil
			}
			return err
		}
		fmt.Printf("session #%d, started %s", sess.ID, sess.StartedAt)
		if sess.TaskID.Valid {
			fmt.Printf(", task #%d", sess.TaskID.Int64)
		}
		if sess.ProjectID.Valid {
			fmt.Printf(", project #%d", sess.ProjectID.Int64)
		}
		if sess.ActorID.Valid {
			fmt.Printf(", actor %s/%s", sess.ActorType.String, sess.ActorID.String)
		}
		fmt.Println()
		if sess.Policy.Valid && sess.Policy.String != "" {
			p := store.ParsePolicy(sess.Policy.String)
			if len(p.AllowTools)+len(p.DenyTools)+len(p.AllowPaths)+len(p.DenyPaths) > 0 {
				fmt.Println("policy restrictions apply — see: acline policy show")
			}
		}
		return nil
	},
}

var sessionListProject string

var sessionListCmd = &cobra.Command{
	Use:   "list",
	Short: "List recent sessions",
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, err := resolveProjectFlagOptional(sessionListProject)
		if err != nil {
			return err
		}
		sessions, err := st.ListSessions(projectID, 20)
		if err != nil {
			return err
		}
		if len(sessions) == 0 {
			fmt.Println("no sessions")
			return nil
		}
		fmt.Printf("%-4s %-4s %-22s %-8s %-14s %s\n", "ID", "PROJ", "STARTED", "ACTOR", "MODEL", "SUMMARY")
		for _, s := range sessions {
			proj := "-"
			if s.ProjectID.Valid {
				proj = fmt.Sprintf("%d", s.ProjectID.Int64)
			}
			fmt.Printf("%-4d %-4s %-22s %-8s %-14s %s\n",
				s.ID, proj, s.StartedAt, s.ActorType.String, s.Model.String, s.Summary.String)
		}
		return nil
	},
}

func init() {
	sessionStartCmd.Flags().StringVarP(&sessionStartTask, "task", "t", "", "task id to associate with this session")
	sessionStartCmd.Flags().StringVar(&sessionStartProject, "project", "", "project name (default: resolved from cwd/ACLINE_PROJECT, then --task's own project)")
	sessionStartCmd.Flags().StringVar(&sessionStartPolicy, "policy", "", "free-text label for what this session is permitted to touch")
	sessionStartCmd.Flags().StringSliceVar(&sessionStartAllowTools, "allow-tools", nil, "only these tools are permitted (comma-separated, '*' for any)")
	sessionStartCmd.Flags().StringSliceVar(&sessionStartDenyTools, "deny-tools", nil, "these tools are explicitly denied")
	sessionStartCmd.Flags().StringSliceVar(&sessionStartAllowPaths, "allow-paths", nil, "only paths matching these globs are permitted")
	sessionStartCmd.Flags().StringSliceVar(&sessionStartDenyPaths, "deny-paths", nil, "paths matching these globs are explicitly denied")
	sessionStartCmd.Flags().StringVar(&sessionStartRole, "role", "", "role name (default: $ACLINE_ROLE); persists as this session's role")
	sessionEndCmd.Flags().StringVarP(&sessionEndSummary, "summary", "m", "", "summary of what was done")
	sessionEndCmd.Flags().Int64Var(&sessionEndTokensIn, "tokens-in", 0, "input tokens consumed this session")
	sessionEndCmd.Flags().Int64Var(&sessionEndTokensOut, "tokens-out", 0, "output tokens produced this session")
	sessionEndCmd.Flags().Float64Var(&sessionEndCost, "cost", 0, "session cost in USD")
	sessionListCmd.Flags().StringVar(&sessionListProject, "project", "", "filter by project name")

	sessionCmd.AddCommand(sessionStartCmd, sessionEndCmd, sessionCurrentCmd, sessionListCmd)
	rootCmd.AddCommand(sessionCmd)
}
