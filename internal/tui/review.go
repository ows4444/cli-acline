package tui

import (
	"errors"
	"fmt"
	"strings"

	tk "github.com/ows4444/tui"
	"github.com/ows4444/tui/ansi"
	"github.com/ows4444/tui/widgets"

	"acline/internal/app"
	"acline/internal/store"
)

// reviewScreen is one place to clear everything waiting on a person: tasks
// whose gate needs only an approval, draft specs, plans (with what changed
// since the spec's previous plan), proposed decisions and memory. Each
// decision asks first, then for the approval token if the store wants it,
// then says what it did.
type reviewScreen struct {
	items  []reviewItem
	cursor int
	scroll int // of the preview
	prompt *prompt

	env           env
	width, height int
}

// reviewItem is one thing to decide. approve and reject open the prompt for
// it (reject is nil where the store has no rejection, e.g. a spec).
type reviewItem struct {
	kind    string // task | spec | plan | decision | memory
	id      int64
	label   string
	preview []string
	approve func(e env) *prompt
	reject  func(e env) *prompt
}

func (reviewScreen) title() string { return "Review" }

func (s reviewScreen) load(e env) (screen, error) {
	q, err := app.Queue(e.st, e.projectID, e.hash)
	if err != nil {
		return s, err
	}
	s.env = e
	var current string
	if s.cursor < len(s.items) {
		current = s.items[s.cursor].key()
	}
	s.items = nil
	for _, g := range q.AwaitingApproval {
		s.items = append(s.items, taskItem(g))
	}
	for _, sp := range q.DraftSpecs {
		s.items = append(s.items, specItem(sp))
	}
	for _, p := range q.DraftPlans {
		it, err := planItem(e.st, p)
		if err != nil {
			return s, err
		}
		s.items = append(s.items, it)
	}
	for _, d := range q.ProposedDecisions {
		s.items = append(s.items, decisionItem(d))
	}
	for _, m := range q.PendingMemory {
		s.items = append(s.items, memoryItem(m))
	}
	s.cursor = min(s.cursor, max(len(s.items)-1, 0))
	for i, it := range s.items { // stay on the same item when it is still there
		if it.key() == current {
			s.cursor = i
		}
	}
	return s, nil
}

func (it reviewItem) key() string { return fmt.Sprintf("%s#%d", it.kind, it.id) }

// para appends a heading and text, one line per line of the text.
func para(lines []string, heading, text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return lines
	}
	lines = append(lines, "", sectionStyle.Render(heading))
	for _, l := range strings.Split(text, "\n") {
		lines = append(lines, "  "+clean(l))
	}
	return lines
}

func by(actorType, actorID string) string { return clean(actorType + "/" + actorID) }

func taskItem(g app.GatedTask) reviewItem {
	t := g.Task
	label := fmt.Sprintf("#%d", t.ID)
	p := []string{
		sectionStyle.Render(fmt.Sprintf("Task #%d %s", t.ID, clean(t.Title))),
		fmt.Sprintf("risk %s, autonomy %s, status %s; by %s", t.Risk, t.Autonomy, t.Status, by(t.ActorType.String, t.ActorID.String)),
		"", sectionStyle.Render("Waiting on you"),
	}
	for _, b := range g.Gate.PersonBlockers {
		p = append(p, "  "+clean(b))
	}
	for _, w := range g.Gate.Warnings {
		p = append(p, "  "+warnStyle.Render("! ")+clean(w))
	}
	p = para(p, "Description", t.Description)
	p = append(p, "", helpStyle.Render("enter opens the task in detail"))
	return reviewItem{kind: "task", id: t.ID, label: "task " + label + "  " + clean(t.Title), preview: p,
		approve: func(e env) *prompt {
			return askYesNo("Approve task "+label+" for the code as it is now?", "approving task "+label, func(_, token string) (string, error) {
				id, err := app.Approve(e.st, app.ApproveRequest{TaskID: t.ID, Token: token, AllowCwdFallback: true, Tree: app.TaskTree(e.st, t.ID, e.hash)})
				return fmt.Sprintf("approval #%d recorded for task %s", id, label), err
			})
		},
		reject: func(e env) *prompt {
			return askLine("Reject task "+label+": why?", "rejecting task "+label, func(note, _ string) (string, error) {
				id, err := app.Reject(e.st, app.RejectRequest{TaskID: t.ID, Note: note, AllowCwdFallback: true})
				return fmt.Sprintf("rejection #%d recorded for task %s", id, label), err
			})
		}}
}

func specItem(sp store.Spec) reviewItem {
	label := fmt.Sprintf("#%d", sp.ID)
	p := []string{
		sectionStyle.Render(fmt.Sprintf("Spec #%d %s", sp.ID, clean(sp.Title))),
		fmt.Sprintf("version %d, %s; by %s", sp.Version, sp.Status, by(sp.ActorType.String, sp.ActorID.String)),
	}
	p = para(p, "Body", sp.Body.String)
	return reviewItem{kind: "spec", id: sp.ID, label: "spec " + label + "  " + clean(sp.Title), preview: p,
		approve: func(e env) *prompt {
			return askYesNo("Approve spec "+label+"? Plans and tasks can then be made from it.", "approving spec "+label, func(_, token string) (string, error) {
				return "spec " + label + " approved", e.st.ApproveSpec(sp.ID, token)
			})
		}}
}

func planItem(st *store.Store, p store.Plan) (reviewItem, error) {
	label := fmt.Sprintf("#%d", p.ID)
	spec := fmt.Sprintf("spec #%d", p.SpecID)
	if sp, err := st.GetSpec(p.SpecID); err == nil {
		spec += " " + clean(sp.Title)
	}
	prev, changes, err := app.PlanDiff(st, p.ID)
	if err != nil {
		return reviewItem{}, err
	}
	lines := []string{
		sectionStyle.Render(fmt.Sprintf("Plan #%d v%d for %s", p.ID, p.Version, spec)),
		"by " + by(p.ActorType.String, p.ActorID.String),
	}
	lines = para(lines, "Note", p.Note.String)
	heading := "Items (the spec's first plan)"
	if prev != nil {
		heading = fmt.Sprintf("Items, compared with plan #%d v%d", prev.ID, prev.Version)
	}
	lines = append(lines, "", sectionStyle.Render(heading))
	marks := map[string]string{"added": okStyle.Render("+"), "removed": errStyle.Render("-"), "changed": warnStyle.Render("~"), "dropped": errStyle.Render("x"), "unchanged": helpStyle.Render("=")}
	kept := 0
	for _, c := range changes {
		it := c.Item
		line := fmt.Sprintf("  %s %-6s %s  %s", marks[c.Change], clean(c.Ref), clean(it.Title), helpStyle.Render(it.Risk+"/"+it.Autonomy))
		if len(it.DependsOn) > 0 {
			line += helpStyle.Render("  after " + clean(strings.Join(it.DependsOn, ", ")))
		}
		switch c.Change {
		case "changed":
			line += warnStyle.Render("  (" + strings.Join(c.Fields, ", ") + ")")
		case "dropped":
			line += errStyle.Render("  (dropped: no task)")
		case "removed":
			line += errStyle.Render("  (no longer in the plan)")
		}
		if c.Change != "removed" && c.Change != "dropped" {
			kept++
		}
		lines = append(lines, line)
	}
	lines = append(lines, "", helpStyle.Render(fmt.Sprintf("approving creates %d task(s)", kept)))
	return reviewItem{kind: "plan", id: p.ID, label: "plan " + label + "  for " + spec, preview: lines,
		approve: func(e env) *prompt {
			return askYesNo(fmt.Sprintf("Approve plan %s? It creates %d task(s).", label, kept), "approving plan "+label, func(_, token string) (string, error) {
				created, err := e.st.ApprovePlan(p.ID, token)
				return fmt.Sprintf("plan %s approved: %d task(s) created", label, len(created)), err
			})
		},
		reject: func(e env) *prompt {
			return askLine("Reject plan "+label+": why?", "", func(note, _ string) (string, error) {
				return "plan " + label + " rejected", e.st.RejectPlan(p.ID, note)
			})
		}}, nil
}

func decisionItem(d store.Decision) reviewItem {
	label := fmt.Sprintf("#%d", d.ID)
	p := []string{
		sectionStyle.Render(fmt.Sprintf("Decision #%d %s", d.ID, clean(d.Title))),
		fmt.Sprintf("%s, scope %s; by %s", d.Status, clean(d.Scope.String), by(d.ActorType.String, d.ActorID.String)),
	}
	p = para(p, "Context", d.Context.String)
	p = para(p, "Decision", d.DecisionText.String)
	p = para(p, "Rationale", d.Rationale.String)
	return reviewItem{kind: "decision", id: d.ID, label: "decision " + label + "  " + clean(d.Title), preview: p,
		approve: func(e env) *prompt {
			return askYesNo("Accept decision "+label+"?", "accepting decision "+label, func(_, token string) (string, error) {
				return "decision " + label + " accepted", e.st.AcceptDecision(d.ID, token)
			})
		},
		reject: func(e env) *prompt {
			return askYesNo("Reject decision "+label+"?", "rejecting decision "+label, func(_, token string) (string, error) {
				return "decision " + label + " rejected", e.st.RejectDecision(d.ID, token)
			})
		}}
}

func memoryItem(m store.MemoryEntry) reviewItem {
	label := fmt.Sprintf("#%d", m.ID)
	p := []string{
		sectionStyle.Render(fmt.Sprintf("Memory #%d, %s", m.ID, m.Kind)),
		fmt.Sprintf("area %s; by %s", clean(m.Area.String), by(m.ActorType.String, m.ActorID.String)),
	}
	if m.SourceKind.Valid {
		p = append(p, fmt.Sprintf("drafted from %s #%d", clean(m.SourceKind.String), m.SourceID.Int64))
	}
	p = para(p, "Entry", m.Body)
	return reviewItem{kind: "memory", id: m.ID, label: "memory " + label + "  " + clean(m.Body), preview: p,
		approve: func(e env) *prompt {
			return askYesNo("Approve memory "+label+"? Agents will be shown it.", "approving memory "+label, func(_, token string) (string, error) {
				return "memory " + label + " approved", e.st.ReviewMemory(m.ID, true, token)
			})
		},
		reject: func(e env) *prompt {
			return askYesNo("Reject memory "+label+"?", "rejecting memory "+label, func(_, token string) (string, error) {
				return "memory " + label + " rejected", e.st.ReviewMemory(m.ID, false, token)
			})
		}}
}

func (s reviewScreen) capturing() bool { return s.prompt != nil }

func (s reviewScreen) update(msg tk.Msg) (screen, tk.Cmd) {
	switch msg := msg.(type) {
	case sizeMsg:
		s.width, s.height = msg.width, msg.height
	case tk.Key:
		if s.prompt != nil {
			next, done := s.prompt.step(msg)
			s.prompt = next
			if done == nil {
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
		if len(s.items) == 0 {
			return s, nil
		}
		it := s.items[s.cursor]
		switch msg.String() {
		case "down", "j":
			s.cursor, s.scroll = min(s.cursor+1, len(s.items)-1), 0
		case "up", "k":
			s.cursor, s.scroll = max(s.cursor-1, 0), 0
		case "pgdown", "space":
			s.scroll += max(s.previewHeight()-1, 1)
		case "pgup":
			s.scroll = max(s.scroll-max(s.previewHeight()-1, 1), 0)
		case "a":
			s.prompt = it.approve(s.env)
		case "X":
			if it.reject == nil {
				return s, errCmd(errors.New("a " + it.kind + " cannot be rejected; leave it in draft or revise it"))
			}
			s.prompt = it.reject(s.env)
		case "enter":
			if it.kind == "task" {
				id := it.id
				return s, func() tk.Msg { return openTaskMsg{id} }
			}
		}
	}
	return s, nil
}

// listHeight is the rows the item list takes: a third of the screen at most.
func (s reviewScreen) listHeight() int {
	return max(min(len(s.items), s.height/3), 1)
}

func (s reviewScreen) previewHeight() int { return max(s.height-s.listHeight()-2, 1) }

func (s reviewScreen) view(width, height int) string {
	if len(s.items) == 0 {
		return okStyle.Render("Nothing is waiting on you.") + "\n\n" + helpStyle.Render("Tasks ready for approval, draft specs and plans, proposed decisions and memory to review show up here.")
	}
	var out []string
	lh := s.listHeight()
	from := max(min(s.cursor-lh/2, len(s.items)-lh), 0)
	for i := from; i < min(from+lh, len(s.items)); i++ {
		line := "  " + s.items[i].label
		if i == s.cursor {
			line = sectionStyle.Render("> " + s.items[i].label)
		}
		out = append(out, fitTo(line, s.width))
	}
	out = append(out, helpStyle.Render(fmt.Sprintf("── %d of %d waiting on you ", s.cursor+1, len(s.items))+strings.Repeat("─", max(s.width-30, 0))))
	if s.prompt != nil {
		return strings.Join(out, "\n") + "\n" + s.prompt.view()
	}
	preview := wrap(s.items[s.cursor].preview, s.width)
	ph := s.previewHeight()
	scroll := max(min(s.scroll, len(preview)-ph), 0)
	out = append(out, window(preview, scroll, ph)...)
	return strings.Join(out, "\n")
}

// fitTo cuts a line to width, when there is one.
func fitTo(line string, width int) string {
	if width > 0 {
		return ansi.Truncate(line, width)
	}
	return line
}

func (s reviewScreen) keys() []widgets.Hint {
	if s.prompt != nil || len(s.items) == 0 {
		return nil
	}
	h := []widgets.Hint{{Key: "↑/↓", Action: "item"}, {Key: "a", Action: "approve"}}
	if it := s.items[s.cursor]; it.reject != nil {
		h = append(h, widgets.Hint{Key: "X", Action: "reject"})
	}
	if s.items[s.cursor].kind == "task" {
		h = append(h, widgets.Hint{Key: "enter", Action: "open"})
	}
	return append(h, widgets.Hint{Key: "pgup/pgdn", Action: "scroll"})
}
