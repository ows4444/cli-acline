package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"acline/internal/clip"
	"acline/internal/store"
)

// The PostToolUse hook is the activity trail: until it existed only *denials*
// reached the audit trail, so a reviewer could not see which files an agent
// changed or which commands it ran during a session. It records one
// `tool_used` event per file write and per shell command, on the active
// session (and its task). Reads and searches are not recorded: they change
// nothing and would drown the trail. It never blocks and never fails the turn:
// by the time it runs the tool already has.

var hookPostToolUseCmd = &cobra.Command{
	Use:   "post-tool-use",
	Short: "PostToolUse: record which file an agent wrote or which command it ran on the active session (never blocks)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		enterProjectDir()
		recordToolUse(st, os.Stdin)
		return nil
	},
}

// maxCommandInEvent bounds how much of a shell command the event keeps; the
// digest identifies the whole command.
const maxCommandInEvent = 160

// recordToolUse writes the tool_used event for one PostToolUse payload, if the
// tool changes something and a session is active. Every failure is ignored.
func recordToolUse(s *store.Store, in io.Reader) {
	if s == nil {
		return
	}
	var payload hookPayload
	if json.NewDecoder(in).Decode(&payload) != nil {
		return
	}
	msg := toolUseMessage(payload)
	if msg == "" {
		return
	}
	sess, err := s.CurrentSession()
	if err != nil {
		return // no session: nothing to review this against
	}
	var taskID *int64
	if sess.TaskID.Valid {
		taskID = &sess.TaskID.Int64
	}
	if _, err := s.LogEvent(taskID, &sess.ID, "tool_used", msg); err != nil {
		fmt.Fprintf(os.Stderr, "acline: warning: tool use was not recorded: %v\n", err)
	}
}

// toolUseMessage describes a file write or a shell command; "" for anything else.
func toolUseMessage(p hookPayload) string {
	switch {
	case writeTools[p.ToolName]:
		if path := payloadPath(p); path != "" {
			return p.ToolName + " " + path
		}
	case p.ToolName == "Bash":
		command, _ := p.ToolInput["command"].(string)
		if strings.TrimSpace(command) == "" {
			return ""
		}
		sum := sha256.Sum256([]byte(command))
		oneLine := strings.Join(strings.Fields(command), " ")
		if len(oneLine) > maxCommandInEvent {
			oneLine = clip.Bytes(oneLine, maxCommandInEvent) + "…"
		}
		return fmt.Sprintf("Bash sha256:%s %s", hex.EncodeToString(sum[:8]), oneLine)
	}
	return ""
}

func init() {
	hookCmd.AddCommand(hookPostToolUseCmd)
}
