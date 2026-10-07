package tui

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	tk "github.com/ows4444/tui"
	"github.com/ows4444/tui/widgets"

	"acline/internal/store"
)

// auditLimit is how many of the newest matching events the screen reads.
const auditLimit = 1000

// auditScreen is the audit trail: whether it verifies, its head and the
// newest seal, and the events, filtered by type, actor and task (the project
// is the shell's). A person can check the newest seal, seal the head, or
// check an anchor kept outside the store; each needs what `acline verify`
// needs.
type auditScreen struct {
	events []store.Event
	types  []string
	typ    string // "" any
	actor  string // "" any, human, agent
	task   *int64

	status []string // verify, head and seal lines
	cursor int
	prompt *prompt

	env           env
	width, height int
}

func (auditScreen) title() string { return "Audit" }

func (s auditScreen) load(e env) (screen, error) {
	s.env = e
	var err error
	if s.events, err = e.st.QueryEvents(store.EventFilter{ProjectID: e.projectID, TaskID: s.task, ActorType: s.actor, Type: s.typ, Limit: auditLimit}); err != nil {
		return s, err
	}
	if s.types, err = e.st.EventTypes(); err != nil {
		return s, err
	}
	verify, err := auditSection(e.st)
	if err != nil {
		return s, err
	}
	s.status = nil
	for _, r := range verify.rows {
		if verify.bad {
			s.status = append(s.status, errStyle.Render("audit trail "+r))
		} else {
			s.status = append(s.status, okStyle.Render("audit trail "+r))
		}
	}
	head, ok, err := e.st.ChainHead()
	if err != nil {
		return s, err
	}
	line := helpStyle.Render("head ") + "none yet"
	if ok {
		line = helpStyle.Render("head ") + head.String() + helpStyle.Render("   keep it outside the store to detect truncation; A checks one")
	}
	s.status = append(s.status, line)
	seals, err := e.st.QueryEvents(store.EventFilter{Type: "head_sealed", Limit: 1})
	if err != nil {
		return s, err
	}
	if len(seals) == 0 {
		s.status = append(s.status, helpStyle.Render("seal ")+"never sealed"+helpStyle.Render("   S seals the head with the approval token"))
	} else {
		seal := seals[0]
		covered := strings.Fields(seal.Message)
		at := ""
		if len(covered) > 1 {
			at = " covering event " + strings.SplitN(covered[1], ":", 2)[0]
		}
		s.status = append(s.status, helpStyle.Render("seal ")+fmt.Sprintf("newest is #%d%s, %s", seal.ID, clean(at), seal.CreatedAt)+helpStyle.Render("   c checks it (needs the token)"))
	}
	s.cursor = min(s.cursor, max(len(s.events)-1, 0))
	return s, nil
}

func (s auditScreen) reload() (screen, tk.Cmd) {
	next, err := s.load(s.env)
	if err != nil {
		return s, errCmd(err)
	}
	return next, nil
}

func (s auditScreen) row(ev store.Event) string {
	task := ""
	if ev.TaskID.Valid {
		task = "#" + strconv.FormatInt(ev.TaskID.Int64, 10)
	}
	return fmt.Sprintf("%7d  %s  %-20s %-22s %-6s %s", ev.ID, helpStyle.Render(ev.CreatedAt), ev.Type, fitTo(by(ev.ActorType.String, ev.ActorID.String), 22), task, clean(ev.Message))
}

func (s auditScreen) preview(ev store.Event) []string {
	p := []string{sectionStyle.Render(fmt.Sprintf("Event #%d, %s", ev.ID, ev.Type)),
		fmt.Sprintf("%s by %s%s", ev.CreatedAt, by(ev.ActorType.String, ev.ActorID.String), modelOf(ev.Model.String))}
	var refs []string
	if ev.TaskID.Valid {
		refs = append(refs, fmt.Sprintf("task #%d", ev.TaskID.Int64))
	}
	if ev.SessionID.Valid {
		refs = append(refs, fmt.Sprintf("session #%d", ev.SessionID.Int64))
	}
	if len(refs) > 0 {
		p = append(p, strings.Join(refs, ", "))
	}
	p = append(p, "  "+clean(ev.Message))
	if ev.Hash.Valid {
		p = append(p, helpStyle.Render("hash "+ev.Hash.String+"  prev "+ev.PrevHash.String))
	}
	return p
}

func modelOf(m string) string {
	if m == "" {
		return ""
	}
	return " (" + clean(m) + ")"
}

func (s auditScreen) capturing() bool { return s.prompt != nil }

func (s auditScreen) update(msg tk.Msg) (screen, tk.Cmd) {
	switch msg := msg.(type) {
	case sizeMsg:
		s.width, s.height = msg.width, msg.height
	case tk.Key:
		return s.key(msg)
	}
	return s, nil
}

func (s auditScreen) key(k tk.Key) (screen, tk.Cmd) {
	if s.prompt != nil {
		next, done := s.prompt.step(k)
		s.prompt = next
		if done == nil {
			return s, nil
		}
		var filter errTaskFilter
		if errors.As(done.err, &filter) {
			s.task, s.cursor = filter.task, 0
			return s.reload()
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
	e := s.env
	switch k.String() {
	case "down", "j":
		s.cursor = min(s.cursor+1, max(len(s.events)-1, 0))
	case "up", "k":
		s.cursor = max(s.cursor-1, 0)
	case "pgdown", "space":
		s.cursor = min(s.cursor+max(s.listHeight()-1, 1), max(len(s.events)-1, 0))
	case "pgup":
		s.cursor = max(s.cursor-max(s.listHeight()-1, 1), 0)
	case "home", "g":
		s.cursor = 0
	case "y":
		s.typ, s.cursor = cycle(append([]string{""}, s.types...), s.typ), 0
		return s.reload()
	case "w":
		s.actor, s.cursor = cycle([]string{"", "human", "agent"}, s.actor), 0
		return s.reload()
	case "x":
		s.typ, s.actor, s.task, s.cursor = "", "", nil, 0
		return s.reload()
	case "t":
		s.prompt = askLine("Show the events of which task? (empty: every task)", "", func(v, _ string) (string, error) {
			v = strings.TrimPrefix(strings.TrimSpace(v), "#")
			if v == "" {
				return "", errTaskFilter{nil}
			}
			id, err := strconv.ParseInt(v, 10, 64)
			if err != nil || id <= 0 {
				return "", fmt.Errorf("%q is not a task id", v)
			}
			return "", errTaskFilter{&id}
		})
	case "c":
		s.prompt = askYesNo("Check the newest head seal made with your token?", "checking the head seal", func(_, token string) (string, error) {
			r, err := e.st.CheckHeadSeal(token)
			if err != nil {
				return "", err
			}
			if !r.Sealed {
				return "no seal made with this token yet", nil
			}
			info := fmt.Sprintf("seal #%d intact: nothing up to event #%d was rewritten", r.SealID, r.Head.ID)
			if r.Foreign > 0 {
				info += fmt.Sprintf(" (%d newer seal(s) made with another token were not trusted)", r.Foreign)
			}
			return info, nil
		})
	case "S":
		s.prompt = askYesNo("Seal the head of the audit trail with your token?", "sealing the head", func(_, token string) (string, error) {
			head, err := e.st.SealHead(token)
			return fmt.Sprintf("head sealed at event #%d; also keep %s outside the store", head.ID, head), err
		})
	case "A":
		s.prompt = askLine("Anchor to check (<event-id>:<hash>, as printed by `acline verify --head`):", "", func(v, _ string) (string, error) {
			anchor, err := store.ParseAnchor(v)
			if err != nil {
				return "", err
			}
			if err := e.st.CheckAnchor(anchor); err != nil {
				return "", err
			}
			return fmt.Sprintf("anchor %d matches: nothing up to event #%d was truncated or rewritten", anchor.ID, anchor.ID), nil
		})
	}
	return s, nil
}

// errTaskFilter carries the task filter out of its prompt.
type errTaskFilter struct{ task *int64 }

func (errTaskFilter) Error() string { return "task filter" }

func (s auditScreen) listHeight() int {
	return max(s.height-len(s.status)-2-previewLines-1, 3)
}

// previewLines is the room under the list for the selected event.
const previewLines = 5

func (s auditScreen) filters() string {
	typ, actor, task := "any", "any", "any"
	if s.typ != "" {
		typ = s.typ
	}
	if s.actor != "" {
		actor = s.actor
	}
	if s.task != nil {
		task = "#" + strconv.FormatInt(*s.task, 10)
	}
	shown := fmt.Sprintf("%d events", len(s.events))
	if len(s.events) == auditLimit {
		shown = fmt.Sprintf("newest %d events", auditLimit)
	}
	return helpStyle.Render("type: ") + typ + helpStyle.Render("  actor: ") + actor + helpStyle.Render("  task: ") + task + helpStyle.Render("  ·  ") + shown
}

func (s auditScreen) view(width, height int) string {
	out := make([]string, 0, s.height)
	for _, l := range s.status {
		out = append(out, fitTo(l, s.width))
	}
	out = append(out, "", s.filters())
	if s.prompt != nil {
		return strings.Join(out, "\n") + "\n" + s.prompt.view()
	}
	if len(s.events) == 0 {
		return strings.Join(out, "\n") + "\n" + helpStyle.Render("  no event matches (x clears the filters)")
	}
	lh := s.listHeight()
	from := max(min(s.cursor-lh/2, len(s.events)-lh), 0)
	for i := from; i < min(from+lh, len(s.events)); i++ {
		line := "  " + s.row(s.events[i])
		if i == s.cursor {
			line = sectionStyle.Render("> ") + s.row(s.events[i])
		}
		out = append(out, fitTo(line, s.width))
	}
	out = append(out, helpStyle.Render(strings.Repeat("─", max(s.width, 1))))
	out = append(out, window(wrap(s.preview(s.events[s.cursor]), s.width), 0, previewLines)...)
	return strings.Join(out, "\n")
}

func (s auditScreen) keys() []widgets.Hint {
	if s.prompt != nil {
		return nil
	}
	return []widgets.Hint{{Key: "↑/↓", Action: "move"}, {Key: "y", Action: "type"}, {Key: "w", Action: "actor"}, {Key: "t", Action: "task"},
		{Key: "x", Action: "clear"}, {Key: "c", Action: "check seal"}, {Key: "S", Action: "seal head"}, {Key: "A", Action: "check anchor"}}
}
