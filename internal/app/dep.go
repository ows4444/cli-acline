// This file records dependencies for every adapter: `acline dep add|verify`
// and acline_dep_add|verify each checked the task, resolved the project and
// called the store themselves. Only the CLI split name@version and only the
// MCP tool required an ecosystem and name.
package app

import (
	"errors"
	"strings"

	"acline/internal/store"
)

// ErrDependencyIncomplete is a dependency without an ecosystem or name.
var ErrDependencyIncomplete = errors.New("ecosystem and name are required")

// AddDependencyRequest records a package. Name may carry its version
// (name@version, scoped npm names included) when Version is "". The project is
// ProjectArg, else the task's, else (with AllowCwdFallback) the current one.
// Verified needs a person (or Token).
type AddDependencyRequest struct {
	Ecosystem        string
	Name             string
	Version          string
	TaskID           *int64
	ProjectArg       string
	AllowCwdFallback bool
	Verified         bool
	Token            string
}

// SplitPackage splits name@version on its last '@', so a scoped npm name
// (@scope/pkg@1.0.0) keeps its leading '@'.
func SplitPackage(spec string) (name, version string) {
	if i := strings.LastIndex(spec, "@"); i > 0 {
		return spec[:i], spec[i+1:]
	}
	return spec, ""
}

// AddDependency records the package and returns its id with the name and
// version it was recorded under. store.ErrApprovalTokenRequired comes back
// unwrapped so the CLI can prompt for the token.
func AddDependency(st *store.Store, req AddDependencyRequest) (id int64, name, version string, err error) {
	ecosystem := strings.TrimSpace(req.Ecosystem)
	name, version = strings.TrimSpace(req.Name), req.Version
	if version == "" {
		name, version = SplitPackage(name)
	}
	if ecosystem == "" || name == "" {
		return 0, "", "", ErrDependencyIncomplete
	}
	var projectID *int64
	if req.TaskID != nil {
		t, err := st.GetTask(*req.TaskID)
		if err != nil {
			return 0, "", "", err
		}
		if t.ProjectID.Valid {
			projectID = &t.ProjectID.Int64
		}
	}
	if req.ProjectArg != "" || projectID == nil {
		if projectID, err = ResolveProject(st, req.ProjectArg, req.AllowCwdFallback); err != nil {
			return 0, "", "", err
		}
	}
	id, err = st.AddDependencyWithToken(req.TaskID, projectID, ecosystem, name, version, req.Verified, req.Token)
	return id, name, version, err
}

// VerifyDependency marks a dependency verified: a person's confirmation that
// the package is real (or Token's holder). store.ErrApprovalTokenRequired
// comes back unwrapped.
func VerifyDependency(st *store.Store, id int64, token string) error {
	return st.VerifyDependencyWithToken(id, token)
}
