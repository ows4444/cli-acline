package mcp

import (
	"context"
	"errors"
	"fmt"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"acline/internal/app"
	"acline/internal/store"
)

type noteAddArgs struct {
	Body    string `json:"body" jsonschema:"the note text -- fast, unstructured capture, promoted later via acline_reflect-equivalent CLI flow"`
	Project string `json:"project,omitempty" jsonschema:"project name to scope this note to"`
	Role    string `json:"role,omitempty" jsonschema:"role name (default: $ACLINE_ROLE)"`
}

type noteAddOut struct {
	ID              int64 `json:"id"`
	SecretsRedacted bool  `json:"secrets_redacted,omitempty"`
}

type logArgs struct {
	Message string `json:"message" jsonschema:"the note/decision/bug/commit/blocker text"`
	Type    string `json:"type" jsonschema:"required: decision|bug|commit|blocker|note (for a note that feeds /reflect, use acline_note_add instead)"`
	TaskID  *int64 `json:"task_id,omitempty" jsonschema:"attach this event to a task"`
	Role    string `json:"role,omitempty" jsonschema:"role name (default: $ACLINE_ROLE)"`
	Project string `json:"project,omitempty" jsonschema:"project name to resolve the role in"`
}

type logOut struct {
	ID              int64 `json:"id"`
	SecretsRedacted bool  `json:"secrets_redacted,omitempty"`
}

func registerNoteAndLogTools(s *sdkmcp.Server, st *store.Store) {
	addNoteAddTool(s, st)
	addLogTool(s, st)
}

func addNoteAddTool(s *sdkmcp.Server, st *store.Store) {
	addTool(s, &sdkmcp.Tool{
		Name:        "acline_note_add",
		Description: "Record a fast, unstructured capture -- the landing zone before a human decides whether it's durable enough to promote into a decision or memory entry.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args noteAddArgs) (*sdkmcp.CallToolResult, noteAddOut, error) {
		res, err := app.AddNote(st, app.AddNoteRequest{Body: args.Body, RoleArg: args.Role, ProjectArg: args.Project})
		if err != nil {
			return nil, noteAddOut{}, err
		}
		return textResult(recordedSummary("note", res)), noteAddOut{ID: res.ID, SecretsRedacted: res.Redacted}, nil
	})
}

func addLogTool(s *sdkmcp.Server, st *store.Store) {
	addTool(s, &sdkmcp.Tool{
		Name:        "acline_log",
		Description: "Record a history event (decision/bug/commit/blocker/note) in the append-only audit trail; type is required. History events never reach /reflect -- use acline_note_add for that. Any live secret value pasted in message is redacted before storage.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args logArgs) (*sdkmcp.CallToolResult, logOut, error) {
		res, err := app.Log(st, app.LogRequest{
			Type: args.Type, Message: args.Message, TaskID: args.TaskID, RoleArg: args.Role, ProjectArg: args.Project,
		})
		if errors.Is(err, app.ErrLogTypeRequired) {
			return nil, logOut{}, fmt.Errorf("type is required (decision|bug|commit|blocker|note); to capture a note for /reflect, use acline_note_add instead")
		}
		if err != nil {
			return nil, logOut{}, err
		}
		msg := fmt.Sprintf("event #%d logged", res.ID)
		if res.Redacted {
			msg += " (a pasted secret value was redacted before recording)"
		}
		return textResult(msg), logOut{ID: res.ID, SecretsRedacted: res.Redacted}, nil
	})
}
