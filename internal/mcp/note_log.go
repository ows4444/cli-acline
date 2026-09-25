package mcp

import (
	"context"
	"fmt"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"acline/internal/redact"
	"acline/internal/store"
)

type noteAddArgs struct {
	Body    string `json:"body" jsonschema:"the note text -- fast, unstructured capture, promoted later via acline_reflect-equivalent CLI flow"`
	Project string `json:"project,omitempty" jsonschema:"project name to scope this note to"`
	Role    string `json:"role,omitempty" jsonschema:"role name (default: $ACLINE_ROLE)"`
}

type noteAddOut struct {
	ID int64 `json:"id"`
}

type logArgs struct {
	Message string `json:"message" jsonschema:"the note/decision/bug/commit/blocker text"`
	Type    string `json:"type" jsonschema:"required: decision|bug|commit|blocker|note (for a note that feeds /reflect, use acline_note_add instead)"`
	TaskID  *int64 `json:"task_id,omitempty" jsonschema:"attach this event to a task"`
	Role    string `json:"role,omitempty" jsonschema:"role name (default: $ACLINE_ROLE)"`
	Project string `json:"project,omitempty" jsonschema:"project name to resolve the role in"`
}

type logOut struct {
	ID int64 `json:"id"`
}

func registerNoteAndLogTools(s *sdkmcp.Server, st *store.Store) {
	addTool(s, &sdkmcp.Tool{
		Name:        "acline_note_add",
		Description: "Record a fast, unstructured capture -- the landing zone before a human decides whether it's durable enough to promote into a decision or memory entry.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args noteAddArgs) (*sdkmcp.CallToolResult, noteAddOut, error) {
		if args.Body == "" {
			return nil, noteAddOut{}, fmt.Errorf("body is required")
		}
		projectID, err := resolveProject(st, args.Project)
		if err != nil {
			return nil, noteAddOut{}, err
		}
		roleID, err := resolveRole(st, args.Role, args.Project)
		if err != nil {
			return nil, noteAddOut{}, err
		}
		id, err := st.AddNoteWithRole(projectID, roleID, args.Body, "manual")
		if err != nil {
			return nil, noteAddOut{}, err
		}
		return textResult(fmt.Sprintf("note #%d recorded", id)), noteAddOut{ID: id}, nil
	})

	addTool(s, &sdkmcp.Tool{
		Name:        "acline_log",
		Description: "Record a history event (decision/bug/commit/blocker/note) in the append-only audit trail; type is required. History events never reach /reflect -- use acline_note_add for that. Any live secret value pasted in message is redacted before storage.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args logArgs) (*sdkmcp.CallToolResult, logOut, error) {
		if args.Message == "" {
			return nil, logOut{}, fmt.Errorf("message is required")
		}
		logType := args.Type
		if logType == "" {
			return nil, logOut{}, fmt.Errorf("type is required (decision|bug|commit|blocker|note); to capture a note for /reflect, use acline_note_add instead")
		}
		if !store.UserLogTypes[logType] {
			return nil, logOut{}, fmt.Errorf("invalid type %q (want: note|decision|bug|commit|blocker)", logType)
		}
		roleID, err := resolveRole(st, args.Role, args.Project)
		if err != nil {
			return nil, logOut{}, err
		}
		message, redacted := redact.Secrets(args.Message)
		var sessionID *int64
		if sess, err := st.CurrentSession(); err == nil {
			sessionID = &sess.ID
		}
		id, err := st.LogEventWithRole(args.TaskID, sessionID, roleID, logType, message)
		if err != nil {
			return nil, logOut{}, err
		}
		if redacted {
			st.LogEventGlobal("secret_redacted", fmt.Sprintf("log event #%d: a pasted secret value was redacted before recording", id))
		}
		return textResult(fmt.Sprintf("event #%d logged", id)), logOut{ID: id}, nil
	})
}
