package tui

import (
	"fmt"
	"strings"

	tk "github.com/ows4444/tui"
	"github.com/ows4444/tui/form"
	"github.com/ows4444/tui/widgets"

	"acline/internal/store"
)

// memoryScreen is what agents are told and what they noted: memory entries
// (approve or reject what an agent wrote, forget what is no longer true,
// restore, reconfirm what is decaying) and the notes not yet promoted
// (promote one into a decision or a memory entry, with a form).
type memoryScreen struct {
	notes  bool   // showing notes, not memory
	filter string // which memory: pending | approved | decaying | stale | all

	memory   []store.MemoryEntry
	decaying map[int64]bool
	unpromo  []store.Note
	cursor   int
	scroll   int
	prompt   *prompt

	env           env
	width, height int
}

var memoryFilters = []string{"pending", "approved", "decaying", "stale", "all"}

func (memoryScreen) title() string { return "Memory" }

func (s memoryScreen) load(e env) (screen, error) {
	s.env = e
	if s.filter == "" {
		s.filter = memoryFilters[0]
	}
	all, err := e.st.ListMemory(store.MemoryFilter{IncludeStale: true, ProjectID: e.projectID})
	if err != nil {
		return s, err
	}
	decay, err := e.st.DecayCandidates(store.DefaultMemoryDecayDays, e.projectID)
	if err != nil {
		return s, err
	}
	s.decaying = map[int64]bool{}
	for _, m := range decay {
		s.decaying[m.ID] = true
	}
	s.memory = s.memory[:0:0]
	for _, m := range all {
		if s.shows(m) {
			s.memory = append(s.memory, m)
		}
	}
	if s.unpromo, err = e.st.ListNotes(store.NoteFilter{ProjectID: e.projectID, UnpromotedOnly: true}); err != nil {
		return s, err
	}
	s.cursor = min(s.cursor, max(s.rows()-1, 0))
	return s, nil
}

func (s memoryScreen) shows(m store.MemoryEntry) bool {
	switch s.filter {
	case "pending":
		return m.Status == "pending" && !m.Stale
	case "approved":
		return m.Status == "approved" && !m.Stale
	case "decaying":
		return s.decaying[m.ID]
	case "stale":
		return m.Stale
	}
	return true
}

func (s memoryScreen) rows() int {
	if s.notes {
		return len(s.unpromo)
	}
	return len(s.memory)
}

func (s memoryScreen) label(i int) string {
	if s.notes {
		n := s.unpromo[i]
		return fmt.Sprintf("note #%d  %s  %s", n.ID, helpStyle.Render(clean(n.Source)), clean(n.Body))
	}
	m := s.memory[i]
	status := m.Status
	switch {
	case m.Stale:
		status = "stale"
	case s.decaying[m.ID]:
		status += ", decaying"
	}
	return fmt.Sprintf("#%d [%s] %s  %s  %s", m.ID, m.Kind, helpStyle.Render(status), helpStyle.Render(clean(m.Area.String)), clean(m.Body))
}

func (s memoryScreen) preview(i int) []string {
	if s.notes {
		n := s.unpromo[i]
		p := []string{sectionStyle.Render(fmt.Sprintf("Note #%d", n.ID)),
			fmt.Sprintf("from %s, by %s, %s", clean(n.Source), by(n.ActorType.String, n.ActorID.String), n.CreatedAt)}
		return append(para(p, "Note", n.Body), "", helpStyle.Render("enter promotes it into a decision or a memory entry"))
	}
	p := memoryItem(s.memory[i]).preview
	m := s.memory[i]
	if m.ReviewedAt.Valid {
		p = append(p, "", helpStyle.Render("reviewed "+m.ReviewedAt.String))
	}
	if s.decaying[m.ID] {
		p = append(p, warnStyle.Render(fmt.Sprintf("not reconfirmed in %d days: t if it is still true, F if not", store.DefaultMemoryDecayDays)))
	}
	return p
}

// memoryActions are the keys that act on the selected memory entry.
func (s memoryScreen) memoryActions(m store.MemoryEntry) map[string]func() *prompt {
	e, label := s.env, fmt.Sprintf("#%d", m.ID)
	acts := map[string]func() *prompt{}
	if m.Status == "pending" && !m.Stale {
		item := memoryItem(m)
		acts["a"] = func() *prompt { return item.approve(e) }
		acts["X"] = func() *prompt { return item.reject(e) }
	}
	if m.Status == "approved" && !m.Stale {
		acts["F"] = func() *prompt {
			return askYesNo("Forget memory "+label+"? Agents stop being told it (R restores it).", "forgetting memory "+label, func(_, token string) (string, error) {
				return "memory " + label + " forgotten", e.st.SetMemoryStaleWithToken(m.ID, true, token)
			})
		}
		acts["t"] = func() *prompt {
			return askYesNo("Reconfirm memory "+label+" is still true?", "reconfirming memory "+label, func(_, token string) (string, error) {
				return "memory " + label + " reconfirmed", e.st.TouchMemoryWithToken(m.ID, token)
			})
		}
	}
	if m.Stale {
		acts["R"] = func() *prompt {
			return askYesNo("Restore memory "+label+"? Agents are told it again.", "restoring memory "+label, func(_, token string) (string, error) {
				return "memory " + label + " restored", e.st.SetMemoryStaleWithToken(m.ID, false, token)
			})
		}
	}
	return acts
}

// promote asks what to make of a note, then the fields for that kind.
func (s memoryScreen) promote(n store.Note) *prompt {
	e, label := s.env, fmt.Sprintf("#%d", n.ID)
	return askOne("Promote note "+label+" into?", "", []string{"memory", "decision"}, "memory", func(kind, _ string) (string, error) {
		if kind == "memory" {
			return then(askFields("Memory from note "+label, "", []form.Field{
				{Name: "kind", Label: "Kind", Kind: form.FieldSelect, Options: []string{"lesson", "constraint", "pitfall", "operational", "failure_pattern"}},
				{Name: "area", Label: "Area", Placeholder: "cli, store, ..."},
			}, func(v map[string]string, _ string) (string, error) {
				id, err := e.st.PromoteNote(n.ID, "memory", store.PromoteOpts{MemoryKind: v["kind"], MemoryArea: strings.TrimSpace(v["area"])})
				return fmt.Sprintf("note %s promoted to memory #%d", label, id), err
			}))
		}
		return then(askFields("Decision from note "+label, "", []form.Field{
			{Name: "title", Label: "Title", Value: firstLine(n.Body), Validators: []form.Validator{form.Required()}, Width: 60},
			{Name: "context", Label: "Context", Width: 60},
			{Name: "decision", Label: "Decision", Value: firstLine(n.Body), Width: 60},
			{Name: "rationale", Label: "Rationale", Width: 60},
			{Name: "scope", Label: "Scope", Placeholder: "system, api, ...", Width: 60},
		}, func(v map[string]string, _ string) (string, error) {
			id, err := e.st.PromoteNote(n.ID, "decision", store.PromoteOpts{Title: v["title"], Context: v["context"], Decision: v["decision"], Rationale: v["rationale"], Scope: v["scope"]})
			return fmt.Sprintf("note %s promoted to decision #%d (proposed)", label, id), err
		}))
	})
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

func (s memoryScreen) capturing() bool { return s.prompt != nil }

func (s memoryScreen) update(msg tk.Msg) (screen, tk.Cmd) {
	switch msg := msg.(type) {
	case sizeMsg:
		s.width, s.height = msg.width, msg.height
	case tk.Key:
		return s.key(msg)
	}
	return s, nil
}

func (s memoryScreen) key(k tk.Key) (screen, tk.Cmd) {
	if s.prompt != nil {
		next, done := s.prompt.step(k)
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
	switch k.String() {
	case "n":
		s.notes, s.cursor, s.scroll = !s.notes, 0, 0
		return s, nil
	case "f":
		if !s.notes {
			s.filter, s.cursor, s.scroll = cycle(memoryFilters, s.filter), 0, 0
			next, err := s.load(s.env)
			if err != nil {
				return s, errCmd(err)
			}
			return next, nil
		}
	case "down", "j":
		s.cursor, s.scroll = min(s.cursor+1, max(s.rows()-1, 0)), 0
		return s, nil
	case "up", "k":
		s.cursor, s.scroll = max(s.cursor-1, 0), 0
		return s, nil
	case "pgdown", "space":
		s.scroll++
		return s, nil
	case "pgup":
		s.scroll = max(s.scroll-1, 0)
		return s, nil
	}
	if s.rows() == 0 {
		return s, nil
	}
	if s.notes {
		if k.String() == "enter" { // p is the shell's project switcher
			s.prompt = s.promote(s.unpromo[s.cursor])
		}
		return s, nil
	}
	if open, ok := s.memoryActions(s.memory[s.cursor])[k.String()]; ok {
		s.prompt = open()
	}
	return s, nil
}

func (s memoryScreen) view(width, height int) string {
	var head string
	if s.notes {
		head = sectionStyle.Render(fmt.Sprintf("Notes not yet promoted (%d)", len(s.unpromo))) + helpStyle.Render("   n shows memory")
	} else {
		head = sectionStyle.Render(fmt.Sprintf("Memory: %s (%d)", s.filter, len(s.memory))) + helpStyle.Render("   f filters, n shows notes")
	}
	out := []string{head}
	if s.rows() == 0 {
		return head + "\n\n" + helpStyle.Render("  nothing here")
	}
	lh := max(min(s.rows(), (s.height-1)*2/5), 1)
	from := max(min(s.cursor-lh/2, s.rows()-lh), 0)
	for i := from; i < min(from+lh, s.rows()); i++ {
		line := "  " + s.label(i)
		if i == s.cursor {
			line = sectionStyle.Render("> ") + s.label(i)
		}
		out = append(out, fitTo(line, s.width))
	}
	out = append(out, helpStyle.Render(strings.Repeat("─", max(s.width, 1))))
	if s.prompt != nil {
		return strings.Join(out, "\n") + "\n" + s.prompt.view()
	}
	ph := max(s.height-len(out), 1)
	preview := wrap(s.preview(s.cursor), s.width)
	out = append(out, window(preview, max(min(s.scroll, len(preview)-ph), 0), ph)...)
	return strings.Join(out, "\n")
}

func (s memoryScreen) keys() []widgets.Hint {
	if s.prompt != nil {
		return nil
	}
	h := []widgets.Hint{{Key: "↑/↓", Action: "move"}, {Key: "n", Action: "notes/memory"}}
	if s.notes {
		if s.rows() > 0 {
			h = append(h, widgets.Hint{Key: "enter", Action: "promote"})
		}
		return h
	}
	h = append(h, widgets.Hint{Key: "f", Action: "filter"})
	if s.rows() > 0 {
		names := map[string]string{"a": "approve", "X": "reject", "F": "forget", "t": "reconfirm", "R": "restore"}
		acts := s.memoryActions(s.memory[s.cursor])
		for _, key := range []string{"a", "X", "F", "t", "R"} {
			if _, ok := acts[key]; ok {
				h = append(h, widgets.Hint{Key: key, Action: names[key]})
			}
		}
	}
	return h
}
