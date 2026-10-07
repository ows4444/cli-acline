package cmd

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"acline/internal/app"
	"acline/internal/store"
)

func newTaskCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "Manage tasks",
	}
	cmd.AddCommand(newTaskAddCmd(c), newTaskListCmd(c), newTaskArchiveCmd(c), newTaskShowCmd(c), newTaskUpdateCmd(c), newTaskDoneCmd(c), newTaskGateCmd(c), newTaskDeferCmd(c), newTaskCriteriaCmd(c), newTaskLinkCmd(c), newTaskAssignCmd(c))
	cmd.AddCommand(newTaskPromoteCmd(c))
	return cmd
}

func newTaskAddCmd(c *cli) *cobra.Command {
	var (
		taskDesc      string
		taskPriority  string
		taskArea      string
		taskType      string
		taskRisk      string
		taskAutonomy  string
		taskSpec      string
		taskMilestone string
		taskParent    string
		taskProject   string
		taskRole      string
	)
	cmd := &cobra.Command{
		Use:   "add <title>",
		Short: "Add a new task",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			title := strings.Join(args, " ")
			req := app.AddTaskRequest{
				Title: title, Description: taskDesc, Priority: taskPriority, Area: taskArea, Type: taskType,
				RoleArg: taskRole, ProjectArg: taskProject, AllowCwdFallback: true,
			}
			// Only what was typed: an unset risk or autonomy takes the project's
			// default (`acline init`, `project add --risk/--autonomy`), else low/hotl.
			if cmd.Flags().Changed("risk") {
				req.Risk = taskRisk
			}
			if cmd.Flags().Changed("autonomy") {
				req.Autonomy = taskAutonomy
			}
			var err error
			if req.SpecID, err = parseOptionalID(taskSpec, "spec"); err != nil {
				return err
			}
			if req.MilestoneID, err = parseOptionalID(taskMilestone, "milestone"); err != nil {
				return err
			}
			if req.ParentID, err = parseOptionalID(taskParent, "parent"); err != nil {
				return err
			}
			var id int64
			err = c.withApprovalToken("creating a task at autonomy auto", func(token string) error {
				req.Token = token
				var aerr error
				id, aerr = app.AddTask(c.st, req)
				return aerr
			})
			if err != nil {
				return err
			}
			fmt.Printf("task #%d created: %s\n", id, title)
			return nil
		},
	}
	cmd.Flags().StringVarP(&taskDesc, "desc", "d", "", "task description")
	cmd.Flags().StringVarP(&taskPriority, "priority", "p", "normal", "priority: low|normal|high|urgent")
	cmd.Flags().StringVar(&taskArea, "area", "", "ownership area, e.g. backend|frontend|mobile")
	cmd.Flags().StringVar(&taskType, "type", "", "bug|refactor|test|architecture|security|performance|reliability|contract|database|messaging|ui_ux|accessibility|feature|debt|docs|devex")
	cmd.Flags().StringVar(&taskRisk, "risk", "", "low|medium|high|critical (high+ requires human approval to complete; default: the project's, else low)")
	cmd.Flags().StringVar(&taskAutonomy, "autonomy", "", "hitl|hotl|auto (hitl requires human approval to complete; default: the project's, else hotl)")
	cmd.Flags().StringVar(&taskSpec, "spec", "", "spec id this task derives from")
	cmd.Flags().StringVar(&taskMilestone, "milestone", "", "milestone id this task belongs to")
	cmd.Flags().StringVar(&taskParent, "parent", "", "parent task id (a subtask; grouping only, it does not make the parent wait)")
	cmd.Flags().StringVar(&taskProject, "project", "", "project name (default: resolved from cwd/ACLINE_PROJECT)")
	cmd.Flags().StringVar(&taskRole, "role", "", "role name (default: $ACLINE_ROLE or the active session's role)")
	return cmd
}

// taskJSONView is the --json view of a store.Task: plain types only, so
// nullable columns serialize as a value or JSON null instead of
// database/sql's {"String":"x","Valid":true} shape.
type taskJSONView struct {
	ID             int64   `json:"id"`
	Title          string  `json:"title"`
	Description    string  `json:"description"`
	Status         string  `json:"status"`
	Priority       string  `json:"priority"`
	Area           *string `json:"area"`
	Type           *string `json:"type"`
	Risk           string  `json:"risk"`
	Autonomy       string  `json:"autonomy"`
	Deferred       bool    `json:"deferred"`
	DeferredReason *string `json:"deferred_reason"`
	BlockedReason  *string `json:"blocked_reason"`
	SpecID         *int64  `json:"spec_id"`
	DecisionID     *int64  `json:"decision_id"`
	ProjectID      *int64  `json:"project_id"`
	ParentID       *int64  `json:"parent_id"`
	MilestoneID    *int64  `json:"milestone_id"`
	RoleID         *int64  `json:"role_id"`
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      string  `json:"updated_at"`
	CompletedAt    *string `json:"completed_at"`
}

func newTaskJSONView(t store.Task) taskJSONView {
	return taskJSONView{
		ID: t.ID, Title: t.Title, Description: t.Description, Status: t.Status, Priority: t.Priority,
		Area: nullStrPtr(t.Area), Type: nullStrPtr(t.Type), Risk: t.Risk, Autonomy: t.Autonomy,
		Deferred: t.Deferred, DeferredReason: nullStrPtr(t.DeferredReason), BlockedReason: nullStrPtr(t.BlockedReason),
		SpecID: nullIntPtr(t.SpecID), DecisionID: nullIntPtr(t.DecisionID), ProjectID: nullIntPtr(t.ProjectID),
		ParentID: nullIntPtr(t.ParentID), MilestoneID: nullIntPtr(t.MilestoneID), RoleID: nullIntPtr(t.RoleID),
		CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt, CompletedAt: nullStrPtr(t.CompletedAt),
	}
}

func newTaskListCmd(c *cli) *cobra.Command {
	var (
		taskProject string
		listStatus  string
		listArea    string
		listRisk    string
		listAll     bool
		listJSON    bool
	)
	var allProjects bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List tasks",
		RunE: func(cmd *cobra.Command, args []string) error {
			projectID, err := c.resolveListScope(taskProject, allProjects)
			if err != nil {
				return err
			}
			tasks, err := c.st.ListTasks(store.TaskFilter{
				Status: listStatus, Area: listArea, Risk: listRisk, All: listAll, ProjectID: projectID,
			})
			if err != nil {
				return err
			}
			if listJSON {
				out := make([]taskJSONView, len(tasks))
				for i, t := range tasks {
					out[i] = newTaskJSONView(t)
				}
				return printJSON(out)
			}
			if len(tasks) == 0 {
				fmt.Println("no tasks")
				return nil
			}
			fmt.Printf("%-4s %-11s %-7s %-8s %-5s %-6s %s\n", "ID", "STATUS", "PRIO", "RISK", "AUTO", "BY", "TITLE")
			for _, t := range tasks {
				marker := t.Title
				if t.Deferred {
					marker = "(deferred) " + marker
				}
				fmt.Printf("%-4d %-11s %-7s %-8s %-5s %-6s %s\n",
					t.ID, t.Status, t.Priority, t.Risk, t.Autonomy, actorShort(t.ActorType), marker)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&listStatus, "status", "s", "", "filter by status")
	cmd.Flags().StringVar(&listArea, "area", "", "filter by area")
	cmd.Flags().StringVar(&listRisk, "risk", "", "filter by risk")
	cmd.Flags().BoolVarP(&listAll, "all", "a", false, "include done/cancelled tasks")
	cmd.Flags().StringVar(&taskProject, "project", "", "project name (default: the current project, else every project)")
	cmd.Flags().BoolVar(&allProjects, "all-projects", false, allProjectsUsage)
	cmd.Flags().BoolVar(&listJSON, "json", false, "print results as a JSON array instead of text")
	return cmd
}

// actorShort renders the actor type compactly for table output.
func actorShort(t sql.NullString) string {
	switch t.String {
	case "agent":
		return "agent"
	case "human":
		return "human"
	default:
		return "-"
	}
}

func newTaskArchiveCmd(c *cli) *cobra.Command {
	var (
		taskProject string
	)
	var allProjects bool
	cmd := &cobra.Command{
		Use:   "archive",
		Short: "List archived (done/cancelled) tasks",
		RunE: func(cmd *cobra.Command, args []string) error {
			projectID, err := c.resolveListScope(taskProject, allProjects)
			if err != nil {
				return err
			}
			var archived []store.Task
			for _, status := range []string{"done", "cancelled"} {
				batch, err := c.st.ListTasks(store.TaskFilter{Status: status, ProjectID: projectID})
				if err != nil {
					return err
				}
				archived = append(archived, batch...)
			}
			if len(archived) == 0 {
				fmt.Println("no archived tasks")
				return nil
			}
			fmt.Printf("%-4s %-11s %s\n", "ID", "STATUS", "TITLE")
			for _, t := range archived {
				fmt.Printf("%-4d %-11s %s\n", t.ID, t.Status, t.Title)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&taskProject, "project", "", "project name (default: the current project, else every project)")
	cmd.Flags().BoolVar(&allProjects, "all-projects", false, allProjectsUsage)
	return cmd
}

func newTaskShowCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show task details, provenance, gates, and history",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], "task")
			if err != nil {
				return err
			}
			t, err := c.st.GetTask(id)
			if err != nil {
				return err
			}
			fmt.Printf("#%d %s\n", t.ID, t.Title)
			fmt.Printf("status:   %s\n", t.Status)
			fmt.Printf("priority: %s\n", t.Priority)
			fmt.Printf("risk:     %s\n", t.Risk)
			fmt.Printf("autonomy: %s\n", t.Autonomy)
			if t.Area.Valid {
				fmt.Printf("area:     %s\n", t.Area.String)
			}
			if t.Type.Valid {
				fmt.Printf("type:     %s\n", t.Type.String)
			}
			if t.ActorType.Valid {
				line := t.ActorType.String
				if t.ActorID.Valid {
					line += " / " + t.ActorID.String
				}
				if t.Model.Valid {
					line += " (" + t.Model.String + ")"
				}
				fmt.Printf("created by: %s\n", line)
			}
			if t.SpecID.Valid {
				fmt.Printf("spec:     #%d\n", t.SpecID.Int64)
			}
			if t.DecisionID.Valid {
				fmt.Printf("decision: #%d\n", t.DecisionID.Int64)
			}
			if t.MilestoneID.Valid {
				fmt.Printf("milestone: #%d\n", t.MilestoneID.Int64)
			}
			if t.RoleID.Valid {
				if r, err := c.st.GetRole(t.RoleID.Int64); err == nil {
					fmt.Printf("role:     %s\n", r.Name)
					if hint, err := c.st.NextRoleHint(nullIntPtr(t.ProjectID), t.RoleID); err == nil && hint != nil {
						fmt.Printf("next (advisory): %s\n", hint.Name)
					}
				}
			}
			if t.BlockedReason.Valid {
				fmt.Printf("blocked:  %s\n", t.BlockedReason.String)
			}
			if t.Deferred {
				fmt.Printf("deferred: yes (%s)\n", t.DeferredReason.String)
				if t.RevisitTrigger.Valid {
					fmt.Printf("revisit:  %s\n", t.RevisitTrigger.String)
				}
			}
			if t.Description != "" {
				fmt.Printf("desc:     %s\n", t.Description)
			}
			fmt.Printf("created:  %s\n", t.CreatedAt)
			fmt.Printf("updated:  %s\n", t.UpdatedAt)
			if t.CompletedAt.Valid {
				fmt.Printf("completed:%s\n", t.CompletedAt.String)
			}

			criteria, err := c.st.ListCriteria(id)
			if err != nil {
				return err
			}
			if len(criteria) > 0 {
				fmt.Println("\nacceptance criteria:")
				for _, c := range criteria {
					box := "[ ]"
					if c.Done {
						box = "[x]"
					}
					pattern := ""
					if c.Pattern.Valid {
						pattern = " (" + c.Pattern.String + ")"
					}
					fmt.Printf("  %s #%d %s%s\n", box, c.ID, c.Text, pattern)
				}
			}

			checks, err := c.st.ListChecks(id)
			if err != nil {
				return err
			}
			if len(checks) > 0 {
				fmt.Println("\nverification:")
				for _, c := range checks {
					fmt.Printf("  [%s] %s %s\n", c.Status, c.Kind, c.Detail.String)
				}
			}

			approvals, err := c.st.ListApprovals(id)
			if err != nil {
				return err
			}
			if len(approvals) > 0 {
				fmt.Println("\napprovals:")
				for _, a := range approvals {
					fmt.Printf("  %s by %s (%s) %s\n", a.Decision, a.Approver, a.Kind, a.Note.String)
				}
			}

			links, err := c.st.ListLinks(id)
			if err != nil {
				return err
			}
			if len(links) > 0 {
				fmt.Println("\nlinks:")
				for _, l := range links {
					fmt.Printf("  %s task #%d\n", l.Relation, l.RelatedTaskID)
				}
			}

			events, err := c.st.ListEvents(&id, 50)
			if err != nil {
				return err
			}
			if len(events) > 0 {
				fmt.Println("\nhistory:")
				for i := len(events) - 1; i >= 0; i-- {
					e := events[i]
					by := ""
					if e.ActorID.Valid {
						by = " <" + e.ActorID.String + ">"
					}
					fmt.Printf("  [%s]%s %s: %s\n", e.CreatedAt, by, e.Type, e.Message)
				}
			}
			return nil
		},
	}
	return cmd
}

func newTaskUpdateCmd(c *cli) *cobra.Command {
	var (
		updateStatus    string
		updateReason    string
		updatePriority  string
		updateArea      string
		updateType      string
		updateRisk      string
		updateAutonomy  string
		updateMilestone string
	)
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a task's status, priority, area, type, risk, autonomy, or milestone",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], "task")
			if err != nil {
				return err
			}
			u := store.TaskUpdate{
				Status: updateStatus, Reason: updateReason, Priority: updatePriority, Area: updateArea,
				Type: updateType, Risk: updateRisk, Autonomy: updateAutonomy,
			}
			if updateMilestone == "none" {
				u.ClearMilestone = true
			} else if u.Milestone, err = parseOptionalID(updateMilestone, "milestone"); err != nil {
				return err
			}
			// Lowering risk or loosening autonomy is privileged: the token is
			// prompted for only if the store requires one, and nothing is applied
			// until every field is allowed.
			err = c.withApprovalToken("changing task #"+args[0], func(token string) error {
				u.Token = token
				_, uerr := app.UpdateTask(c.st, id, u)
				return uerr
			})
			switch {
			case errors.Is(err, app.ErrNothingToUpdate):
				return fmt.Errorf("nothing to update: pass --status, --priority, --area, --type, --risk, --autonomy, and/or --milestone")
			case errors.Is(err, app.ErrReasonNeedsBlocked):
				return fmt.Errorf("--reason only applies to --status blocked")
			case errors.Is(err, store.ErrStatusDoneNeedsGate):
				return fmt.Errorf("use 'acline task done %d' so completion gates are evaluated", id)
			case err != nil:
				return err
			}
			fmt.Printf("task #%d updated\n", id)
			return nil
		},
	}
	cmd.Flags().StringVar(&updateStatus, "status", "", "backlog|todo|in_progress|blocked|review|cancelled")
	cmd.Flags().StringVar(&updateReason, "reason", "", "why the task is blocked (with --status blocked)")
	cmd.Flags().StringVar(&updatePriority, "priority", "", "low|normal|high|urgent")
	cmd.Flags().StringVar(&updateArea, "area", "", "ownership area")
	cmd.Flags().StringVar(&updateType, "type", "", "task type")
	cmd.Flags().StringVar(&updateRisk, "risk", "", "low|medium|high|critical")
	cmd.Flags().StringVar(&updateAutonomy, "autonomy", "", "hitl|hotl|auto")
	cmd.Flags().StringVar(&updateMilestone, "milestone", "", "milestone id to assign, or 'none' to clear")
	return cmd
}

func newTaskDoneCmd(c *cli) *cobra.Command {
	var (
		doneForce bool
	)
	cmd := &cobra.Command{
		Use:   "done <id>",
		Short: "Mark a task done, subject to verification and approval gates",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], "task")
			if err != nil {
				return err
			}
			var result store.CompleteResult
			err = c.withApprovalToken("overriding the gate on task #"+args[0], func(token string) error {
				var cerr error
				result, cerr = app.CompleteTask(c.st, app.CompleteTaskRequest{TaskID: id, Force: doneForce, Token: token, Hash: c.hashTree})
				return cerr
			})
			var blocked *store.GateBlockedError
			if errors.As(err, &blocked) {
				var b strings.Builder
				b.WriteString("cannot complete task: gate not satisfied\n")
				for _, blocker := range blocked.Blockers {
					b.WriteString("  - " + blocker + "\n")
				}
				b.WriteString("re-run with --force to override (the override is recorded)")
				return fmt.Errorf("%s", b.String())
			}
			if err != nil {
				return err
			}
			for _, w := range result.Warnings {
				fmt.Printf("warning: %s\n", w)
			}
			if result.Overridden {
				fmt.Printf("gate overridden for task #%d (recorded)\n", id)
			}
			fmt.Printf("task #%d marked done\n", id)
			return nil
		},
	}
	cmd.Flags().BoolVar(&doneForce, "force", false, "complete despite unmet gates (records an override)")
	return cmd
}

func newTaskGateCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gate <id>",
		Short: "Show whether a task can be completed, and what's missing",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], "task")
			if err != nil {
				return err
			}
			gate, err := c.st.EvaluateGateForTree(id, c.taskGateTree(id))
			if err != nil {
				return err
			}
			if gate.OK() {
				fmt.Printf("task #%d: gate satisfied\n", id)
			} else {
				fmt.Printf("task #%d: gate NOT satisfied\n", id)
				for _, b := range gate.Blockers {
					fmt.Printf("  blocker: %s\n", b)
				}
			}
			for _, w := range gate.Warnings {
				fmt.Printf("  warning: %s\n", w)
			}
			return nil
		},
	}
	return cmd
}

func newTaskDeferCmd(c *cli) *cobra.Command {
	var (
		deferReason  string
		deferTrigger string
		deferClear   bool
	)
	cmd := &cobra.Command{
		Use:   "defer <id>",
		Short: "Mark a task deferred (disposition, independent of status), or clear it with --clear",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], "task")
			if err != nil {
				return err
			}
			err = app.DeferTask(c.st, id, deferClear, deferReason, deferTrigger)
			if errors.Is(err, app.ErrDeferReasonRequired) {
				return fmt.Errorf("--reason is required when deferring a task")
			}
			if err != nil {
				return err
			}
			if deferClear {
				fmt.Printf("task #%d deferred flag cleared\n", id)
				return nil
			}
			fmt.Printf("task #%d deferred\n", id)
			return nil
		},
	}
	cmd.Flags().StringVar(&deferReason, "reason", "", "why this task is deferred")
	cmd.Flags().StringVar(&deferTrigger, "trigger", "", "condition that should bring this back into scope")
	cmd.Flags().BoolVar(&deferClear, "clear", false, "clear the deferred flag")
	return cmd
}

func newTaskCriteriaCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "criteria",
		Short: "Manage acceptance criteria on a task",
	}
	cmd.AddCommand(newTaskCriteriaAddCmd(c), newTaskCriteriaCheckCmd(c), newTaskCriteriaUncheckCmd(c))
	return cmd
}

func newTaskCriteriaAddCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <task-id> <text...>",
		Short: "Add an acceptance criterion (EARS phrasing recommended)",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			taskID, err := parseID(args[0], "task")
			if err != nil {
				return err
			}
			text := strings.Join(args[1:], " ")
			id, pattern, err := app.AddCriterion(c.st, taskID, text)
			if err != nil {
				return err
			}
			if pattern == "" {
				fmt.Println("warning: criterion does not follow an EARS template; consider:")
				fmt.Println("  When <trigger>, the <system> shall <response>")
				fmt.Println("  While <state>, the <system> shall <response>")
				fmt.Println("  If <trigger>, then the <system> shall <response>")
				fmt.Println("  The <system> shall <response>")
			} else {
				fmt.Printf("EARS pattern: %s\n", pattern)
			}
			fmt.Printf("criterion #%d added to task #%d\n", id, taskID)
			return nil
		},
	}
	return cmd
}

func newTaskCriteriaCheckCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check <criterion-id>",
		Short: "Mark an acceptance criterion done",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], "criterion")
			if err != nil {
				return err
			}
			if err := c.st.SetCriterionDone(id, true); err != nil {
				return err
			}
			fmt.Printf("criterion #%d checked\n", id)
			return nil
		},
	}
	return cmd
}

func newTaskCriteriaUncheckCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "uncheck <criterion-id>",
		Short: "Mark an acceptance criterion not done again",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], "criterion")
			if err != nil {
				return err
			}
			if err := c.st.SetCriterionDone(id, false); err != nil {
				return err
			}
			fmt.Printf("criterion #%d unchecked\n", id)
			return nil
		},
	}
	return cmd
}

func newTaskLinkCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "link <task-id> <depends_on|blocks|related> <other-task-id>",
		Short: "Link two tasks",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			taskID, err := parseID(args[0], "task")
			if err != nil {
				return err
			}
			relation := args[1]
			otherID, err := parseID(args[2], "task")
			if err != nil {
				return err
			}
			id, err := app.LinkTasks(c.st, taskID, relation, otherID)
			if err != nil {
				return err
			}
			fmt.Printf("link #%d created: task #%d %s task #%d\n", id, taskID, relation, otherID)
			return nil
		},
	}
	return cmd
}

// taskAssignCmd is the roles feature's workflow-stage handoff (acline spec
// #2): tasks.role_id IS the task's current owning role, and the sequence
// of role_assigned events this logs IS the workflow trail -- no separate
// state machine, nothing enforced beyond "here's who owns it now" (see
// Store.AssignRole and Store.NextRoleHint).
func newTaskAssignCmd(c *cli) *cobra.Command {
	var (
		taskAssignProject string
	)
	cmd := &cobra.Command{
		Use:   "assign <task-id> <role-name>",
		Short: "Set the task's current owning role (the workflow-stage handoff)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], "task")
			if err != nil {
				return err
			}
			res, err := app.AssignRole(c.st, app.AssignRoleRequest{TaskID: id, Role: args[1], ProjectArg: taskAssignProject})
			if err != nil {
				return err
			}
			fmt.Printf("task #%d assigned to role %s (was: %s)\n", id, res.Role.Name, res.Previous)
			if res.Next != nil {
				fmt.Printf("next (advisory): %s\n", *res.Next)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&taskAssignProject, "project", "", "project to look the role up in (default: the task's own project)")
	return cmd
}

func parseID(s, kind string) (int64, error) {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s id %q", kind, s)
	}
	return id, nil
}

// parseOptionalID is parseID for an optional flag: "" means not given (nil).
func parseOptionalID(s, kind string) (*int64, error) {
	if s == "" {
		return nil, nil
	}
	id, err := parseID(s, kind)
	if err != nil {
		return nil, err
	}
	return &id, nil
}

// runStatusTransition is the shared pending->approved/rejected/accepted
// pattern behind decision accept/reject, spec approve, and memory
// approve/reject: parse the id, call the store's setter (which records the
// event in its own transaction), then print a one-line confirmation. Task approve/reject is deliberately NOT unified
// here -- it writes to the append-only approvals table with actor-identity
// rules (an agent can't approve its own work), a genuinely different shape
// from a plain status-column swap, not just the same pattern restated.
func runStatusTransition(args []string, kind string, setStatus func(id int64, status string) error, status, verb string) error {
	id, err := parseID(args[0], kind)
	if err != nil {
		return err
	}
	if err := setStatus(id, status); err != nil {
		return err
	}
	fmt.Printf("%s #%d %s\n", kind, id, verb)
	return nil
}
