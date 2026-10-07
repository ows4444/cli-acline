package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"acline/internal/app"
	"acline/internal/store"
)

func newSessionCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Manage work sessions",
	}
	cmd.AddCommand(newSessionStartCmd(c), newSessionEndCmd(c), newSessionCurrentCmd(c), newSessionListCmd(c))
	return cmd
}

func newSessionStartCmd(c *cli) *cobra.Command {
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
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start a work session",
		RunE: func(cmd *cobra.Command, args []string) error {
			taskID, err := parseOptionalID(sessionStartTask, "task")
			if err != nil {
				return err
			}
			res, err := app.StartSession(c.st, app.StartSessionRequest{
				TaskID: taskID, ProjectArg: sessionStartProject, RoleArg: sessionStartRole, AllowCwdFallback: true,
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
			fmt.Printf("session #%d started (actor: %s/%s)", res.ID, c.st.Actor.Type, c.st.Actor.ID)
			if res.ProjectID != nil {
				fmt.Printf(", project #%d", *res.ProjectID)
			}
			fmt.Println()
			printRecall(res.Lessons)
			return nil
		},
	}
	cmd.Flags().StringVarP(&sessionStartTask, "task", "t", "", "task id to associate with this session")
	cmd.Flags().StringVar(&sessionStartProject, "project", "", "project name (default: resolved from cwd/ACLINE_PROJECT, then --task's own project)")
	cmd.Flags().StringVar(&sessionStartPolicy, "policy", "", "free-text label for what this session is permitted to touch")
	cmd.Flags().StringSliceVar(&sessionStartAllowTools, "allow-tools", nil, "only these tools are permitted (comma-separated, '*' for any)")
	cmd.Flags().StringSliceVar(&sessionStartDenyTools, "deny-tools", nil, "these tools are explicitly denied")
	cmd.Flags().StringSliceVar(&sessionStartAllowPaths, "allow-paths", nil, "only paths matching these globs are permitted")
	cmd.Flags().StringSliceVar(&sessionStartDenyPaths, "deny-paths", nil, "paths matching these globs are explicitly denied")
	cmd.Flags().StringVar(&sessionStartRole, "role", "", "role name (default: $ACLINE_ROLE); persists as this session's role")
	return cmd
}

// printRecall lists the approved pitfall/failure_pattern memory recalled for
// the session's task, so the lessons reach the agent at the moment work starts.
func printRecall(entries []store.MemoryEntry) {
	if len(entries) == 0 {
		return
	}
	fmt.Printf("\nrelevant lessons (%d):\n", len(entries))
	for _, m := range entries {
		fmt.Printf("  #%d [%s] %s\n", m.ID, m.Kind, m.Body)
	}
}

func newSessionEndCmd(c *cli) *cobra.Command {
	var (
		sessionEndSummary   string
		sessionEndTokensIn  int64
		sessionEndTokensOut int64
		sessionEndCost      float64
	)
	cmd := &cobra.Command{
		Use:   "end",
		Short: "End the active work session",
		RunE: func(cmd *cobra.Command, args []string) error {
			var res app.EndSessionResult
			err := c.withApprovalToken("ending a policy-restricted session", func(token string) error {
				var eerr error
				res, eerr = app.EndSession(c.st, sessionEndSummary, store.SessionCost{
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
			fmt.Printf("session #%d ended\n", res.Session.ID)

			// End-of-session ritual: surface the memory review queue.
			if res.PendingMemory > 0 {
				fmt.Printf("%d memory entr%s pending review — run: acline memory review\n",
					res.PendingMemory, plural(res.PendingMemory, "y", "ies"))
			}

			// This project has opted into the JSON-as-source-of-truth workflow
			// (acline.json exists) — remind that the cache has moved ahead of it.
			if _, err := os.Stat(defaultSnapshotPath); err == nil {
				fmt.Printf("run: acline snapshot export   (keep %s in sync before committing)\n", defaultSnapshotPath)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&sessionEndSummary, "summary", "m", "", "summary of what was done")
	cmd.Flags().Int64Var(&sessionEndTokensIn, "tokens-in", 0, "input tokens consumed this session")
	cmd.Flags().Int64Var(&sessionEndTokensOut, "tokens-out", 0, "output tokens produced this session")
	cmd.Flags().Float64Var(&sessionEndCost, "cost", 0, "session cost in USD")
	return cmd
}

func newSessionCurrentCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "current",
		Short: "Show the active session",
		RunE: func(cmd *cobra.Command, args []string) error {
			sess, err := c.st.CurrentSession()
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
	return cmd
}

func newSessionListCmd(c *cli) *cobra.Command {
	var (
		sessionListProject string
	)
	var allProjects bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List recent sessions",
		RunE: func(cmd *cobra.Command, args []string) error {
			projectID, err := c.resolveListScope(sessionListProject, allProjects)
			if err != nil {
				return err
			}
			sessions, err := c.st.ListSessions(projectID, 20)
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
	cmd.Flags().StringVar(&sessionListProject, "project", "", "project name (default: the current project, else every project)")
	cmd.Flags().BoolVar(&allProjects, "all-projects", false, allProjectsUsage)
	return cmd
}
