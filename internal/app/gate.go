// This file is the second internal/app extraction (see scope.go's package
// doc): task approval/rejection, which previously existed as two
// near-identical copies -- internal/cmd/gate.go's approveCmd/rejectCmd and
// internal/mcp/gate.go's acline_approve/acline_reject -- each independently
// resolving a role and then calling store.RecordApproval.
package app

import "acline/internal/store"

// ApproveRequest carries a task-approval intent plus the role-resolution
// options that differ between the CLI (has a cwd fallback) and the MCP
// server (does not -- see ResolveProject's doc comment).
type ApproveRequest struct {
	TaskID           int64
	Kind             string
	By               string
	Note             string
	Token            string
	RoleArg          string
	ProjectArg       string
	AllowCwdFallback bool
}

// Approve resolves the request's role scope, then records approval. Returns
// store.ErrAgentCannotApprove unmodified when an agent actor tries to
// approve its own work; each adapter formats that error's user-facing
// wording itself.
func Approve(st *store.Store, req ApproveRequest) (int64, error) {
	roleID, err := ResolveRole(st, req.RoleArg, req.ProjectArg, req.AllowCwdFallback)
	if err != nil {
		return 0, err
	}
	return st.RecordApproval(store.ApprovalRequest{
		TaskID: req.TaskID, RoleID: roleID, Kind: req.Kind, Decision: "approved",
		By: req.By, Note: req.Note, Token: req.Token,
	})
}

// RejectRequest is Approve's counterpart for recording a rejection at
// review -- always kind "code_review", never token-gated (an override is
// what needs the approval token, not turning work back for revision).
type RejectRequest struct {
	TaskID           int64
	By               string
	Note             string
	RoleArg          string
	ProjectArg       string
	AllowCwdFallback bool
}

func Reject(st *store.Store, req RejectRequest) (int64, error) {
	roleID, err := ResolveRole(st, req.RoleArg, req.ProjectArg, req.AllowCwdFallback)
	if err != nil {
		return 0, err
	}
	return st.RecordApproval(store.ApprovalRequest{
		TaskID: req.TaskID, RoleID: roleID, Kind: "code_review", Decision: "rejected",
		By: req.By, Note: req.Note,
	})
}
