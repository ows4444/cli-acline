// This file holds the smaller task, roadmap and catalogue use cases that
// `acline` and the MCP server each implemented: deferring and linking tasks,
// adding criteria, updating a milestone, adding a role, a feature or an eval.
// Each adapter checked its own required fields and existence, and resolved
// projects its own way.
package app

import (
	"errors"
	"strings"

	"acline/internal/store"
)

var (
	// ErrDeferReasonRequired is a task deferred without a reason.
	ErrDeferReasonRequired = errors.New("a reason is required when deferring a task")
	// ErrCriterionTextRequired is a criterion with blank text.
	ErrCriterionTextRequired = errors.New("criterion text is required")
	// ErrRoleNeedsProject is a role added outside any project; the built-in
	// global roles cover that case.
	ErrRoleNeedsProject = errors.New("a role needs a project: the 7 built-in roles (developer/qa/designer/manager/scrummaster/architect/security) already cover the global case")
	// ErrNameRequired is a feature with a blank name.
	ErrNameRequired = errors.New("name is required")
)

// DeferTask marks a task deferred with a reason and an optional revisit
// trigger, or with clear lifts the flag.
func DeferTask(st *store.Store, id int64, clear bool, reason, trigger string) error {
	if clear {
		return st.DeferTask(id, false, "", "")
	}
	if strings.TrimSpace(reason) == "" {
		return ErrDeferReasonRequired
	}
	return st.DeferTask(id, true, reason, trigger)
}

// LinkTasks links two existing tasks (depends_on, blocks or related); a
// missing task is store.ErrNotFound, a cycle store.ErrLinkCycle.
func LinkTasks(st *store.Store, taskID int64, relation string, otherID int64) (int64, error) {
	for _, id := range []int64{taskID, otherID} {
		if _, err := st.GetTask(id); err != nil {
			return 0, err
		}
	}
	return st.AddLink(taskID, otherID, relation)
}

// AddCriterion adds an acceptance criterion to a task and returns its id and
// the EARS pattern it follows ("" if none).
func AddCriterion(st *store.Store, taskID int64, text string) (int64, string, error) {
	if strings.TrimSpace(text) == "" {
		return 0, "", ErrCriterionTextRequired
	}
	return st.AddCriterion(taskID, text)
}

// UpdateMilestone sets a milestone's status and/or target date, both or
// neither (store.UpdateMilestone).
func UpdateMilestone(st *store.Store, id int64, status, target string) error {
	if status == "" && target == "" {
		return ErrNothingToUpdate
	}
	return st.UpdateMilestone(id, status, target)
}

// AddRoleRequest is a project-scoped role. A can_approve role needs a person
// (or Token).
type AddRoleRequest struct {
	Name             string
	Kind             string
	CanApprove       bool
	StageOrder       *int64
	Description      string
	Token            string
	ProjectArg       string
	AllowCwdFallback bool
}

// AddRole adds a role to the request's project; there must be one.
func AddRole(st *store.Store, req AddRoleRequest) (int64, error) {
	if strings.TrimSpace(req.Name) == "" {
		return 0, ErrNameRequired
	}
	projectID, err := ResolveProject(st, req.ProjectArg, req.AllowCwdFallback)
	if err != nil {
		return 0, err
	}
	if projectID == nil {
		return 0, ErrRoleNeedsProject
	}
	return st.AddRoleWithToken(*projectID, req.Name, req.Kind, req.CanApprove, req.StageOrder, req.Description, req.Token)
}

// AddFeatureRequest records a capability; Status "" is "live".
type AddFeatureRequest struct {
	Name             string
	Status           string
	OwnerArea        string
	SourcePointer    string
	Description      string
	ProjectArg       string
	AllowCwdFallback bool
}

// AddFeature records a feature in the request's project.
func AddFeature(st *store.Store, req AddFeatureRequest) (int64, error) {
	if strings.TrimSpace(req.Name) == "" {
		return 0, ErrNameRequired
	}
	projectID, err := ResolveProject(st, req.ProjectArg, req.AllowCwdFallback)
	if err != nil {
		return 0, err
	}
	return st.AddFeature(req.Name, req.Status, store.FeatureOpts{
		OwnerArea: req.OwnerArea, SourcePointer: req.SourcePointer, Description: req.Description, ProjectID: projectID,
	})
}

// RecordEvalRequest is a measured pass rate (0..1) for a suite. The eval
// belongs to ProjectArg, else the task's project, else (with
// AllowCwdFallback) the current one: autonomy promotion only counts an eval
// from the task's own project.
type RecordEvalRequest struct {
	TaskID           *int64
	Suite            string
	PassRate         float64
	SampleSize       int64
	Note             string
	ProjectArg       string
	AllowCwdFallback bool
}

// RecordEval records the eval.
func RecordEval(st *store.Store, req RecordEvalRequest) (int64, error) {
	var projectID *int64
	if req.TaskID != nil {
		t, err := st.GetTask(*req.TaskID)
		if err != nil {
			return 0, err
		}
		if t.ProjectID.Valid {
			projectID = &t.ProjectID.Int64
		}
	}
	if req.ProjectArg != "" || projectID == nil {
		var err error
		if projectID, err = ResolveProject(st, req.ProjectArg, req.AllowCwdFallback); err != nil {
			return 0, err
		}
	}
	return st.AddEval(req.TaskID, projectID, req.Suite, req.PassRate, req.SampleSize, req.Note)
}
