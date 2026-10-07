package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"acline/internal/app"
)

func newLogCmd(c *cli) *cobra.Command {
	var (
		logTask string
		logType string
		logRole string
	)
	cmd := &cobra.Command{
		Use:   "log <message>",
		Short: "Record a history event (note, decision, bug, commit, blocker)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			taskID, err := parseOptionalID(logTask, "task")
			if err != nil {
				return err
			}
			res, err := app.Log(c.st, app.LogRequest{
				Type: logType, Message: strings.Join(args, " "), TaskID: taskID, RoleArg: logRole, AllowCwdFallback: true,
			})
			// No default type: a bare `acline log "..."` used to record a note-type
			// history event, easy to mistake for `acline note add`, which is what
			// actually feeds /reflect.
			if errors.Is(err, app.ErrLogTypeRequired) {
				return errors.New("--type is required (decision|bug|commit|blocker|note); to capture a note for /reflect, use `acline note add` instead")
			}
			if err != nil {
				return err
			}
			if res.Redacted {
				fmt.Println("note: a pasted secret value was redacted before recording")
			}
			fmt.Printf("event #%d logged\n", res.ID)
			return nil
		},
	}
	cmd.Flags().StringVarP(&logTask, "task", "t", "", "associate this event with a task id")
	cmd.Flags().StringVar(&logType, "type", "", "required: decision|bug|commit|blocker|note (for a /reflect note, use `acline note add`)")
	cmd.Flags().StringVar(&logRole, "role", "", "role name (default: $ACLINE_ROLE or the active session's role)")
	return cmd
}

func newHistoryCmd(c *cli) *cobra.Command {
	var (
		historyTask  string
		historyLimit int
	)
	cmd := &cobra.Command{
		Use:   "history",
		Short: "Show recent history events",
		RunE: func(cmd *cobra.Command, args []string) error {
			taskID, err := parseOptionalID(historyTask, "task")
			if err != nil {
				return err
			}
			events, err := c.st.ListEvents(taskID, historyLimit)
			if err != nil {
				return err
			}
			if len(events) == 0 {
				fmt.Println("no history")
				return nil
			}
			for i := len(events) - 1; i >= 0; i-- {
				e := events[i]
				taskPart := ""
				if e.TaskID.Valid {
					taskPart = fmt.Sprintf(" task#%d", e.TaskID.Int64)
				}
				fmt.Printf("[%s]%s %s: %s\n", e.CreatedAt, taskPart, e.Type, e.Message)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&historyTask, "task", "t", "", "filter by task id")
	cmd.Flags().IntVarP(&historyLimit, "limit", "n", 20, "max number of events")
	return cmd
}
