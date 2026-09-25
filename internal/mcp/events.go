package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"acline/internal/store"
)

const (
	eventsResourceURI        = "acline://events"
	projectEventsTemplateURI = "acline://projects/{project}/events"
	taskEventsTemplateURI    = "acline://tasks/{id}/events"
	eventsResourceLimit      = 200
)

// eventOut mirrors store.Event with plain JSON-friendly types, the same
// reasoning as every other toXOut helper in this package.
type eventOut struct {
	ID        int64   `json:"id"`
	TaskID    *int64  `json:"task_id,omitempty"`
	SessionID *int64  `json:"session_id,omitempty"`
	Type      string  `json:"type"`
	Message   string  `json:"message"`
	ActorType *string `json:"actor_type,omitempty"`
	ActorID   *string `json:"actor_id,omitempty"`
	CreatedAt string  `json:"created_at"`
	Hash      *string `json:"hash,omitempty"`
}

func toEventOut(e store.Event) eventOut {
	return eventOut{
		ID: e.ID, TaskID: nullIntPtr(e.TaskID), SessionID: nullIntPtr(e.SessionID),
		Type: e.Type, Message: e.Message, ActorType: nullStrPtr(e.ActorType), ActorID: nullStrPtr(e.ActorID),
		CreatedAt: e.CreatedAt, Hash: nullStrPtr(e.Hash),
	}
}

// registerEventsResource exposes the hash-chained, append-only audit trail
// as an MCP resource, so the audit trail can be browsed without SQL from any
// MCP client's resource browser (a bespoke web/TUI viewer would be a bigger lift). Fixed to the most recent 200 events; a future revision could
// add a resource template (acline://events/{task_id}) for scoping, but v1
// keeps this to the single most useful view: "what just happened".
func registerEventsResource(s *sdkmcp.Server, st *store.Store) {
	s.AddResource(&sdkmcp.Resource{
		URI:         eventsResourceURI,
		Name:        "acline audit trail",
		Description: "The most recent 200 events from acline's hash-chained, append-only audit trail (events, approvals, checks all feed into this).",
		MIMEType:    "application/json",
	}, func(ctx context.Context, req *sdkmcp.ReadResourceRequest) (*sdkmcp.ReadResourceResult, error) {
		return eventsResult(st, eventsResourceURI, store.EventFilter{Limit: eventsResourceLimit})
	})

	// Scoped views of the same trail: the one above mixes every project's.
	s.AddResourceTemplate(&sdkmcp.ResourceTemplate{
		URITemplate: projectEventsTemplateURI,
		Name:        "acline project audit trail",
		Description: "The most recent 200 events of one project's tasks and sessions.",
		MIMEType:    "application/json",
	}, func(ctx context.Context, req *sdkmcp.ReadResourceRequest) (*sdkmcp.ReadResourceResult, error) {
		uri := req.Params.URI
		name, ok := strings.CutSuffix(strings.TrimPrefix(uri, "acline://projects/"), "/events")
		if !ok || name == "" || strings.Contains(name, "/") {
			return nil, sdkmcp.ResourceNotFoundError(uri)
		}
		if unescaped, err := url.PathUnescape(name); err == nil {
			name = unescaped
		}
		p, err := st.GetProjectByName(name)
		if err != nil {
			return nil, sdkmcp.ResourceNotFoundError(uri)
		}
		return eventsResult(st, uri, store.EventFilter{ProjectID: &p.ID, Limit: eventsResourceLimit})
	})
	s.AddResourceTemplate(&sdkmcp.ResourceTemplate{
		URITemplate: taskEventsTemplateURI,
		Name:        "acline task audit trail",
		Description: "The most recent 200 events of one task.",
		MIMEType:    "application/json",
	}, func(ctx context.Context, req *sdkmcp.ReadResourceRequest) (*sdkmcp.ReadResourceResult, error) {
		uri := req.Params.URI
		idStr, ok := strings.CutSuffix(strings.TrimPrefix(uri, "acline://tasks/"), "/events")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if !ok || err != nil {
			return nil, sdkmcp.ResourceNotFoundError(uri)
		}
		if _, err := st.GetTask(id); err != nil {
			return nil, sdkmcp.ResourceNotFoundError(uri)
		}
		return eventsResult(st, uri, store.EventFilter{TaskID: &id, Limit: eventsResourceLimit})
	})
}

func eventsResult(st *store.Store, uri string, f store.EventFilter) (*sdkmcp.ReadResourceResult, error) {
	events, err := st.QueryEvents(f)
	if err != nil {
		return nil, err
	}
	out := make([]eventOut, len(events))
	for i, e := range events {
		out[i] = toEventOut(e)
	}
	data, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("marshaling events: %w", err)
	}
	return &sdkmcp.ReadResourceResult{
		Contents: []*sdkmcp.ResourceContents{{URI: uri, MIMEType: "application/json", Text: string(data)}},
	}, nil
}
