package store

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"
)

type Approval struct {
	ID        int64
	TaskID    int64
	Kind      string
	Approver  string
	Decision  string
	Note      sql.NullString
	ActorType sql.NullString
	ActorID   sql.NullString
	Model     sql.NullString
	RoleID    sql.NullInt64
	CreatedAt string
}

type Check struct {
	ID        int64
	TaskID    int64
	Kind      string
	Status    string
	Detail    sql.NullString
	ActorType sql.NullString
	ActorID   sql.NullString
	RoleID    sql.NullInt64
	CreatedAt string
	// Source is CheckSourceRunner when acline ran the tool, CheckSourceManual
	// when someone recorded a result. TreeHash fingerprints the working tree the
	// check was about (internal/worktree), when known.
	Source   string
	TreeHash sql.NullString
}

// Where a check's result came from.
const (
	CheckSourceRunner = "runner" // acline ran the tool and read its exit code (`check run`)
	CheckSourceManual = "manual" // someone recorded a result (`check record`)
)

// CheckMeta is a check's provenance. The zero value is a hand-recorded check
// with no known tree.
type CheckMeta struct {
	Source   string // CheckSourceRunner or CheckSourceManual (default)
	TreeHash string // fingerprint of the tree the check ran against, "" if unknown
	// AdHocCommand marks a runner result whose command the caller chose (`check
	// run --cmd`) rather than the project's runner or the built-in default.
	AdHocCommand bool
}

// ErrAgentCannotChooseCheckCommand is returned when an agent records a runner
// result from a command it named itself. Runner evidence counts because the
// command is not the caller's choice; `--cmd true` would otherwise satisfy the
// high-risk gate.
var ErrAgentCannotChooseCheckCommand = errors.New("an agent cannot choose the command a check runs: only the project's runner (set by a person) or the default counts as runner evidence")

// AuthorizeAdHocCheckCommand reports whether the caller may run a check with a
// command of its own choosing. Adapters call it before running the command, so
// a refused caller never executes it; AddCheckWithMeta enforces it again.
func (s *Store) AuthorizeAdHocCheckCommand(token string) error {
	return s.requirePerson(token, ErrAgentCannotChooseCheckCommand)
}

// ValidApprovalKinds is deliberately just these two: code_review is the
// kind the gate itself checks (see EvaluateGate), and override is recorded
// by `task done --force` when a gate is bypassed. merge/deploy/stop existed
// with no corresponding enforcement logic anywhere — closer to CI/CD
// concerns than a local per-developer SQLite gate — and were dropped rather
// than left as unused surface.
var ValidApprovalKinds = map[string]bool{
	"code_review": true, "override": true,
}

var ValidApprovalDecisions = map[string]bool{
	"approved": true, "rejected": true, "overridden": true,
}

var ValidCheckKinds = map[string]bool{
	"test": true, "sast": true, "sca": true, "lint": true, "human_review": true, "eval": true,
}

var ValidCheckStatuses = map[string]bool{
	"pass": true, "fail": true, "skipped": true,
}

// AddApproval records an approval/rejection/override with no role attached.
// See AddApprovalWithRole for the roles-aware variant `approve`/`reject`
// use when --role is given.
func (s *Store) AddApproval(taskID int64, kind, approver, decision, note string) (int64, error) {
	return s.AddApprovalWithRole(taskID, nil, kind, approver, decision, note)
}

// AddApprovalWithRole is AddApproval plus an explicit role_id (see
// Store.ResolveRole) and, new alongside roles, actor_type/actor_id/model
// stamped the same way every other table already does -- approvals was the
// one table with no actor columns at all before this.
func (s *Store) AddApprovalWithRole(taskID int64, roleID *int64, kind, approver, decision, note string) (int64, error) {
	note = scrubText(note)
	if !ValidApprovalKinds[kind] {
		return 0, fmt.Errorf("invalid approval kind %q", kind)
	}
	if !ValidApprovalDecisions[decision] {
		return 0, fmt.Errorf("invalid approval decision %q", decision)
	}
	if approver == "" {
		approver = s.Actor.ID
	}
	sessionID := s.currentSessionID()
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	id, err := s.insertApproval(tx, sessionID, taskID, roleID, kind, approver, decision, note)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	if decision == "rejected" {
		s.draftFailurePattern(taskID, "rejection", note)
	}
	return id, nil
}

// sqlExecer is satisfied by *sql.DB and *sql.Tx.
type sqlExecer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// insertApproval writes the approval and its row_seal in the caller's tx.
func (s *Store) insertApproval(tx *sql.Tx, sessionID *int64, taskID int64, roleID *int64, kind, approver, decision, note string) (int64, error) {
	if approver == "" {
		approver = s.Actor.ID
	}
	if err := s.ensureSealWatermark(tx, sessionID); err != nil {
		return 0, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := tx.Exec(
		`INSERT INTO approvals (task_id, kind, approver, decision, note, actor_type, actor_id, model, role_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		taskID, kind, approver, decision, nullStr(note), s.Actor.Type, s.Actor.ID, nullStr(s.Actor.Model), nullInt(roleID), now,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	dig := approvalDigest(id, taskID, kind, approver, decision, note, s.Actor.Type, s.Actor.ID, s.Actor.Model, roleStr(roleID), now)
	if err := s.sealRow(tx, "approvals", id, taskID, sessionID, dig); err != nil {
		return 0, err
	}
	return id, nil
}

func (s *Store) ListApprovals(taskID int64) ([]Approval, error) {
	rows, err := s.DB.Query(
		`SELECT id, task_id, kind, approver, decision, note, actor_type, actor_id, model, role_id, created_at
		 FROM approvals WHERE task_id = ? ORDER BY id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Approval
	for rows.Next() {
		var a Approval
		if err := rows.Scan(&a.ID, &a.TaskID, &a.Kind, &a.Approver, &a.Decision, &a.Note,
			&a.ActorType, &a.ActorID, &a.Model, &a.RoleID, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// AddCheck records a check with no role attached. See AddCheckWithRole.
func (s *Store) AddCheck(taskID int64, kind, status, detail string) (int64, error) {
	return s.AddCheckWithRole(taskID, nil, kind, status, detail)
}

// ErrAgentCannotRecordHumanReview is returned when an agent tries to record a
// passing human_review check. That kind means a person looked at the work; if the
// agent whose work is being judged could record it, the gate would be satisfied
// by an assertion. A failing human_review is always accepted (it only tightens
// the gate).
var ErrAgentCannotRecordHumanReview = errors.New("an agent cannot record a passing human_review check: that kind is a person's review")

// AddCheckWithRole is AddCheck plus an explicit role_id (see Store.ResolveRole).
func (s *Store) AddCheckWithRole(taskID int64, roleID *int64, kind, status, detail string) (int64, error) {
	return s.AddCheckWithRoleAndToken(taskID, roleID, kind, status, detail, "")
}

// AddCheckWithRoleAndToken is AddCheckWithRole with the approval token, which
// recording a passing human_review needs whenever one is enabled.
func (s *Store) AddCheckWithRoleAndToken(taskID int64, roleID *int64, kind, status, detail, token string) (int64, error) {
	return s.AddCheckWithMeta(taskID, roleID, kind, status, detail, token, CheckMeta{})
}

// AddCheckWithMeta is AddCheckWithRoleAndToken plus the check's provenance: who
// produced the result (CheckSourceRunner for `check run`) and which tree it was
// about. Both are sealed with the rest of the record.
func (s *Store) AddCheckWithMeta(taskID int64, roleID *int64, kind, status, detail, token string, meta CheckMeta) (int64, error) {
	if meta.Source == "" {
		meta.Source = CheckSourceManual
	}
	if meta.Source != CheckSourceRunner && meta.Source != CheckSourceManual {
		return 0, fmt.Errorf("invalid check source %q", meta.Source)
	}
	if meta.Source == CheckSourceRunner && meta.AdHocCommand {
		if err := s.AuthorizeAdHocCheckCommand(token); err != nil {
			return 0, err
		}
	}
	if kind == "human_review" && status != "fail" {
		if err := s.requirePerson(token, ErrAgentCannotRecordHumanReview); err != nil {
			return 0, err
		}
	}
	detail = scrubText(detail)
	if !ValidCheckKinds[kind] {
		return 0, fmt.Errorf("invalid check kind %q", kind)
	}
	if !ValidCheckStatuses[status] {
		return 0, fmt.Errorf("invalid check status %q", status)
	}
	sessionID := s.currentSessionID()
	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err := s.ensureSealWatermark(tx, sessionID); err != nil {
		return 0, err
	}
	res, err := tx.Exec(
		`INSERT INTO checks (task_id, kind, status, detail, actor_type, actor_id, role_id, created_at, source, tree_hash)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		taskID, kind, status, nullStr(detail), s.Actor.Type, s.Actor.ID, nullInt(roleID), now, meta.Source, nullStr(meta.TreeHash),
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	dig := checkDigest(id, taskID, kind, status, detail, s.Actor.Type, s.Actor.ID, roleStr(roleID), now, meta.Source, meta.TreeHash)
	if err := s.sealRow(tx, "checks", id, taskID, sessionID, dig); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	if status == "fail" {
		s.draftFailurePattern(taskID, "check:"+kind, detail)
	}
	return id, nil
}

func (s *Store) ListChecks(taskID int64) ([]Check, error) {
	rows, err := s.DB.Query(
		`SELECT id, task_id, kind, status, detail, actor_type, actor_id, role_id, created_at, source, tree_hash
		 FROM checks WHERE task_id = ? ORDER BY id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Check
	for rows.Next() {
		var c Check
		if err := rows.Scan(&c.ID, &c.TaskID, &c.Kind, &c.Status, &c.Detail, &c.ActorType, &c.ActorID, &c.RoleID, &c.CreatedAt, &c.Source, &c.TreeHash); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// nullIntPtr converts a scanned sql.NullInt64 back to the *int64 shape the
// rest of the package's functions (ListRoles, hasApprovingRole, ...) take.
func nullIntPtr(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	id := v.Int64
	return &id
}

// hasApprovingRole reports whether projectID has its own (project-scoped)
// role with can_approve=1 -- the backward-compatibility hinge for
// EvaluateGate's role check. This deliberately does NOT count the 7 global
// (project_id NULL) seeded roles, even though two of them (manager,
// scrummaster) have can_approve=1: those are seeded into every store
// unconditionally, so counting them would turn role enforcement on for
// every existing project the moment this migration runs, rather than only
// for a project that has deliberately opted in with `acline role add
// --can-approve --project <name>`. A project can still use the global
// manager/scrummaster roles for attribution (--role manager) at any time;
// this only gates whether the *gate itself* starts requiring one.
func (s *Store) hasApprovingRole(projectID *int64) (bool, error) {
	if projectID == nil {
		return false, nil
	}
	var n int
	err := s.DB.QueryRow(
		`SELECT COUNT(*) FROM roles WHERE can_approve = 1 AND project_id = ?`,
		*projectID,
	).Scan(&n)
	return n > 0, err
}

// approvalSatisfiesRole reports whether roleID (an approval's role_id,
// possibly unset) names a role with can_approve=1.
func (s *Store) approvalSatisfiesRole(roleID sql.NullInt64) (bool, error) {
	if !roleID.Valid {
		return false, nil
	}
	var canApprove int
	err := s.DB.QueryRow(`SELECT can_approve FROM roles WHERE id = ?`, roleID.Int64).Scan(&canApprove)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return canApprove != 0, nil
}

// GateResult reports why a task may or may not be completed.
type GateResult struct {
	Blockers []string
	Warnings []string
}

func (g GateResult) OK() bool { return len(g.Blockers) == 0 }

// EvaluateGate applies the completion rules
// verification must be recorded, no check may be failing, and high-risk or
// human-in-the-loop tasks need a recorded human approval. It cannot tell whether
// the code changed since a check ran (it is not told where the code is); callers
// that know the working directory use EvaluateGateForTree.
func (s *Store) EvaluateGate(taskID int64) (GateResult, error) {
	return s.EvaluateGateForTree(taskID, "")
}

// EvaluateGateForTree is EvaluateGate with the current fingerprint of the
// working tree (internal/worktree; "" when unknown). It adds the provenance
// rules: a passing check that was hand-recorded by an agent draws a warning, one
// about a different tree draws a warning, and for high/critical risk the passing
// evidence must include a check acline itself ran (`check run`) and must not be
// about code that has since changed.
func (s *Store) EvaluateGateForTree(taskID int64, currentTree string) (GateResult, error) {
	var g GateResult

	t, err := s.GetTask(taskID)
	if err != nil {
		return g, err
	}

	checks, err := s.ListChecks(taskID)
	if err != nil {
		return g, err
	}
	// Checks are append-only, so the newest result for each check kind is its
	// current state. A repaired test run must supersede an earlier failure.
	latestChecks := make(map[string]Check)
	for _, c := range checks {
		latestChecks[c.Kind] = c
	}
	var passed, failed int
	for _, c := range latestChecks {
		switch c.Status {
		case "pass":
			passed++
		case "fail":
			failed++
		}
	}
	if len(checks) == 0 {
		g.Blockers = append(g.Blockers, "no verification recorded (use: acline check record)")
	}
	if failed > 0 {
		g.Blockers = append(g.Blockers, fmt.Sprintf("%d check(s) failing", failed))
	}
	if len(latestChecks) > 0 && passed == 0 && failed == 0 {
		g.Warnings = append(g.Warnings, "all recorded checks were skipped")
	}

	// A skipped security scan is not evidence. On high/critical-risk work a
	// sast/sca check whose newest result is "skipped" (tool not installed, no
	// runner) blocks completion until it is actually run; lower-risk tasks
	// keep the softer all-skipped warning above.
	if t.Risk == "high" || t.Risk == "critical" {
		for _, kind := range []string{"sast", "sca"} {
			if c, ok := latestChecks[kind]; ok && c.Status == "skipped" {
				g.Blockers = append(g.Blockers, fmt.Sprintf(
					"%s check was skipped (risk=%s) — install the tool and re-run: acline check run %d --kind %s",
					kind, t.Risk, taskID, kind))
			}
		}
	}

	s.evaluateEvidenceProvenance(t, checks, latestChecks, currentTree, &g)

	needsApproval := t.Autonomy == "hitl" || t.Risk == "high" || t.Risk == "critical"
	if needsApproval {
		approvals, err := s.ListApprovals(taskID)
		if err != nil {
			return g, err
		}
		// Approval decisions are also append-only. The newest code_review
		// decision controls; a later rejection revokes an earlier approval.
		// The override kind is a separate, unrelated gate (see task.go's
		// `task done --force`) and must not affect this one.
		var approved bool
		var latest Approval
		for _, a := range approvals {
			if a.Kind != "code_review" {
				continue
			}
			if a.Decision == "approved" || a.Decision == "rejected" {
				approved = a.Decision == "approved"
				latest = a
			}
		}
		if !approved {
			g.Blockers = append(g.Blockers, fmt.Sprintf(
				"human approval required (risk=%s, autonomy=%s) — use: acline approve %d --kind code_review",
				t.Risk, t.Autonomy, taskID))
		} else if roled, err := s.hasApprovingRole(nullIntPtr(t.ProjectID)); err != nil {
			return g, err
		} else if roled {
			// Roles feature (spec #2): once a project has configured at
			// least one can_approve role, the approving role must be one
			// of them, not just any human --by. A project that has never
			// touched roles has none with can_approve=1, so `roled` is
			// false and this branch never runs — old behavior, unchanged.
			ok, err := s.approvalSatisfiesRole(latest.RoleID)
			if err != nil {
				return g, err
			}
			if !ok {
				g.Blockers = append(g.Blockers, fmt.Sprintf(
					"the latest approval (#%d) has no can_approve role attached — use: acline approve %d --role <manager|scrummaster|...>",
					latest.ID, taskID))
			}
		}
	}

	if open, err := s.OpenPrerequisites(taskID); err != nil {
		return g, err
	} else {
		// A warning, not a blocker: finishing ahead of a prerequisite can be a
		// deliberate call, but it should never be a silent one.
		for _, p := range open {
			g.Warnings = append(g.Warnings, "prerequisite not done: "+prerequisiteLabel(p))
		}
	}

	criteria, err := s.ListCriteria(taskID)
	if err != nil {
		return g, err
	}
	var openCriteria int
	for _, c := range criteria {
		if !c.Done {
			openCriteria++
		}
	}
	if openCriteria > 0 {
		g.Warnings = append(g.Warnings, fmt.Sprintf("%d acceptance criterion/criteria still unchecked", openCriteria))
	}

	return g, nil
}

// TreeUnavailable is the current tree an adapter passes when it knows which
// directory the code is in but could not fingerprint it (worktree.Hash returned
// ""). Unlike "" (the caller does not know the directory), it is not a neutral
// answer: staleness cannot be checked, so high risk blocks and lower risk warns.
const TreeUnavailable = "unavailable"

// UnboundTreeNote is appended to a runner check's detail when its working tree
// could not be fingerprinted, so the record says why it is bound to no tree.
const UnboundTreeNote = "\n(working tree not fingerprinted: too large or unreadable; this result is not bound to a version of the code)"

// runnableKinds are the check kinds `acline check run` can produce.
var runnableKinds = map[string]bool{"test": true, "lint": true, "sast": true, "sca": true}

// evaluateEvidenceProvenance applies the source and tree rules to a task's
// newest result per kind (see EvaluateGateForTree). all is every check, oldest
// first, so a typed pass can be compared with what the runner last observed.
func (s *Store) evaluateEvidenceProvenance(t *Task, all []Check, latest map[string]Check, currentTree string, g *GateResult) {
	kinds := make([]string, 0, len(latest))
	for k := range latest {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)

	// The newest result acline itself produced, per kind.
	lastRunner := map[string]Check{}
	for _, c := range all {
		if c.Source == CheckSourceRunner {
			lastRunner[c.Kind] = c
		}
	}
	highRisk := t.Risk == "high" || t.Risk == "critical"
	treeKnown := currentTree != "" && currentTree != TreeUnavailable

	var passes, runnerPasses, stale, unbound int
	for _, kind := range kinds {
		c := latest[kind]
		if c.Status != "pass" {
			continue
		}
		passes++
		if c.Source == CheckSourceRunner {
			runnerPasses++
		} else if runnableKinds[kind] {
			// A typed pass must not erase a failure acline observed. An agent's
			// cannot; a person's is an override worth a warning below high risk.
			if r, ok := lastRunner[kind]; ok && r.Status == "fail" {
				msg := fmt.Sprintf(
					"%s: a pass recorded by hand (by %s) cannot replace the failure acline ran (check #%d) — re-run: acline check run %d --kind %s",
					kind, c.ActorID.String, r.ID, t.ID, kind)
				if highRisk || c.ActorType.String == "agent" {
					g.Blockers = append(g.Blockers, msg)
				} else {
					g.Warnings = append(g.Warnings, msg)
				}
			}
			// At high risk every runnable kind's pass must come from the runner, so
			// one runner pass of another kind cannot carry a typed one through.
			if highRisk || c.ActorType.String == "agent" {
				msg := fmt.Sprintf(
					"%s check passed by hand (recorded by %s), not run by acline — run: acline check run %d --kind %s",
					kind, c.ActorID.String, t.ID, kind)
				if highRisk {
					g.Blockers = append(g.Blockers, msg)
				} else {
					g.Warnings = append(g.Warnings, msg)
				}
			}
		}
		differs := treeKnown && c.TreeHash.Valid && c.TreeHash.String != currentTree
		if differs {
			g.Warnings = append(g.Warnings, fmt.Sprintf(
				"%s check passed for a different version of the code than the current one — the code changed since; re-run: acline check run %d --kind %s",
				kind, t.ID, kind))
		}
		if c.Source == CheckSourceRunner {
			switch {
			case differs:
				stale++
			case treeKnown && !c.TreeHash.Valid:
				// It recorded no tree, so it cannot show it is about this code.
				unbound++
			}
		}
	}

	if currentTree == TreeUnavailable && runnerPasses > 0 {
		msg := fmt.Sprintf(
			"the working tree could not be fingerprinted (too large or unreadable), so acline cannot tell whether the passing checks are about the current code — see `acline doctor`; risk=%s",
			t.Risk)
		if highRisk {
			g.Blockers = append(g.Blockers, msg)
		} else {
			g.Warnings = append(g.Warnings, msg)
		}
	}

	if !highRisk {
		return
	}
	if passes > 0 && runnerPasses == 0 {
		g.Blockers = append(g.Blockers, fmt.Sprintf(
			"risk=%s needs a passing check that acline ran, and every passing check was recorded by hand — run: acline check run %d --kind test",
			t.Risk, t.ID))
	}
	if stale > 0 {
		g.Blockers = append(g.Blockers, fmt.Sprintf(
			"the code has changed since the passing checks ran (risk=%s) — re-run: acline check run %d --kind test",
			t.Risk, t.ID))
	}
	if unbound > 0 {
		g.Blockers = append(g.Blockers, fmt.Sprintf(
			"%d passing check(s) acline ran recorded no working tree, so they cannot show they are about the current code (risk=%s) — re-run: acline check run %d --kind test",
			unbound, t.Risk, t.ID))
	}
}
