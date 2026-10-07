package tui

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"

	tk "github.com/ows4444/tui"
	"github.com/ows4444/tui/ansi"
	"github.com/ows4444/tui/widgets"

	"acline/internal/app"
	"acline/internal/store"
)

// historyLimit is how many of a task's newest events the detail reads.
const historyLimit = 40

// bookkeeping events seal or version the audit trail; they say nothing about
// what happened to the task.
var bookkeeping = map[string]bool{"row_seal": true, "hash_version": true}

// taskDetail is one task: what it is on the left (fields, criteria, links,
// spec), and on the right whether it can be completed (the gate, the newest
// check of each kind, approvals) and what happened to it.
type taskDetail struct {
	task      store.Task
	role      string
	spec      string
	criteria  []store.Criterion
	links     []store.Link
	gate      store.GateResult
	tree      string        // the code as it is now, for the stale flags
	checks    []store.Check // newest per kind, by kind
	approvals []store.Approval
	history   []store.Event

	focus         int // 0: left column, 1: right
	scroll        [2]int
	width, height int
}

func loadDetail(e env, id int64) (taskDetail, error) {
	var d taskDetail
	t, err := e.st.GetTask(id)
	if err != nil {
		return d, err
	}
	d.task = *t
	if t.RoleID.Valid {
		if r, err := e.st.GetRole(t.RoleID.Int64); err == nil {
			d.role = r.Name
		}
	}
	if t.SpecID.Valid {
		if sp, err := e.st.GetSpec(t.SpecID.Int64); err == nil {
			d.spec = fmt.Sprintf("#%d %s (%s)", sp.ID, clean(sp.Title), sp.Status)
		}
	}
	if d.criteria, err = e.st.ListCriteria(id); err != nil {
		return d, err
	}
	if d.links, err = e.st.ListLinks(id); err != nil {
		return d, err
	}
	d.tree = app.GateTree(e.st, id, e.hash)
	if d.gate, err = e.st.EvaluateGateForTree(id, d.tree); err != nil {
		return d, err
	}
	checks, err := e.st.ListChecks(id)
	if err != nil {
		return d, err
	}
	newest := map[string]store.Check{} // checks come oldest first, as the gate reads them
	for _, c := range checks {
		newest[c.Kind] = c
	}
	for _, c := range newest {
		d.checks = append(d.checks, c)
	}
	sort.Slice(d.checks, func(i, j int) bool { return d.checks[i].Kind < d.checks[j].Kind })
	if d.approvals, err = e.st.ListApprovals(id); err != nil {
		return d, err
	}
	events, err := e.st.ListEvents(&id, historyLimit)
	if err != nil {
		return d, err
	}
	for _, ev := range events {
		if !bookkeeping[ev.Type] {
			d.history = append(d.history, ev)
		}
	}
	return d, nil
}

// stale reports whether a check is about code other than the code as it is now.
func (d taskDetail) stale(treeHash string) bool {
	known := d.tree != "" && d.tree != store.TreeUnavailable
	return known && treeHash != "" && treeHash != d.tree
}

func (d taskDetail) left() []string {
	t := d.task
	l := []string{sectionStyle.Render(fmt.Sprintf("#%d %s", t.ID, clean(t.Title)))}
	field := func(name, value string) {
		if value != "" {
			l = append(l, helpStyle.Render(fmt.Sprintf("%-10s", name))+value)
		}
	}
	status := t.Status
	if t.Status == "blocked" && t.BlockedReason.Valid {
		status += ": " + clean(t.BlockedReason.String)
	}
	field("status", status)
	field("priority", t.Priority)
	field("risk", t.Risk)
	field("autonomy", t.Autonomy)
	field("area", clean(t.Area.String))
	field("type", t.Type.String)
	field("role", clean(d.role))
	field("spec", d.spec)
	if t.Deferred {
		field("deferred", clean(t.DeferredReason.String)+revisit(t.RevisitTrigger))
	}
	field("by", clean(t.ActorType.String+"/"+t.ActorID.String))
	field("created", t.CreatedAt)
	if desc := strings.TrimSpace(t.Description); desc != "" {
		l = append(l, "")
		for _, line := range strings.Split(desc, "\n") {
			l = append(l, clean(line))
		}
	}

	l = append(l, "", sectionStyle.Render(fmt.Sprintf("Acceptance criteria (%d)", len(d.criteria))))
	if len(d.criteria) == 0 {
		l = append(l, helpStyle.Render("  none"))
	}
	for _, c := range d.criteria {
		box := "[ ]"
		if c.Done {
			box = "[x]"
		}
		l = append(l, fmt.Sprintf("  %s #%d %s", box, c.ID, clean(c.Text)))
	}
	if len(d.links) > 0 {
		l = append(l, "", sectionStyle.Render("Links"))
		for _, k := range d.links {
			other := k.RelatedTaskID
			if other == t.ID {
				other = k.TaskID
			}
			l = append(l, fmt.Sprintf("  %s #%d", k.Relation, other))
		}
	}
	return l
}

func revisit(trigger sql.NullString) string {
	if trigger.Valid && trigger.String != "" {
		return " (revisit when: " + clean(trigger.String) + ")"
	}
	return ""
}

func (d taskDetail) right() []string {
	var r []string
	switch {
	case d.task.Status == "done":
		r = append(r, okStyle.Render("Gate: done"))
	case d.gate.OK():
		r = append(r, okStyle.Render("Gate: can be completed"))
	default:
		r = append(r, errStyle.Render(fmt.Sprintf("Gate: %d blocker(s)", len(d.gate.Blockers))))
	}
	person := map[string]bool{}
	for _, b := range d.gate.PersonBlockers {
		person[b] = true
	}
	for _, b := range d.gate.Blockers {
		who := "agent"
		if person[b] {
			who = "person"
		}
		r = append(r, "  "+errStyle.Render("✗ ")+helpStyle.Render("("+who+") ")+clean(b))
	}
	for _, w := range d.gate.Warnings {
		r = append(r, "  "+warnStyle.Render("! ")+clean(w))
	}
	if d.tree == store.TreeUnavailable {
		r = append(r, "  "+warnStyle.Render("! the project's files could not be fingerprinted; stale checks cannot be told apart"))
	}

	r = append(r, "", sectionStyle.Render("Newest check per kind"))
	if len(d.checks) == 0 {
		r = append(r, helpStyle.Render("  none recorded"))
	}
	for _, c := range d.checks {
		line := fmt.Sprintf("%-12s %-7s %-6s %s by %s", c.Kind, c.Status, c.Source, c.CreatedAt, clean(c.ActorType.String+"/"+c.ActorID.String))
		switch c.Status {
		case "fail":
			line = errStyle.Render(line)
		case "pass":
			line = okStyle.Render(line)
		}
		if d.stale(c.TreeHash.String) {
			line += warnStyle.Render("  STALE: about older code")
		}
		r = append(r, "  "+line)
	}

	r = append(r, "", sectionStyle.Render(fmt.Sprintf("Approvals (%d)", len(d.approvals))))
	if len(d.approvals) == 0 {
		r = append(r, helpStyle.Render("  none"))
	}
	for _, a := range d.approvals {
		line := fmt.Sprintf("  %s %s by %s, %s", a.Decision, a.Kind, clean(a.Approver), a.CreatedAt)
		if d.stale(a.TreeHash.String) {
			line += warnStyle.Render("  STALE: given for older code")
		}
		if a.Note.Valid && a.Note.String != "" {
			line += helpStyle.Render(" — ") + clean(a.Note.String)
		}
		r = append(r, line)
	}

	r = append(r, "", sectionStyle.Render("History, newest first"))
	for _, ev := range d.history {
		r = append(r, "  "+helpStyle.Render(ev.CreatedAt)+" "+ev.Type+" "+clean(ev.Message))
	}
	return r
}

var okStyle = ansi.NewStyle().Foreground(ansi.BrightGreen)

// sideBySide is the width from which the two columns sit side by side; below
// it they are stacked.
const sideBySide = 100

func (d taskDetail) update(msg tk.Msg) taskDetail {
	switch msg := msg.(type) {
	case sizeMsg:
		d.width, d.height = msg.width, msg.height
	case tk.Key:
		switch msg.String() {
		case "left", "h":
			d.focus = 0
		case "right", "l":
			d.focus = 1
		case "down", "j":
			d.scroll[d.focus]++
		case "up", "k":
			d.scroll[d.focus]--
		case "pgdown", "space":
			d.scroll[d.focus] += max(d.height-1, 1)
		case "pgup":
			d.scroll[d.focus] -= max(d.height-1, 1)
		case "home", "g":
			d.scroll[d.focus] = 0
		}
	}
	return d.clamp()
}

func (d taskDetail) stacked() bool { return d.width > 0 && d.width < sideBySide }

// columns is the two columns wrapped to their widths (one, stacked, on a
// narrow terminal): nothing is cut off, since a blocker's whole text matters.
func (d taskDetail) columns() (cols [2][]string, widths [2]int) {
	if d.stacked() || d.width <= 0 {
		all := append(append(d.left(), ""), d.right()...)
		return [2][]string{wrap(all, d.width), nil}, [2]int{d.width, 0}
	}
	lw := d.width*2/5 - 2
	rw := d.width - lw - 3
	return [2][]string{wrap(d.left(), lw), wrap(d.right(), rw)}, [2]int{lw, rw}
}

// wrap breaks lines wider than width, indenting the continuation.
func wrap(lines []string, width int) []string {
	const indent = "    "
	if width <= len(indent)+10 {
		return lines
	}
	var out []string
	for _, l := range lines {
		if ansi.Width(l) <= width {
			out = append(out, l)
			continue
		}
		// The wrapper trims leading space, so the line's own indent is kept
		// aside and put back on every part.
		body := strings.TrimLeft(l, " ")
		lead := strings.Repeat(" ", len(l)-len(body))
		for i, part := range strings.Split(ansi.WrapStyled(body, width-len(lead)-len(indent)), "\n") {
			if i > 0 {
				part = indent + part
			}
			out = append(out, lead+part)
		}
	}
	return out
}

func (d taskDetail) clamp() taskDetail {
	cols, _ := d.columns()
	if d.stacked() {
		d.focus = 0
	}
	for i := range d.scroll {
		d.scroll[i] = max(min(d.scroll[i], len(cols[i])-d.height), 0)
	}
	return d
}

func window(lines []string, from, height int) []string {
	if height > 0 {
		end := min(from+height, len(lines))
		lines = lines[min(from, end):end]
	}
	return lines
}

func (d taskDetail) view() string {
	cols, widths := d.columns()
	left := window(cols[0], d.scroll[0], d.height)
	if cols[1] == nil {
		return strings.Join(left, "\n")
	}
	right := window(cols[1], d.scroll[1], d.height)
	divider := helpStyle.Render(" │ ")
	if d.focus == 1 {
		divider = sectionStyle.Render(" │ ")
	}
	rows := max(len(left), len(right))
	out := make([]string, rows)
	for i := range rows {
		var l, r string
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		out[i] = l + strings.Repeat(" ", max(widths[0]-ansi.Width(l), 0)) + divider + r
	}
	return strings.Join(out, "\n")
}

func (taskDetail) keys() []widgets.Hint {
	return []widgets.Hint{{Key: "esc", Action: "back"}, {Key: "←/→", Action: "column"}, {Key: "↑/↓", Action: "scroll"}}
}
