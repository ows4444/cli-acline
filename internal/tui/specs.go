package tui

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	tk "github.com/ows4444/tui"
	"github.com/ows4444/tui/ansi"
	"github.com/ows4444/tui/theme"
	"github.com/ows4444/tui/treeview"
	"github.com/ows4444/tui/widgets"

	"acline/internal/store"
)

// specsScreen is the work from intent to tasks: each spec, its plans, and
// the tasks an approved plan made (or the items a draft plan proposes), plus
// the decisions. Below the tree is the selected node: a spec can be compared
// with an earlier version, a draft plan's items edited before approval, and a
// draft approved or rejected as on the Review tab.
type specsScreen struct {
	tree  treeview.Model
	nodes map[string]specNode // by tree path ("0.1.2")

	prompt *prompt
	diff   string // a version comparison being shown, over the preview
	scroll int

	env           env
	width, height int
}

// specNode is what a tree row stands for. Exactly one of its fields is set.
type specNode struct {
	spec     *store.Spec
	plan     *store.Plan
	item     *store.PlanItem
	task     *store.Task
	decision *store.Decision
}

func (specsScreen) title() string { return "Specs" }

func (s specsScreen) load(e env) (screen, error) {
	s.env = e
	specs, err := e.st.ListSpecs("", e.projectID)
	if err != nil {
		return s, err
	}
	decisions, err := e.st.ListDecisions("", e.projectID)
	if err != nil {
		return s, err
	}
	s.nodes = map[string]specNode{}
	var roots []treeview.Node
	for i := range specs {
		sp := &specs[i]
		path := strconv.Itoa(i)
		s.nodes[path] = specNode{spec: sp}
		root := treeview.Node{Label: fmt.Sprintf("spec #%d %s  [%s v%d]", sp.ID, clean(sp.Title), sp.Status, sp.Version)}
		plans, err := e.st.ListPlans(&sp.ID, "")
		if err != nil {
			return s, err
		}
		for j := range plans {
			p := &plans[j]
			ppath := path + "." + strconv.Itoa(j)
			s.nodes[ppath] = specNode{plan: p}
			pnode := treeview.Node{Label: fmt.Sprintf("plan #%d v%d  [%s]", p.ID, p.Version, p.Status)}
			items, err := e.st.PlanItems(p.ID)
			if err != nil {
				return s, err
			}
			for k := range items {
				it := &items[k]
				ipath := ppath + "." + strconv.Itoa(len(pnode.Children))
				if it.TaskID.Valid {
					t, err := e.st.GetTask(it.TaskID.Int64)
					if err != nil {
						return s, err
					}
					s.nodes[ipath] = specNode{task: t}
					pnode.Children = append(pnode.Children, treeview.Node{Label: fmt.Sprintf("task #%d %s  [%s]", t.ID, clean(t.Title), t.Status)})
					continue
				}
				s.nodes[ipath] = specNode{item: it, plan: p}
				label := fmt.Sprintf("%s  %s  [%s/%s]", clean(it.Ref), clean(it.Title), it.Risk, it.Autonomy)
				if it.Dropped {
					label += "  (dropped)"
				}
				pnode.Children = append(pnode.Children, treeview.Node{Label: label})
			}
			root.Children = append(root.Children, pnode)
		}
		roots = append(roots, root)
	}
	if len(decisions) > 0 {
		path := strconv.Itoa(len(roots))
		group := treeview.Node{Label: fmt.Sprintf("Decisions (%d)", len(decisions))}
		for i := range decisions {
			d := &decisions[i]
			s.nodes[path+"."+strconv.Itoa(i)] = specNode{decision: d}
			group.Children = append(group.Children, treeview.Node{Label: fmt.Sprintf("decision #%d %s  [%s]", d.ID, clean(d.Title), d.Status)})
		}
		roots = append(roots, group)
	}
	cursor := s.tree.Cursor()
	expanded := map[string]bool{} // keep what was open, when the shape allows
	for _, r := range s.tree.VisibleRows() {
		if s.tree.IsExpanded(r.Path) {
			expanded[r.Path] = true
		}
	}
	s.tree = treeview.New(roots...)
	paths := make([]string, 0, len(expanded))
	for path := range expanded {
		paths = append(paths, path)
	}
	sort.Slice(paths, func(i, j int) bool { return len(paths[i]) < len(paths[j]) }) // parents first
	for _, path := range paths {
		s.tree = expandPath(s.tree, path)
	}
	s.tree.SetCursor(min(cursor, max(len(s.tree.VisibleRows())-1, 0)))
	return s, nil
}

// expandPath opens the node at path by moving the cursor to it and pressing
// right, the only way the tree exposes; the cursor is put back by the caller.
func expandPath(t treeview.Model, path string) treeview.Model {
	for i, r := range t.VisibleRows() {
		if r.Path == path {
			t.SetCursor(i)
			t, _ = t.Update(tk.Key{Type: tk.KeyRight})
			break
		}
	}
	return t
}

// selected is the node under the cursor.
func (s specsScreen) selected() (specNode, bool) {
	rows := s.tree.VisibleRows()
	if c := s.tree.Cursor(); c < len(rows) {
		n, ok := s.nodes[rows[c].Path]
		return n, ok
	}
	return specNode{}, false
}

func (s specsScreen) preview(n specNode) []string {
	switch {
	case n.spec != nil:
		p := specItem(*n.spec).preview
		if versions, err := s.env.st.ListSpecVersions(n.spec.ID); err == nil && len(versions) > 0 {
			p = append(p, "", sectionStyle.Render("Earlier versions"))
			for _, v := range versions {
				p = append(p, fmt.Sprintf("  v%d %s (%s, replaced %s)", v.Version, clean(v.Title), v.Status, v.CreatedAt))
			}
		}
		return p
	case n.item != nil:
		it := n.item
		p := []string{
			sectionStyle.Render(fmt.Sprintf("Item %s of plan #%d (%s)", clean(it.Ref), n.plan.ID, n.plan.Status)),
			clean(it.Title),
			fmt.Sprintf("risk %s, autonomy %s, area %s, size %s, milestone %s", it.Risk, it.Autonomy, clean(it.Area.String), clean(it.Size.String), clean(it.Milestone.String)),
		}
		if len(it.DependsOn) > 0 {
			p = append(p, "after "+clean(strings.Join(it.DependsOn, ", ")))
		}
		if it.Dropped {
			p = append(p, errStyle.Render("dropped: approving the plan makes no task of it"))
		}
		return para(p, "Description", it.Description)
	case n.plan != nil:
		it, err := planItem(s.env.st, *n.plan)
		if err != nil {
			return []string{errStyle.Render(err.Error())}
		}
		return it.preview
	case n.task != nil:
		t := n.task
		return []string{sectionStyle.Render(fmt.Sprintf("Task #%d %s", t.ID, clean(t.Title))),
			fmt.Sprintf("%s, risk %s, autonomy %s", t.Status, t.Risk, t.Autonomy), "", helpStyle.Render("enter opens the task in detail")}
	case n.decision != nil:
		return decisionItem(*n.decision).preview
	}
	return []string{helpStyle.Render("enter opens or closes a group")}
}

// draftPlan is the plan whose items may be edited from n: a draft plan, or the
// plan of a draft plan's item.
func (n specNode) draftPlan() *store.Plan {
	if n.plan != nil && n.plan.Status == "draft" {
		return n.plan
	}
	return nil
}

// errShowDiff carries a version comparison out of the version picker.
type errShowDiff struct{ diff string }

func (errShowDiff) Error() string { return "show diff" }

func (s specsScreen) compareVersions(sp store.Spec) (*prompt, error) {
	versions, err := s.env.st.ListSpecVersions(sp.ID)
	if err != nil {
		return nil, err
	}
	if len(versions) == 0 {
		return nil, fmt.Errorf("spec #%d has only its current version", sp.ID)
	}
	var choices []string
	byLabel := map[string]store.SpecVersion{}
	for _, v := range versions {
		l := fmt.Sprintf("v%d %s (replaced %s)", v.Version, v.Title, v.CreatedAt)
		choices = append(choices, l)
		byLabel[l] = v
	}
	return askOne(fmt.Sprintf("Compare spec #%d v%d with which version?", sp.ID, sp.Version), "", choices, choices[len(choices)-1], func(choice, _ string) (string, error) {
		v := byLabel[choice]
		// Specs are often written by agents: no escape sequence reaches the screen.
		old := ansi.Sanitize("title: " + v.Title + "\n\n" + v.Body)
		cur := ansi.Sanitize("title: " + sp.Title + "\n\n" + sp.Body.String)
		d, ok := unifiedDiff(old, cur)
		if !ok {
			return "", errors.New("the versions are too long to compare here")
		}
		head := fmt.Sprintf("spec #%d: v%d (-) against v%d, the current one (+)", sp.ID, v.Version, sp.Version)
		return "", errShowDiff{head + "\n" + d}
	}), nil
}

// editItem asks which item (unless one is selected), which field, and the new
// value, then edits the draft plan's item: a person's change before approval.
func (s specsScreen) editItem(plan store.Plan, item *store.PlanItem) (*prompt, error) {
	e := s.env
	field := func(it store.PlanItem) *prompt {
		label := clean(it.Ref)
		set := func(edit store.PlanItemEdit, what string) do {
			return func(_, token string) (string, error) {
				return fmt.Sprintf("plan #%d item %s: %s", plan.ID, label, what), e.st.EditPlanItem(plan.ID, it.Ref, edit, token)
			}
		}
		text := func(name string, apply func(v string) store.PlanItemEdit) (string, error) {
			return then(askLine(fmt.Sprintf("New %s for item %s:", name, label), "editing plan #"+strconv.FormatInt(plan.ID, 10), func(v, token string) (string, error) {
				return set(apply(v), name+" set")("", token)
			}))
		}
		drop := "drop (no task)"
		if it.Dropped {
			drop = "keep (make a task again)"
		}
		return askOne("Change what on item "+label+"?", "", []string{"title", "risk", "autonomy", "area", "size", "milestone", drop}, "", func(f, token string) (string, error) {
			switch f {
			case "title":
				return text("title", func(v string) store.PlanItemEdit { return store.PlanItemEdit{Title: &v} })
			case "area":
				return text("area", func(v string) store.PlanItemEdit { return store.PlanItemEdit{Area: &v} })
			case "size":
				return text("size", func(v string) store.PlanItemEdit { return store.PlanItemEdit{Size: &v} })
			case "milestone":
				return text("milestone", func(v string) store.PlanItemEdit { return store.PlanItemEdit{Milestone: &v} })
			case "risk", "autonomy":
				values := riskFilters[1:]
				current := it.Risk
				if f == "autonomy" {
					values, current = []string{"hitl", "hotl", "auto"}, it.Autonomy
				}
				return then(askOne(fmt.Sprintf("%s of item %s?", f, label), "editing plan #"+strconv.FormatInt(plan.ID, 10), values, current, func(v, token string) (string, error) {
					edit := store.PlanItemEdit{Risk: &v}
					if f == "autonomy" {
						edit = store.PlanItemEdit{Autonomy: &v}
					}
					return set(edit, f+" "+v)(v, token)
				}))
			default:
				d := !it.Dropped
				what := "dropped"
				if !d {
					what = "kept"
				}
				return set(store.PlanItemEdit{Drop: &d}, what)("", token)
			}
		})
	}
	if item != nil {
		return field(*item), nil
	}
	items, err := e.st.PlanItems(plan.ID)
	if err != nil {
		return nil, err
	}
	var refs []string
	byRef := map[string]store.PlanItem{}
	for _, it := range items {
		l := it.Ref + "  " + it.Title
		refs = append(refs, l)
		byRef[l] = it
	}
	return askOne(fmt.Sprintf("Edit which item of plan #%d?", plan.ID), "", refs, "", func(ref, _ string) (string, error) {
		return then(field(byRef[ref]))
	}), nil
}

func (s specsScreen) capturing() bool { return s.prompt != nil }

func (s specsScreen) update(msg tk.Msg) (screen, tk.Cmd) {
	switch msg := msg.(type) {
	case sizeMsg:
		s.width, s.height = msg.width, msg.height
		return s, nil
	case treeview.SelectedMsg:
		if n, ok := s.nodes[msg.Path]; ok && n.task != nil {
			id := n.task.ID
			return s, func() tk.Msg { return openTaskMsg{id} }
		}
		return s, nil
	case tk.Key:
		return s.key(msg)
	}
	return s, nil
}

func (s specsScreen) key(k tk.Key) (screen, tk.Cmd) {
	if s.prompt != nil {
		next, done := s.prompt.step(k)
		s.prompt = next
		if done == nil {
			return s, nil
		}
		var show errShowDiff
		if errors.As(done.err, &show) {
			s.diff, s.scroll = show.diff, 0
			return s, nil
		}
		reloaded, err := s.load(s.env)
		if err != nil {
			return s, errCmd(err)
		}
		if done.err != nil {
			return reloaded, errCmd(done.err)
		}
		return reloaded, infoCmd(done.info)
	}
	if s.diff != "" {
		switch k.String() {
		case "esc":
			s.diff = ""
		case "down", "j":
			s.scroll++
		case "up", "k":
			s.scroll = max(s.scroll-1, 0)
		case "pgdown", "space":
			s.scroll += max(s.height-2, 1)
		case "pgup":
			s.scroll = max(s.scroll-max(s.height-2, 1), 0)
		}
		return s, nil
	}
	n, ok := s.selected()
	open := func(p *prompt, err error) (screen, tk.Cmd) {
		if err != nil {
			return s, errCmd(err)
		}
		s.prompt = p
		return s, nil
	}
	switch k.String() {
	case "v":
		if ok && n.spec != nil {
			return open(s.compareVersions(*n.spec))
		}
	case "e":
		if ok && n.draftPlan() != nil {
			return open(s.editItem(*n.draftPlan(), n.item))
		}
		if ok && (n.plan != nil || n.item != nil) {
			return s, errCmd(errors.New("only a draft plan's items can be edited; an approved plan is fixed"))
		}
	case "a", "X":
		if r, ok := s.reviewable(n); ok {
			if k.String() == "a" {
				return open(r.approve(s.env), nil)
			}
			if r.reject == nil {
				return s, errCmd(errors.New("a " + r.kind + " cannot be rejected; leave it in draft or revise it"))
			}
			return open(r.reject(s.env), nil)
		}
	case "pgdown":
		s.scroll++
		return s, nil
	case "pgup":
		s.scroll = max(s.scroll-1, 0)
		return s, nil
	}
	var cmd tk.Cmd
	before := s.tree.Cursor()
	s.tree, cmd = s.tree.Update(k)
	if s.tree.Cursor() != before {
		s.scroll = 0
	}
	return s, cmd
}

// reviewable is the Review tab's item for a node still waiting on a person: a
// draft spec, a draft plan, a proposed decision.
func (s specsScreen) reviewable(n specNode) (reviewItem, bool) {
	switch {
	case n.spec != nil && n.spec.Status == "draft":
		return specItem(*n.spec), true
	case n.item == nil && n.plan != nil && n.plan.Status == "draft":
		it, err := planItem(s.env.st, *n.plan)
		return it, err == nil
	case n.decision != nil && n.decision.Status == "proposed":
		return decisionItem(*n.decision), true
	}
	return reviewItem{}, false
}

func (s specsScreen) treeHeight() int {
	return max(min(len(s.tree.VisibleRows()), s.height*2/5), 1)
}

func (s specsScreen) view(width, height int) string {
	if len(s.nodes) == 0 {
		return helpStyle.Render("No specs or decisions yet. Agents add them with `acline spec add` and `acline decision add`.")
	}
	if s.diff != "" {
		lines := strings.Split(widgets.DiffView(s.diff, max(s.width, 20), theme.DarkTheme()), "\n")
		return strings.Join(window(lines, min(s.scroll, max(len(lines)-s.height, 0)), s.height), "\n")
	}
	th := s.treeHeight()
	rows := strings.Split(s.tree.View(), "\n")
	from := max(min(s.tree.Cursor()-th/2, len(rows)-th), 0)
	out := window(rows, from, th)
	for i := range out {
		out[i] = fitTo(out[i], s.width)
	}
	out = append(out, helpStyle.Render(strings.Repeat("─", max(s.width, 1))))
	if s.prompt != nil {
		return strings.Join(out, "\n") + "\n" + s.prompt.view()
	}
	n, _ := s.selected()
	ph := max(s.height-th-1, 1)
	preview := wrap(s.preview(n), s.width)
	out = append(out, window(preview, max(min(s.scroll, len(preview)-ph), 0), ph)...)
	return strings.Join(out, "\n")
}

func (s specsScreen) keys() []widgets.Hint {
	if s.prompt != nil {
		return nil
	}
	if s.diff != "" {
		return []widgets.Hint{{Key: "esc", Action: "back"}, {Key: "↑/↓", Action: "scroll"}}
	}
	h := []widgets.Hint{{Key: "↑/↓", Action: "move"}, {Key: "enter/→/←", Action: "open/close"}}
	n, _ := s.selected()
	if n.spec != nil {
		h = append(h, widgets.Hint{Key: "v", Action: "compare versions"})
	}
	if n.draftPlan() != nil {
		h = append(h, widgets.Hint{Key: "e", Action: "edit item"})
	}
	if r, ok := s.reviewable(n); ok {
		h = append(h, widgets.Hint{Key: "a", Action: "approve"})
		if r.reject != nil {
			h = append(h, widgets.Hint{Key: "X", Action: "reject"})
		}
	}
	return append(h, widgets.Hint{Key: "pgup/pgdn", Action: "scroll preview"})
}
