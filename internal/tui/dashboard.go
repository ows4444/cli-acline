package tui

import (
	"fmt"
	"strings"

	tk "github.com/ows4444/tui"
	"github.com/ows4444/tui/ansi"
	"github.com/ows4444/tui/widgets"

	"acline/internal/app"
	"acline/internal/store"
)

// sectionCap is how many rows a dashboard section lists before "and N more".
const sectionCap = 8

// dashboardScreen is what needs a person: app.Dashboard for the overview and
// app.Queue for what only a person can move forward, plus whether the audit
// trail verifies.
type dashboardScreen struct {
	sections []section
	scroll   int // first line shown
	height   int // lines the last view had room for
}

// section is one heading and its rows. bad marks one that is a problem in
// itself (a failed verify), not merely a list of things to do.
type section struct {
	title string
	rows  []string
	bad   bool
}

func (dashboardScreen) title() string { return "Dashboard" }

func (s dashboardScreen) load(e env) (screen, error) {
	d, err := app.Dashboard(e.st, e.projectID)
	if err != nil {
		return s, err
	}
	q, err := app.Queue(e.st, e.projectID, e.hash)
	if err != nil {
		return s, err
	}
	audit, err := auditSection(e.st)
	if err != nil {
		return s, err
	}

	approval := section{title: "Awaiting your approval: the gate needs nothing else"}
	queued := map[int64]bool{}
	for _, g := range q.AwaitingApproval {
		queued[g.Task.ID] = true
		approval.rows = append(approval.rows, fmt.Sprintf("#%d [%s risk] %s — %s", g.Task.ID, g.Task.Risk, clean(g.Task.Title), clean(strings.Join(g.Gate.PersonBlockers, "; "))))
	}
	attention := section{title: "Tasks needing attention: blocked, urgent/high priority, or high/critical risk"}
	for _, t := range d.TasksNeedingAttention {
		if !queued[t.ID] {
			attention.rows = append(attention.rows, fmt.Sprintf("#%d [%s] %s priority, %s risk: %s", t.ID, t.Status, t.Priority, t.Risk, clean(t.Title)))
		}
	}
	specs := section{title: "Draft specs"}
	for _, sp := range q.DraftSpecs {
		specs.rows = append(specs.rows, fmt.Sprintf("#%d %s", sp.ID, clean(sp.Title)))
	}
	plans := section{title: "Plans awaiting a person"}
	for _, p := range q.DraftPlans {
		plans.rows = append(plans.rows, fmt.Sprintf("plan #%d v%d for spec #%d (by %s)", p.ID, p.Version, p.SpecID, clean(p.ActorID.String)))
	}
	decisions := section{title: "Proposed decisions"}
	for _, dc := range q.ProposedDecisions {
		decisions.rows = append(decisions.rows, fmt.Sprintf("#%d %s", dc.ID, clean(dc.Title)))
	}
	memory := section{title: "Memory awaiting review"}
	if d.DraftedMemoryCount > 0 {
		memory.title += fmt.Sprintf(": %d drafted from failures", d.DraftedMemoryCount)
	}
	for _, mem := range q.PendingMemory {
		memory.rows = append(memory.rows, fmt.Sprintf("#%d [%s] %s", mem.ID, mem.Kind, clean(mem.Body)))
	}
	sessions := section{title: fmt.Sprintf("Stale sessions: no activity for %s; end one with `acline session end` from its project", app.StaleSessionAge)}
	for _, ss := range q.StaleSessions {
		sessions.rows = append(sessions.rows, fmt.Sprintf("#%d started %s by %s", ss.ID, ss.StartedAt, clean(ss.ActorID.String)))
	}
	upkeep := section{title: "Upkeep"}
	if d.DecayingMemoryCount > 0 {
		upkeep.rows = append(upkeep.rows, fmt.Sprintf("%d approved memory entr(ies) not reconfirmed in %d days (`acline memory decay`)", d.DecayingMemoryCount, store.DefaultMemoryDecayDays))
	}
	if n := len(d.UnverifiedDeps); n > 0 {
		upkeep.rows = append(upkeep.rows, fmt.Sprintf("%d dependenc(ies) not verified (`acline dep list`)", n))
	}

	s.sections = []section{audit, approval, attention, specs, plans, decisions, memory, sessions, upkeep}
	return s.clamp(), nil
}

// auditSection says whether the hash chain and the sealed approvals and checks
// verify, as `acline verify` does.
func auditSection(st *store.Store) (section, error) {
	sec := section{title: "Audit trail"}
	chain, err := st.VerifyChain()
	if err != nil {
		return sec, err
	}
	if !chain.OK() {
		sec.bad = true
		sec.rows = []string{fmt.Sprintf("FAILED verification at event #%d: %s", chain.BadID, clean(chain.Reason))}
		return sec, nil
	}
	rec, err := st.VerifyRecords()
	if err != nil {
		return sec, err
	}
	if !rec.OK() {
		sec.bad = true
		sec.rows = []string{fmt.Sprintf("approvals/checks FAILED verification at %s: %s", rec.BadRef, clean(rec.Reason))}
		return sec, nil
	}
	sec.rows = []string{fmt.Sprintf("intact: %d event(s) and %d sealed approval/check record(s) verify", chain.Checked, rec.Checked)}
	return sec, nil
}

// clean makes stored text safe and one line for display: titles and bodies are
// often written by agents, and an escape sequence in one must not reach the
// terminal.
func clean(s string) string {
	return strings.Join(strings.Fields(ansi.Sanitize(strings.ReplaceAll(s, "\t", " "))), " ")
}

var sectionStyle = ansi.NewStyle().Bold()

// lines is the dashboard: each section with something in it, then one line
// naming the sections that are empty, so an empty queue reads at a glance.
func (s dashboardScreen) lines() []string {
	var out, empty []string
	for _, sec := range s.sections {
		if len(sec.rows) == 0 {
			empty = append(empty, sec.title)
			continue
		}
		if len(out) > 0 {
			out = append(out, "")
		}
		title := sec.title
		if !sec.bad && sec.title != "Audit trail" {
			title = fmt.Sprintf("%s (%d)", sec.title, len(sec.rows))
		}
		if sec.bad {
			out = append(out, errStyle.Render(title))
		} else {
			out = append(out, sectionStyle.Render(title))
		}
		for j, r := range sec.rows {
			if j == sectionCap {
				out = append(out, helpStyle.Render(fmt.Sprintf("  … and %d more", len(sec.rows)-sectionCap)))
				break
			}
			if sec.bad {
				r = errStyle.Render(r)
			}
			out = append(out, "  "+r)
		}
	}
	if len(empty) > 0 {
		var names []string
		for _, e := range empty {
			names = append(names, strings.SplitN(e, ":", 2)[0])
		}
		out = append(out, "", helpStyle.Render("Nothing waiting: "+strings.Join(names, ", ")))
	}
	return out
}

func (s dashboardScreen) update(msg tk.Msg) (screen, tk.Cmd) {
	if size, ok := msg.(sizeMsg); ok {
		s.height = size.height
	}
	k, ok := msg.(tk.Key)
	if !ok {
		return s.clamp(), nil
	}
	page := max(s.height-1, 1)
	switch k.String() {
	case "down", "j":
		s.scroll++
	case "up", "k":
		s.scroll--
	case "pgdown", "space":
		s.scroll += page
	case "pgup":
		s.scroll -= page
	case "home", "g":
		s.scroll = 0
	}
	return s.clamp(), nil
}

// clamp keeps the scroll within the lines there are.
func (s dashboardScreen) clamp() dashboardScreen {
	s.scroll = max(min(s.scroll, len(s.lines())-s.height), 0)
	return s
}

func (s dashboardScreen) view(width, height int) string {
	lines := s.lines()
	if height > 0 {
		end := min(s.scroll+height, len(lines))
		lines = lines[min(s.scroll, end):end]
	}
	for i, l := range lines {
		if width > 0 {
			lines[i] = ansi.Truncate(l, width)
		}
	}
	return strings.Join(lines, "\n")
}

func (dashboardScreen) keys() []widgets.Hint {
	return []widgets.Hint{{Key: "↑/↓ pgup/pgdn", Action: "scroll"}}
}
