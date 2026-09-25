package cmd

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"acline/internal/redact"
	"acline/internal/store"
)

var (
	logTask string
	logType string
	logRole string
)

var logCmd = &cobra.Command{
	Use:   "log <message>",
	Short: "Record a history event (note, decision, bug, commit, blocker)",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		message := strings.Join(args, " ")
		if redactedMsg, found := redact.Secrets(message); found {
			message = redactedMsg
			logEventGlobal("secret_redacted", "acline log: a pasted secret value was redacted before recording")
		}
		// No default: a bare `acline log "..."` used to record a note-type
		// history event, easy to mistake for `acline note add`, which is
		// what actually feeds /reflect.
		if logType == "" {
			return errors.New("--type is required (decision|bug|commit|blocker|note); to capture a note for /reflect, use `acline note add` instead")
		}
		if !store.UserLogTypes[logType] {
			return fmt.Errorf("invalid type %q (want: note|decision|bug|commit|blocker)", logType)
		}
		var taskID *int64
		if logTask != "" {
			id, err := strconv.ParseInt(logTask, 10, 64)
			if err != nil {
				return fmt.Errorf("invalid task id: %w", err)
			}
			taskID = &id
		}
		var sessionID *int64
		if sess, err := st.CurrentSession(); err == nil {
			sessionID = &sess.ID
		} else if !errors.Is(err, store.ErrNoActiveSession) {
			return err
		}
		roleID, err := resolveRoleFlag(logRole, "")
		if err != nil {
			return err
		}
		id, err := st.LogEventWithRole(taskID, sessionID, roleID, logType, message)
		if err != nil {
			return err
		}
		fmt.Printf("event #%d logged\n", id)
		return nil
	},
}

var (
	historyTask  string
	historyLimit int
)

var historyCmd = &cobra.Command{
	Use:   "history",
	Short: "Show recent history events",
	RunE: func(cmd *cobra.Command, args []string) error {
		var taskID *int64
		if historyTask != "" {
			id, err := strconv.ParseInt(historyTask, 10, 64)
			if err != nil {
				return fmt.Errorf("invalid task id: %w", err)
			}
			taskID = &id
		}
		events, err := st.ListEvents(taskID, historyLimit)
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

// logEventGlobal is a thin wrapper over store.LogEventGlobal, kept so the
// many existing logEventGlobal(...) call sites throughout cmd don't all
// need to change to st.LogEventGlobal(...).
func logEventGlobal(eventType, message string) {
	st.LogEventGlobal(eventType, message)
}

func init() {
	logCmd.Flags().StringVarP(&logTask, "task", "t", "", "associate this event with a task id")
	logCmd.Flags().StringVar(&logType, "type", "", "required: decision|bug|commit|blocker|note (for a /reflect note, use `acline note add`)")
	logCmd.Flags().StringVar(&logRole, "role", "", "role name (default: $ACLINE_ROLE or the active session's role)")

	historyCmd.Flags().StringVarP(&historyTask, "task", "t", "", "filter by task id")
	historyCmd.Flags().IntVarP(&historyLimit, "limit", "n", 20, "max number of events")

	rootCmd.AddCommand(logCmd, historyCmd)
}
