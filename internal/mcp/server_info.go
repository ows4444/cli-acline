package mcp

import (
	"context"
	"fmt"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"acline/internal/store"
)

type serverInfoArgs struct{}

type serverInfoOut struct {
	Version   string `json:"version"`
	DBPath    string `json:"db_path"`
	ActorType string `json:"actor_type"`
	ActorID   string `json:"actor_id"`
	Model     string `json:"model,omitempty"`
	// ApprovalTokenEnabled tells a client whether it must collect a human's
	// approval token before approving, forcing a gate, or approving memory.
	// ts:"optional" since it's absent on servers older than acline 0.2.
	ApprovalTokenEnabled bool `json:"approval_token_enabled" ts:"optional"`
	// ProtocolVersion is ProtocolVersion; absent on servers that predate it.
	ProtocolVersion int `json:"protocol_version" ts:"optional"`
}

// registerServerInfoTool lets a client see which store and identity this
// server is actually bound to. A spawned server resolves its DB path and actor
// from its own environment, which is not necessarily its client's — without
// this a client could not tell it was talking to a different database than the
// CLI in the user's terminal.
func registerServerInfoTool(s *sdkmcp.Server, st *store.Store) {
	addTool(s, &sdkmcp.Tool{
		Name:        "acline_server_info",
		Description: "Report this server's version, the database file it is bound to, and the actor identity it records writes under.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args serverInfoArgs) (*sdkmcp.CallToolResult, serverInfoOut, error) {
		enabled, err := st.ApprovalTokenEnabled()
		if err != nil {
			return nil, serverInfoOut{}, err
		}
		out := serverInfoOut{
			Version: Version, DBPath: st.Path,
			ActorType: st.Actor.Type, ActorID: st.Actor.ID, Model: st.Actor.Model,
			ApprovalTokenEnabled: enabled, ProtocolVersion: ProtocolVersion,
		}
		return textResult(fmt.Sprintf("acline mcp %s, db %s, acting as %s/%s", out.Version, out.DBPath, out.ActorType, out.ActorID)), out, nil
	})
}
