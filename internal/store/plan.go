package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

// A plan is a proposed task graph for an approved spec. It is DATA, not tasks:
// nothing in it can be routed, briefed or launched until a person approves it,
// and approval creates the real tasks, links, parents, criteria and milestones
// in one transaction. The rules are below and in docs/ARCHITECTURE.md ("Authority").

const (
	// MaxPlanItems caps a plan so a runaway proposal cannot bury its reviewer.
	MaxPlanItems    = 30
	maxPlanCriteria = 20
	maxPlanBytes    = 1 << 20
	maxTitleLen     = 200
)

var (
	// ErrAgentCannotDecidePlan is returned when an agent tries to edit or
	// approve a plan. Like memory review and spec approval, accepting a plan is
	// a person's decision; an agent may only propose one.
	ErrAgentCannotDecidePlan = errors.New("an agent cannot edit or approve a plan: a person must decide")
	// ErrPlanStale means the spec changed after the plan was proposed.
	ErrPlanStale = errors.New("the spec changed after this plan was proposed; revise the plan against the current spec")
	// ErrPlanDraftExists means a spec already has a draft plan awaiting a decision.
	ErrPlanDraftExists = errors.New("this spec already has a draft plan")

	planRefPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,20}$`)
	// PlanAutonomies are the autonomy levels a plan may ask for. "auto" is
	// deliberately absent: it is earned from a measured eval (PromoteAutonomy),
	// never granted by a proposal.
	planAutonomies = map[string]bool{"hitl": true, "hotl": true}
	validPlanSizes = map[string]bool{"S": true, "M": true, "L": true}
)

var planTablesDDL = []string{
	`CREATE TABLE IF NOT EXISTS plans (
		id           INTEGER PRIMARY KEY,
		spec_id      INTEGER NOT NULL REFERENCES specs(id),
		spec_version INTEGER NOT NULL,
		status       TEXT NOT NULL DEFAULT 'draft',
		version      INTEGER NOT NULL DEFAULT 1,
		note         TEXT,
		project_id   INTEGER REFERENCES projects(id),
		actor_type   TEXT,
		actor_id     TEXT,
		model        TEXT,
		created_at   TEXT NOT NULL,
		decided_at   TEXT
	)`,
	`CREATE TABLE IF NOT EXISTS plan_items (
		id          INTEGER PRIMARY KEY,
		plan_id     INTEGER NOT NULL REFERENCES plans(id),
		ref         TEXT NOT NULL,
		title       TEXT NOT NULL,
		description TEXT NOT NULL DEFAULT '',
		area        TEXT,
		type        TEXT,
		risk        TEXT NOT NULL DEFAULT 'low',
		autonomy    TEXT NOT NULL DEFAULT 'hotl',
		parent_ref  TEXT,
		milestone   TEXT,
		size        TEXT,
		dropped     INTEGER NOT NULL DEFAULT 0,
		position    INTEGER NOT NULL,
		task_id     INTEGER REFERENCES tasks(id),
		UNIQUE (plan_id, ref)
	)`,
	`CREATE TABLE IF NOT EXISTS plan_edges (
		id             INTEGER PRIMARY KEY,
		plan_id        INTEGER NOT NULL REFERENCES plans(id),
		item_ref       TEXT NOT NULL,
		depends_on_ref TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS plan_criteria (
		id           INTEGER PRIMARY KEY,
		plan_item_id INTEGER NOT NULL REFERENCES plan_items(id),
		text         TEXT NOT NULL
	)`,
}

// PlanItemInput is one proposed task.
type PlanItemInput struct {
	Ref         string   `json:"ref"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Area        string   `json:"area,omitempty"`
	Type        string   `json:"type,omitempty"`
	Risk        string   `json:"risk,omitempty"`
	Autonomy    string   `json:"autonomy,omitempty"`
	Parent      string   `json:"parent,omitempty"`
	Milestone   string   `json:"milestone,omitempty"`
	Size        string   `json:"size,omitempty"`
	DependsOn   []string `json:"depends_on,omitempty"`
	Criteria    []string `json:"criteria,omitempty"`
}

// PlanInput is a whole proposed plan.
type PlanInput struct {
	Note  string          `json:"note,omitempty"`
	Items []PlanItemInput `json:"items"`
}

// ParsePlanJSON reads a plan document. Unknown fields are an error: a typo such
// as "depend_on" must not silently drop a dependency.
func ParsePlanJSON(r io.Reader) (PlanInput, error) {
	var in PlanInput
	dec := json.NewDecoder(io.LimitReader(r, maxPlanBytes+1))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return in, fmt.Errorf("reading plan: %w", err)
	}
	if dec.More() {
		return in, errors.New("reading plan: unexpected data after the plan document")
	}
	return in, nil
}

// normalizeAndValidate applies defaults, scrubs text and checks every rule that
// does not need the database. It edits in place.
func normalizeAndValidate(in *PlanInput) error {
	if len(in.Items) == 0 {
		return errors.New("a plan needs at least one item")
	}
	if len(in.Items) > MaxPlanItems {
		return fmt.Errorf("a plan is capped at %d items (got %d); split it", MaxPlanItems, len(in.Items))
	}
	in.Note = scrubText(strings.TrimSpace(in.Note))
	refs := map[string]int{}
	for i := range in.Items {
		it := &in.Items[i]
		it.Ref = strings.TrimSpace(it.Ref)
		if !planRefPattern.MatchString(it.Ref) {
			return fmt.Errorf("item %d: ref %q must be 1-20 letters, digits, '.', '_' or '-'", i+1, it.Ref)
		}
		if _, dup := refs[it.Ref]; dup {
			return fmt.Errorf("duplicate ref %q", it.Ref)
		}
		refs[it.Ref] = i
	}
	for i := range in.Items {
		it := &in.Items[i]
		at := fmt.Sprintf("item %s", it.Ref)
		it.Title = scrubText(strings.TrimSpace(it.Title))
		if it.Title == "" || len(it.Title) > maxTitleLen {
			return fmt.Errorf("%s: title must be 1-%d characters", at, maxTitleLen)
		}
		it.Description = scrubText(strings.TrimSpace(it.Description))
		it.Area, it.Milestone = strings.TrimSpace(it.Area), strings.TrimSpace(it.Milestone)
		if it.Type != "" && !ValidTaskTypes[it.Type] {
			return fmt.Errorf("%s: invalid type %q", at, it.Type)
		}
		if it.Risk == "" {
			it.Risk = "low"
		}
		if !ValidRisks[it.Risk] {
			return fmt.Errorf("%s: invalid risk %q", at, it.Risk)
		}
		if it.Autonomy == "" {
			it.Autonomy = "hotl"
		}
		if it.Autonomy == "auto" {
			return fmt.Errorf("%s: a plan cannot grant autonomy \"auto\"; it is earned from a measured eval after the task exists", at)
		}
		if !planAutonomies[it.Autonomy] {
			return fmt.Errorf("%s: invalid autonomy %q (want hitl or hotl)", at, it.Autonomy)
		}
		if it.Size != "" && !validPlanSizes[it.Size] {
			return fmt.Errorf("%s: invalid size %q (want S, M or L)", at, it.Size)
		}
		if it.Parent != "" {
			if it.Parent == it.Ref {
				return fmt.Errorf("%s: an item cannot be its own parent", at)
			}
			if _, ok := refs[it.Parent]; !ok {
				return fmt.Errorf("%s: parent %q is not in the plan", at, it.Parent)
			}
		}
		seen := map[string]bool{}
		for _, d := range it.DependsOn {
			if d == it.Ref {
				return fmt.Errorf("%s: an item cannot depend on itself", at)
			}
			if _, ok := refs[d]; !ok {
				return fmt.Errorf("%s: depends_on %q is not in the plan", at, d)
			}
			if seen[d] {
				return fmt.Errorf("%s: depends_on lists %q twice", at, d)
			}
			seen[d] = true
		}
		if len(it.Criteria) > maxPlanCriteria {
			return fmt.Errorf("%s: at most %d criteria per item", at, maxPlanCriteria)
		}
		for j, c := range it.Criteria {
			c = scrubText(strings.TrimSpace(c))
			if c == "" {
				return fmt.Errorf("%s: criterion %d is empty", at, j+1)
			}
			it.Criteria[j] = c
		}
	}
	if err := checkPlanAcyclic(in); err != nil {
		return err
	}
	return nil
}

// checkPlanAcyclic rejects dependency cycles and parent loops.
func checkPlanAcyclic(in *PlanInput) error {
	indeg := map[string]int{}
	after := map[string][]string{} // dep -> items that wait on it
	for _, it := range in.Items {
		indeg[it.Ref] += 0
		for _, d := range it.DependsOn {
			after[d] = append(after[d], it.Ref)
			indeg[it.Ref]++
		}
	}
	var queue []string
	for _, it := range in.Items {
		if indeg[it.Ref] == 0 {
			queue = append(queue, it.Ref)
		}
	}
	done := 0
	for len(queue) > 0 {
		r := queue[0]
		queue = queue[1:]
		done++
		for _, w := range after[r] {
			if indeg[w]--; indeg[w] == 0 {
				queue = append(queue, w)
			}
		}
	}
	if done != len(in.Items) {
		return errors.New("the plan's dependencies contain a cycle")
	}
	parent := map[string]string{}
	for _, it := range in.Items {
		parent[it.Ref] = it.Parent
	}
	for _, it := range in.Items {
		cur := it.Ref
		for hops := 0; parent[cur] != ""; hops++ {
			if hops > len(in.Items) {
				return fmt.Errorf("item %s: parents form a loop", it.Ref)
			}
			cur = parent[cur]
		}
	}
	return nil
}

// Plan is a proposed task graph.
type Plan struct {
	ID          int64
	SpecID      int64
	SpecVersion int
	Status      string // draft | approved | rejected | superseded
	Version     int
	Note        sql.NullString
	ProjectID   sql.NullInt64
	ActorType   sql.NullString
	ActorID     sql.NullString
	CreatedAt   string
	DecidedAt   sql.NullString
}

// PlanItem is one proposed task, with its edges and criteria attached.
type PlanItem struct {
	ID          int64
	PlanID      int64
	Ref         string
	Title       string
	Description string
	Area        sql.NullString
	Type        sql.NullString
	Risk        string
	Autonomy    string
	Parent      sql.NullString
	Milestone   sql.NullString
	Size        sql.NullString
	Dropped     bool
	Position    int
	TaskID      sql.NullInt64 // set once the plan is approved
	DependsOn   []string
	Criteria    []string
}

const planColumns = `id, spec_id, spec_version, status, version, note, project_id, actor_type, actor_id, created_at, decided_at`

func scanPlan(row interface{ Scan(...any) error }) (*Plan, error) {
	p := &Plan{}
	err := row.Scan(&p.ID, &p.SpecID, &p.SpecVersion, &p.Status, &p.Version, &p.Note, &p.ProjectID, &p.ActorType, &p.ActorID, &p.CreatedAt, &p.DecidedAt)
	return p, err
}

// GetPlan loads one plan.
func (s *Store) GetPlan(id int64) (*Plan, error) {
	p, err := scanPlan(s.DB.QueryRow(`SELECT `+planColumns+` FROM plans WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("plan #%d: %w", id, ErrNotFound)
	}
	return p, err
}

// ListPlans lists plans, newest first, optionally for one spec and/or status.
func (s *Store) ListPlans(specID *int64, status string) ([]Plan, error) {
	return s.ListProjectPlans(specID, status, nil)
}

// ListProjectPlans is ListPlans limited to the plans of one project's specs
// (nil: every project).
func (s *Store) ListProjectPlans(specID *int64, status string, projectID *int64) ([]Plan, error) {
	q := `SELECT ` + planColumns + ` FROM plans`
	var where []string
	var args []any
	if projectID != nil {
		where = append(where, `spec_id IN (SELECT id FROM specs WHERE project_id = ?)`)
		args = append(args, *projectID)
	}
	if specID != nil {
		where = append(where, `spec_id = ?`)
		args = append(args, *specID)
	}
	if status != "" {
		where = append(where, `status = ?`)
		args = append(args, status)
	}
	if len(where) > 0 {
		q += ` WHERE ` + strings.Join(where, ` AND `)
	}
	rows, err := s.DB.Query(q+` ORDER BY id DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Plan
	for rows.Next() {
		p, err := scanPlan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// PlanItems returns a plan's items in proposal order, edges and criteria attached.
func (s *Store) PlanItems(planID int64) ([]PlanItem, error) {
	rows, err := s.DB.Query(`SELECT id, plan_id, ref, title, description, area, type, risk, autonomy, parent_ref, milestone, size, dropped, position, task_id
		FROM plan_items WHERE plan_id = ? ORDER BY position`, planID)
	if err != nil {
		return nil, err
	}
	var items []PlanItem
	for rows.Next() {
		var it PlanItem
		var dropped int
		if err := rows.Scan(&it.ID, &it.PlanID, &it.Ref, &it.Title, &it.Description, &it.Area, &it.Type, &it.Risk, &it.Autonomy,
			&it.Parent, &it.Milestone, &it.Size, &dropped, &it.Position, &it.TaskID); err != nil {
			rows.Close()
			return nil, err
		}
		it.Dropped = dropped != 0
		items = append(items, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	byRef := map[string]*PlanItem{}
	for i := range items {
		byRef[items[i].Ref] = &items[i]
	}
	erows, err := s.DB.Query(`SELECT item_ref, depends_on_ref FROM plan_edges WHERE plan_id = ? ORDER BY id`, planID)
	if err != nil {
		return nil, err
	}
	for erows.Next() {
		var a, b string
		if err := erows.Scan(&a, &b); err != nil {
			erows.Close()
			return nil, err
		}
		if it := byRef[a]; it != nil {
			it.DependsOn = append(it.DependsOn, b)
		}
	}
	erows.Close()
	crows, err := s.DB.Query(`SELECT c.plan_item_id, c.text FROM plan_criteria c JOIN plan_items i ON i.id = c.plan_item_id WHERE i.plan_id = ? ORDER BY c.id`, planID)
	if err != nil {
		return nil, err
	}
	defer crows.Close()
	byID := map[int64]*PlanItem{}
	for i := range items {
		byID[items[i].ID] = &items[i]
	}
	for crows.Next() {
		var id int64
		var text string
		if err := crows.Scan(&id, &text); err != nil {
			return nil, err
		}
		if it := byID[id]; it != nil {
			it.Criteria = append(it.Criteria, text)
		}
	}
	return items, crows.Err()
}

// insertPlan writes a validated plan and its items inside tx.
func (s *Store) insertPlan(tx *sql.Tx, spec *Spec, version int, in *PlanInput) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := tx.Exec(`INSERT INTO plans (spec_id, spec_version, status, version, note, project_id, actor_type, actor_id, model, created_at)
		VALUES (?, ?, 'draft', ?, ?, ?, ?, ?, ?, ?)`,
		spec.ID, spec.Version, version, nullStr(in.Note), spec.ProjectID, s.Actor.Type, s.Actor.ID, nullStr(s.Actor.Model), now)
	if err != nil {
		return 0, err
	}
	planID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	for pos, it := range in.Items {
		ires, err := tx.Exec(`INSERT INTO plan_items (plan_id, ref, title, description, area, type, risk, autonomy, parent_ref, milestone, size, position)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			planID, it.Ref, it.Title, it.Description, nullStr(it.Area), nullStr(it.Type), it.Risk, it.Autonomy,
			nullStr(it.Parent), nullStr(it.Milestone), nullStr(it.Size), pos)
		if err != nil {
			return 0, err
		}
		itemID, _ := ires.LastInsertId()
		for _, d := range it.DependsOn {
			if _, err := tx.Exec(`INSERT INTO plan_edges (plan_id, item_ref, depends_on_ref) VALUES (?, ?, ?)`, planID, it.Ref, d); err != nil {
				return 0, err
			}
		}
		for _, c := range it.Criteria {
			if _, err := tx.Exec(`INSERT INTO plan_criteria (plan_item_id, text) VALUES (?, ?)`, itemID, c); err != nil {
				return 0, err
			}
		}
	}
	return planID, nil
}

func (s *Store) approvedSpec(specID int64) (*Spec, error) {
	spec, err := s.GetSpec(specID)
	if err != nil {
		return nil, err
	}
	if spec.Status != "approved" {
		return nil, fmt.Errorf("spec #%d is %s: only an approved spec can be planned", specID, spec.Status)
	}
	return spec, nil
}

// ProposePlan records a draft plan for an approved spec. Anyone, including an
// agent, may propose; nothing is created until a person approves it.
func (s *Store) ProposePlan(specID int64, in PlanInput) (int64, error) {
	if err := normalizeAndValidate(&in); err != nil {
		return 0, err
	}
	spec, err := s.approvedSpec(specID)
	if err != nil {
		return 0, err
	}
	var draft int64
	if err := s.DB.QueryRow(`SELECT COALESCE(MAX(id), 0) FROM plans WHERE spec_id = ? AND status = 'draft'`, specID).Scan(&draft); err != nil {
		return 0, err
	}
	if draft != 0 {
		return 0, fmt.Errorf("%w (#%d): approve, reject or revise it first", ErrPlanDraftExists, draft)
	}
	var version int
	if err := s.DB.QueryRow(`SELECT COALESCE(MAX(version), 0) + 1 FROM plans WHERE spec_id = ?`, specID).Scan(&version); err != nil {
		return 0, err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	id, err := s.insertPlan(tx, spec, version, &in)
	if err != nil {
		return 0, err
	}
	if _, err := s.logEventTx(tx, nil, nil, nil, "plan_proposed", fmt.Sprintf("plan #%d (v%d, %d items) proposed for spec #%d", id, version, len(in.Items), specID)); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// RevisePlan replaces a draft with a new version against the spec's current
// text, marking the old one superseded. An approved plan is immutable.
func (s *Store) RevisePlan(planID int64, in PlanInput) (int64, error) {
	if err := normalizeAndValidate(&in); err != nil {
		return 0, err
	}
	old, err := s.GetPlan(planID)
	if err != nil {
		return 0, err
	}
	if old.Status != "draft" {
		return 0, fmt.Errorf("plan #%d is %s: only a draft can be revised", planID, old.Status)
	}
	spec, err := s.approvedSpec(old.SpecID)
	if err != nil {
		return 0, err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.Exec(`UPDATE plans SET status = 'superseded', decided_at = ? WHERE id = ?`, now, planID); err != nil {
		return 0, err
	}
	id, err := s.insertPlan(tx, spec, old.Version+1, &in)
	if err != nil {
		return 0, err
	}
	if _, err := s.logEventTx(tx, nil, nil, nil, "plan_proposed", fmt.Sprintf("plan #%d (v%d) revises #%d for spec #%d", id, old.Version+1, planID, old.SpecID)); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

func (s *Store) draftPlan(planID int64) (*Plan, error) {
	p, err := s.GetPlan(planID)
	if err != nil {
		return nil, err
	}
	if p.Status != "draft" {
		return nil, fmt.Errorf("plan #%d is %s: only a draft can be changed", planID, p.Status)
	}
	return p, nil
}

// PlanItemEdit changes a draft item. A nil field is left alone; Drop removes
// the item from what approval will create.
type PlanItemEdit struct {
	Title     *string
	Area      *string
	Risk      *string
	Autonomy  *string
	Size      *string
	Milestone *string
	Drop      *bool
}

// EditPlanItem changes one item of a draft plan before approval. A person's
// action: an agent revises by proposing a new version instead.
func (s *Store) EditPlanItem(planID int64, ref string, e PlanItemEdit, token string) error {
	if err := s.requirePerson(token, ErrAgentCannotDecidePlan); err != nil {
		return err
	}
	if _, err := s.draftPlan(planID); err != nil {
		return err
	}
	var set []string
	var args []any
	add := func(col string, v any) { set = append(set, col+" = ?"); args = append(args, v) }
	if e.Title != nil {
		t := scrubText(strings.TrimSpace(*e.Title))
		if t == "" || len(t) > maxTitleLen {
			return fmt.Errorf("title must be 1-%d characters", maxTitleLen)
		}
		add("title", t)
	}
	if e.Area != nil {
		add("area", nullStr(strings.TrimSpace(*e.Area)))
	}
	if e.Milestone != nil {
		add("milestone", nullStr(strings.TrimSpace(*e.Milestone)))
	}
	if e.Risk != nil {
		if !ValidRisks[*e.Risk] {
			return fmt.Errorf("invalid risk %q", *e.Risk)
		}
		add("risk", *e.Risk)
	}
	if e.Autonomy != nil {
		if *e.Autonomy == "auto" {
			return errors.New("a plan cannot grant autonomy \"auto\"; it is earned from a measured eval after the task exists")
		}
		if !planAutonomies[*e.Autonomy] {
			return fmt.Errorf("invalid autonomy %q (want hitl or hotl)", *e.Autonomy)
		}
		add("autonomy", *e.Autonomy)
	}
	if e.Size != nil {
		if *e.Size != "" && !validPlanSizes[*e.Size] {
			return fmt.Errorf("invalid size %q (want S, M or L)", *e.Size)
		}
		add("size", nullStr(*e.Size))
	}
	if e.Drop != nil {
		add("dropped", boolToInt(*e.Drop))
	}
	if len(set) == 0 {
		return errors.New("nothing to change")
	}
	res, err := s.DB.Exec(`UPDATE plan_items SET `+strings.Join(set, ", ")+` WHERE plan_id = ? AND ref = ?`, append(args, planID, ref)...)
	if err != nil {
		return err
	}
	if err := mustExist(res, "plan item "+ref+" of plan", planID); err != nil {
		return err
	}
	s.LogEventGlobal("plan_edited", fmt.Sprintf("plan #%d item %s edited", planID, ref))
	return nil
}

// CreatedTask pairs a plan item with the task approval created for it.
type CreatedTask struct {
	Ref    string
	TaskID int64
}

// ApprovePlan turns a draft plan into real tasks: one per kept item, with its
// criteria, dependency links, parent, milestone, and the spec it came from, all
// in one transaction (all or nothing). Only a person may do it.
//
// Dropped items are skipped; edges to them are removed and their children lose
// the parent. Every created task is 'todo', at the item's risk and its
// autonomy (hotl unless the item said hitl; never auto).
func (s *Store) ApprovePlan(planID int64, token string) ([]CreatedTask, error) {
	if err := s.requirePerson(token, ErrAgentCannotDecidePlan); err != nil {
		return nil, err
	}
	plan, err := s.draftPlan(planID)
	if err != nil {
		return nil, err
	}
	spec, err := s.approvedSpec(plan.SpecID)
	if err != nil {
		return nil, err
	}
	if spec.Version != plan.SpecVersion {
		return nil, ErrPlanStale
	}
	all, err := s.PlanItems(planID)
	if err != nil {
		return nil, err
	}
	var items []PlanItem
	kept := map[string]bool{}
	for _, it := range all {
		if !it.Dropped {
			items = append(items, it)
			kept[it.Ref] = true
		}
	}
	if len(items) == 0 {
		return nil, errors.New("every item was dropped: nothing to approve (reject the plan instead)")
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	milestones := map[string]int64{}
	milestoneID := func(name string) (*int64, error) {
		if name == "" {
			return nil, nil
		}
		if id, ok := milestones[name]; ok {
			return &id, nil
		}
		var id int64
		err := tx.QueryRow(`SELECT id FROM milestones WHERE name = ? AND project_id IS ?`, name, nullInt64Arg(plan.ProjectID)).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			now := time.Now().UTC().Format(time.RFC3339)
			res, ierr := tx.Exec(`INSERT INTO milestones (name, status, project_id, actor_type, actor_id, model, created_at, updated_at)
				VALUES (?, 'planned', ?, ?, ?, ?, ?, ?)`, name, plan.ProjectID, s.Actor.Type, s.Actor.ID, nullStr(s.Actor.Model), now, now)
			if ierr != nil {
				return nil, ierr
			}
			id, err = res.LastInsertId()
		}
		if err != nil {
			return nil, err
		}
		milestones[name] = id
		return &id, nil
	}

	taskOf := map[string]int64{}
	var created []CreatedTask
	specID := spec.ID
	var projectID *int64
	if plan.ProjectID.Valid {
		projectID = &plan.ProjectID.Int64
	}
	for _, it := range items {
		mid, err := milestoneID(it.Milestone.String)
		if err != nil {
			return nil, err
		}
		tid, err := s.insertTask(tx, it.Title, it.Description, "normal", TaskOpts{
			Area: it.Area.String, Type: it.Type.String, Risk: it.Risk, Autonomy: it.Autonomy,
			SpecID: &specID, ProjectID: projectID, MilestoneID: mid,
		})
		if err != nil {
			return nil, fmt.Errorf("creating %s: %w", it.Ref, err)
		}
		taskOf[it.Ref] = tid
		created = append(created, CreatedTask{Ref: it.Ref, TaskID: tid})
		if _, err := tx.Exec(`UPDATE tasks SET plan_item_id = ? WHERE id = ?`, it.ID, tid); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`UPDATE plan_items SET task_id = ? WHERE id = ?`, tid, it.ID); err != nil {
			return nil, err
		}
		now := time.Now().UTC().Format(time.RFC3339)
		for _, c := range it.Criteria {
			if _, err := tx.Exec(`INSERT INTO task_criteria (task_id, text, pattern, created_at) VALUES (?, ?, ?, ?)`,
				tid, c, nullStr(DetectEARSPattern(c)), now); err != nil {
				return nil, err
			}
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	var links int
	for _, it := range items {
		if it.Parent.Valid && kept[it.Parent.String] {
			if _, err := tx.Exec(`UPDATE tasks SET parent_id = ? WHERE id = ?`, taskOf[it.Parent.String], taskOf[it.Ref]); err != nil {
				return nil, err
			}
		}
		for _, d := range it.DependsOn {
			if !kept[d] {
				continue
			}
			if _, err := tx.Exec(`INSERT INTO task_links (task_id, related_task_id, relation, created_at) VALUES (?, ?, 'depends_on', ?)`,
				taskOf[it.Ref], taskOf[d], now); err != nil {
				return nil, err
			}
			links++
		}
	}
	if _, err := tx.Exec(`UPDATE plans SET status = 'approved', decided_at = ? WHERE id = ?`, now, planID); err != nil {
		return nil, err
	}
	if _, err := s.logEventTx(tx, nil, nil, nil, "plan_approved",
		fmt.Sprintf("plan #%d approved: %d task(s), %d dependency link(s) created for spec #%d", planID, len(created), links, spec.ID)); err != nil {
		return nil, err
	}
	return created, tx.Commit()
}

// RejectPlan rejects a draft. Like rejecting memory it is the safe direction and
// needs no token, only a non-agent actor. The reason is kept as a note so
// `reflect` can learn from it; it is not trusted context on its own.
func (s *Store) RejectPlan(planID int64, note string) error {
	if s.Actor.Type == "agent" {
		return ErrAgentCannotDecidePlan
	}
	plan, err := s.draftPlan(planID)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.DB.Exec(`UPDATE plans SET status = 'rejected', decided_at = ? WHERE id = ?`, now, planID); err != nil {
		return err
	}
	msg := fmt.Sprintf("plan #%d rejected", planID)
	if note = strings.TrimSpace(note); note != "" {
		msg += ": " + note
		var pid *int64
		if plan.ProjectID.Valid {
			pid = &plan.ProjectID.Int64
		}
		_, _ = s.AddNote(pid, fmt.Sprintf("plan #%d for spec #%d was rejected: %s", planID, plan.SpecID, note), "conversation")
	}
	s.LogEventGlobal("plan_rejected", msg)
	return nil
}

// SpecsAwaitingPlan lists approved specs that have neither a live plan (draft or
// approved) nor any task, i.e. work a person has agreed to that nobody has
// started to break down. It backs the dashboard nudge.
func (s *Store) SpecsAwaitingPlan(projectID *int64) ([]Spec, error) {
	q := `SELECT ` + specColumns + ` FROM specs sp WHERE sp.status = 'approved'
		AND NOT EXISTS (SELECT 1 FROM plans p WHERE p.spec_id = sp.id AND p.status IN ('draft', 'approved'))
		AND NOT EXISTS (SELECT 1 FROM tasks t WHERE t.spec_id = sp.id)`
	var args []any
	if projectID != nil {
		q += ` AND sp.project_id = ?`
		args = append(args, *projectID)
	}
	rows, err := s.DB.Query(q+` ORDER BY sp.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Spec
	for rows.Next() {
		sp, err := scanSpec(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sp)
	}
	return out, rows.Err()
}

// DraftPlans lists plans awaiting a decision, oldest first.
func (s *Store) DraftPlans(projectID *int64) ([]Plan, error) {
	q := `SELECT ` + planColumns + ` FROM plans WHERE status = 'draft'`
	var args []any
	if projectID != nil {
		q += ` AND project_id = ?`
		args = append(args, *projectID)
	}
	rows, err := s.DB.Query(q+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Plan
	for rows.Next() {
		p, err := scanPlan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// PlanIsStale reports whether a draft plan can no longer be approved because
// its spec changed or is no longer approved. Approved and closed plans are
// never stale.
func (s *Store) PlanIsStale(p *Plan) bool {
	if p.Status != "draft" {
		return false
	}
	spec, err := s.GetSpec(p.SpecID)
	return err != nil || spec.Version != p.SpecVersion || spec.Status != "approved"
}
