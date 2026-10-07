package tui

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"acline/internal/store"
	tk "github.com/ows4444/tui"
	"github.com/ows4444/tui/ansi"
	"github.com/ows4444/tui/commandpalette"
	"github.com/ows4444/tui/helpscreen"
	"github.com/ows4444/tui/picker"
	"github.com/ows4444/tui/tabs"
	"github.com/ows4444/tui/widgets"
)

// screen is one tab of the shell. The shell owns the global keys and the
// overlays; a screen gets every other key while it is showing.
type screen interface {
	title() string
	// load reads what the screen shows, for the project in scope.
	load(e env) (screen, error)
	update(msg tk.Msg) (screen, tk.Cmd)
	view(width, height int) string
	// keys are the screen's own key hints, for the footer and the help screen.
	keys() []widgets.Hint
}

// env is what a screen reads from: the store, the project in scope (nil: every
// project) and how to fingerprint a directory, for the completion gate.
type env struct {
	st        *store.Store
	projectID *int64
	hash      func(dir string) string
	self      string    // the acline binary, for what runs in its own process
	cleanup   *cleanups // what must stop when the TUI exits
}

// action is one entry of the command palette: everything the TUI can do is
// one, so the palette lists all of it.
type action struct {
	name, desc string
	run        func(m model) (model, tk.Cmd)
}

type mode int

const (
	modeNormal  mode = iota
	modeHelp         // the key help over the screen
	modePalette      // the command palette
	modeProject      // the project switcher
)

// model is the shell every screen sits in: who is acting, on which project,
// whether the approval token makes that identity enforced, and the active
// session; tabs for the screens; key hints in the footer.
type model struct {
	st        *store.Store
	hash      func(dir string) string
	self      string
	cleanup   *cleanups
	projectID *int64
	project   string // its name, or "all projects"
	token     bool
	session   string

	screens []screen
	tabs    tabs.Model
	active  int

	mode     mode
	help     helpscreen.Model
	palette  commandpalette.Model
	projects picker.Model
	choices  []*int64 // the project each switcher row selects

	err           string // the last failure, shown until the next key
	info          string // the last action's outcome, shown until the next key
	width, height int

	// Live refresh: every watchEvery (0: off) the shell checks the store's
	// data_version, which moves when anything else (a hook, an agent, the CLI
	// in another terminal) commits. Then it rereads the header and the screen
	// showing; the others are marked stale and reread when shown, since some
	// (the dashboard's gates, the audit trail's verify) are costly.
	watchEvery time.Duration
	version    int64
	stale      []bool
}

func newModel(st *store.Store, projectID *int64, hash func(string) string) (model, error) {
	return newShell(st, projectID, hash, defaultScreens()...)
}

// defaultScreens are the tabs, in order.
func defaultScreens() []screen {
	return []screen{dashboardScreen{}, tasksScreen{}, reviewScreen{}, specsScreen{}, memoryScreen{}, auditScreen{}, orchestratorScreen{}}
}

// newShell is the shell around screens, scoped to projectID.
func newShell(st *store.Store, projectID *int64, hash func(string) string, screens ...screen) (model, error) {
	m := model{st: st, hash: hash, screens: screens}
	labels := make([]string, len(m.screens))
	for i, s := range m.screens {
		labels[i] = s.title()
	}
	m.tabs = tabs.New(labels...)
	return m.scope(projectID)
}

// scope points every screen at projectID (nil: every project) and reloads.
func (m model) scope(projectID *int64) (model, error) {
	m.projectID, m.project = projectID, "all projects"
	if projectID != nil {
		projects, err := m.st.ListProjects()
		if err != nil {
			return m, err
		}
		for _, p := range projects {
			if p.ID == *projectID {
				m.project = p.Name
			}
		}
	}
	return m.reload()
}

// reload rereads the header and every screen from the store.
func (m model) reload() (model, error) {
	m, err := m.reloadHeader()
	if err != nil {
		return m, err
	}
	for i, s := range m.screens {
		if m.screens[i], err = s.load(m.env()); err != nil {
			return m, err
		}
	}
	m.stale = make([]bool, len(m.screens))
	return m.sized(), nil
}

// reloadHeader rereads what the header shows, and notes the store's version.
func (m model) reloadHeader() (model, error) {
	v, err := m.st.DataVersion()
	if err != nil {
		return m, err
	}
	m.version = v
	enabled, err := m.st.ApprovalTokenEnabled()
	if err != nil {
		return m, err
	}
	m.token = enabled
	m.session = "no active session"
	if sess, err := m.st.CurrentSession(); err == nil {
		m.session = fmt.Sprintf("session #%d", sess.ID)
	} else if !errors.Is(err, store.ErrNoActiveSession) {
		return m, err
	}
	return m, nil
}

type watchMsg struct{}

// watch ticks every watchEvery for as long as the program runs. It is the
// Program's own ticker, so a test's fake clock drives it.
func (m model) watch() tk.Cmd {
	if m.watchEvery <= 0 {
		return nil
	}
	cmd, _ := tk.Every(m.watchEvery, func(time.Time) tk.Msg { return watchMsg{} })
	return cmd
}

// changed rereads the header and the screen showing, when the store moved
// under the TUI, and marks the other screens stale.
func (m model) changed() model {
	v, err := m.st.DataVersion()
	if err != nil {
		return m
	}
	if v != m.version {
		if m, err = m.reloadHeader(); err != nil {
			m.err = err.Error()
			return m
		}
		for i := range m.stale {
			m.stale[i] = true
		}
	}
	return m.fresh()
}

// fresh rereads the screen showing if it is stale. Not while it holds the
// keyboard (a prompt, a search, a running check): the rows must not move
// under a person who is answering about one of them. It stays stale, and the
// next tick after they finish rereads it.
func (m model) fresh() model {
	i := m.active
	if i >= len(m.stale) || !m.stale[i] || m.capturing() {
		return m
	}
	var err error
	if m.screens[i], err = m.screens[i].load(m.env()); err != nil {
		m.err = err.Error()
	}
	m.stale[i] = false
	return m.sized()
}

// capturing reports whether the screen showing holds the keyboard.
func (m model) capturing() bool {
	c, ok := m.screens[m.active].(interface{ capturing() bool })
	return ok && c.capturing()
}

func (m model) env() env {
	return env{st: m.st, projectID: m.projectID, hash: m.hash, self: m.self, cleanup: m.cleanup}
}

// actions is everything the palette offers, in the order it lists them.
func (m model) actions() []action {
	var out []action
	for i, s := range m.screens {
		out = append(out, action{"Go to " + s.title(), fmt.Sprintf("show the %s tab (%d)", s.title(), i+1), func(m model) (model, tk.Cmd) {
			return m.show(i), nil
		}})
	}
	return append(out,
		action{"Switch project", "show another project, or all of them (p)", func(m model) (model, tk.Cmd) { return m.openProjects() }},
		action{"Refresh", "reread everything from the store (r)", func(m model) (model, tk.Cmd) { return m.refresh(), nil }},
		action{"Help", "list every key (?)", func(m model) (model, tk.Cmd) { return m.openHelp(), nil }},
		action{"Quit", "leave acline tui (q)", func(m model) (model, tk.Cmd) { return m, tk.Quit() }},
	)
}

// globalKeys are the shell's own keys, shown in the footer and the help screen.
var globalKeys = []widgets.Hint{
	{Key: "tab/shift+tab", Action: "next/previous tab"},
	{Key: "1-9", Action: "go to tab"},
	{Key: ": or ctrl+k", Action: "command palette"},
	{Key: "p", Action: "switch project"},
	{Key: "r", Action: "refresh"},
	{Key: "?", Action: "help"},
	{Key: "q", Action: "quit"},
}

func (m model) show(i int) model {
	m.active = i
	m.tabs.SetActive(i)
	m.mode = modeNormal
	return m.fresh() // the store may have changed while another tab showed
}

func (m model) refresh() model {
	m.mode = modeNormal
	r, err := m.reload()
	if err != nil {
		m.err = err.Error()
		return m
	}
	return r
}

func (m model) openHelp() model {
	hints := append(append([]widgets.Hint{}, m.screens[m.active].keys()...), globalKeys...)
	m.help = helpscreen.New(hints...)
	m.mode = modeHelp
	return m
}

func (m model) openPalette() (model, tk.Cmd) {
	acts := m.actions()
	cmds := make([]commandpalette.Command, len(acts))
	for i, a := range acts {
		cmds[i] = commandpalette.Command{Name: a.name, Description: a.desc}
	}
	m.palette = commandpalette.New(cmds...)
	m.mode = modePalette
	return m, m.palette.Init()
}

func (m model) openProjects() (model, tk.Cmd) {
	projects, err := m.st.ListProjects()
	if err != nil {
		m.err = err.Error()
		return m, nil
	}
	items := []picker.Item{{Label: "All projects"}}
	m.choices = []*int64{nil}
	cursor := 0
	for _, p := range projects {
		items = append(items, picker.Item{Label: p.Name})
		id := p.ID
		m.choices = append(m.choices, &id)
		if m.projectID != nil && *m.projectID == p.ID {
			cursor = len(items) - 1
		}
	}
	m.projects = picker.New(items...)
	m.projects.SetCursor(cursor)
	m.mode = modeProject
	return m, nil
}

func (m model) Init() tk.Cmd { return m.watch() }

func (m model) Update(msg tk.Msg) (tk.Model, tk.Cmd) {
	switch msg := msg.(type) {
	case tk.ResizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m.sized(), nil
	case watchMsg:
		return m.changed(), nil
	case commandpalette.SelectedMsg:
		m.mode = modeNormal
		for _, a := range m.actions() {
			if a.name == msg.Command.Name {
				return a.run(m)
			}
		}
		return m, nil
	case picker.SelectedMsg:
		if m.mode != modeProject {
			break
		}
		m.mode = modeNormal
		next, err := m.scope(m.choices[msg.Index])
		if err != nil {
			m.err = err.Error()
			return m, nil
		}
		return next, nil
	case errMsg:
		m.err, m.info = msg.err.Error(), ""
		return m.sized(), nil // the message takes rows from the screen
	case infoMsg:
		m.info, m.err = string(msg), ""
		return m.sized(), nil
	case openTaskMsg:
		for i, s := range m.screens { // whichever screen asked, the Tasks tab shows it
			if _, ok := s.(tasksScreen); ok {
				m = m.show(i)
				break
			}
		}
		return m.toScreen(msg)
	case tk.Key:
		if m.err != "" || m.info != "" {
			m.err, m.info = "", ""
			m = m.sized()
		}
		if msg.Type == tk.KeyCtrlC {
			return m, tk.Quit()
		}
		return m.key(msg)
	}
	return m.toScreen(msg)
}

// key handles a key press: an open overlay takes it first, then the shell's
// global keys, then the screen.
func (m model) key(k tk.Key) (tk.Model, tk.Cmd) {
	switch m.mode {
	case modeHelp:
		if k.Type == tk.KeyEsc || k.Type == tk.KeyEnter || isRune(k, "?") || isRune(k, "q") {
			m.mode = modeNormal
		}
		return m, nil
	case modePalette:
		if k.Type == tk.KeyEsc {
			m.mode = modeNormal
			return m, nil
		}
		var cmd tk.Cmd
		m.palette, cmd = m.palette.Update(k)
		return m, cmd
	case modeProject:
		if k.Type == tk.KeyEsc || isRune(k, "q") {
			m.mode = modeNormal
			return m, nil
		}
		var cmd tk.Cmd
		m.projects, cmd = m.projects.Update(k)
		return m, cmd
	}

	// A screen taking typed text (a search box) gets every key but ctrl+c.
	if m.capturing() {
		return m.toScreen(k)
	}
	switch {
	case isRune(k, "q"):
		return m, tk.Quit()
	case isRune(k, "?"):
		return m.openHelp(), nil
	case isRune(k, ":") || k.String() == "ctrl+k":
		return m.openPalette()
	case isRune(k, "p"):
		return m.openProjects()
	case isRune(k, "r"):
		return m.refresh(), nil
	case k.String() == "tab":
		return m.show((m.active + 1) % len(m.screens)), nil
	case k.String() == "shift+tab":
		return m.show((m.active + len(m.screens) - 1) % len(m.screens)), nil
	case k.Type == tk.KeyRunes && len(k.Text) == 1 && k.Text[0] >= '1' && k.Text[0] <= '9':
		if i := int(k.Text[0] - '1'); i < len(m.screens) {
			return m.show(i), nil
		}
		return m, nil
	}
	return m.toScreen(k)
}

func (m model) toScreen(msg tk.Msg) (tk.Model, tk.Cmd) {
	var cmd tk.Cmd
	m.screens[m.active], cmd = m.screens[m.active].update(msg)
	return m, cmd
}

// maxErrLines is how many lines an error may take above the footer.
const maxErrLines = 4

// errMsg reports a failure from a screen; the shell shows it until the next key.
type errMsg struct{ err error }

func errCmd(err error) tk.Cmd { return func() tk.Msg { return errMsg{err} } }

// infoMsg reports what an action did; the shell shows it until the next key.
type infoMsg string

func infoCmd(s string) tk.Cmd { return func() tk.Msg { return infoMsg(s) } }

func isRune(k tk.Key, s string) bool { return k.Type == tk.KeyRunes && k.Text == s }

var (
	headerStyle = ansi.NewStyle().Bold().Reverse()
	warnStyle   = ansi.NewStyle().Bold().Foreground(ansi.BrightYellow)
	errStyle    = ansi.NewStyle().Bold().Foreground(ansi.BrightRed)
	helpStyle   = ansi.NewStyle().Faint()
)

func (m model) header() string {
	token := "token: on"
	if !m.token {
		token = "token: OFF (identity is self-declared)"
	}
	actor := m.st.Actor.Type + "/" + m.st.Actor.ID
	return clean(strings.Join([]string{"acline", m.project, actor, token, m.session}, " · "))
}

func (m model) footer() string {
	hints := append(append([]widgets.Hint{}, m.screens[m.active].keys()...),
		widgets.Hint{Key: ":", Action: "actions"}, widgets.Hint{Key: "?", Action: "help"}, widgets.Hint{Key: "q", Action: "quit"})
	return widgets.KeyHints("  ", hints...)
}

func (m model) fit(s string) string {
	if m.width > 0 {
		return ansi.Truncate(s, m.width)
	}
	return s
}

func (m model) View() string { return m.render(true) }

// render draws the screen; fill pads it to the terminal's height.
func (m model) render(fill bool) string {
	top, bottom := m.top(), m.bottom()
	bodyHeight := m.bodyHeight()
	var body string
	switch m.mode {
	case modePalette:
		body = "Run an action (type to filter, enter runs, esc closes):\n\n" + m.palette.View()
		if !m.palette.IsOpen() {
			var names []string
			for _, a := range m.actions() {
				names = append(names, "  "+a.name+helpStyle.Render("  "+a.desc))
			}
			body += "\n\n" + strings.Join(names, "\n")
		}
	case modeProject:
		body = "Show which project? (enter selects, esc closes)\n\n" + m.projects.View()
	default:
		body = m.screens[m.active].view(m.width, bodyHeight)
	}
	// Cut a body taller than the room left, so the header never scrolls away.
	if lines := strings.Split(body, "\n"); bodyHeight > 0 && len(lines) > bodyHeight {
		body = strings.Join(lines[:bodyHeight], "\n")
	}
	// Fill the screen, so the footer sits at the bottom and an overlay has room.
	if pad := bodyHeight - strings.Count(body, "\n") - 1; fill && pad > 0 {
		body += strings.Repeat("\n", pad)
	}
	out := top + body + bottom
	if m.mode == modeHelp {
		out = m.help.Render(out)
	}
	return out
}

func (m model) top() string {
	var top strings.Builder
	top.WriteString(headerStyle.Render(m.fit(m.header())) + "\n")
	if !m.token {
		top.WriteString(warnStyle.Render(m.fit("No approval token: anything that sets ACLINE_ACTOR_TYPE=human can approve. Run `acline auth init`.")) + "\n")
	}
	top.WriteString(m.tabs.View() + "\n\n")
	return top.String()
}

func (m model) bottom() string {
	bottom := "\n"
	if m.err != "" {
		// An error can carry several blockers: wrap it (up to a few lines) rather
		// than cut off the part that says what to do.
		lines := wrap([]string{"error: " + clean(m.err)}, m.width)
		if len(lines) > maxErrLines {
			lines = append(lines[:maxErrLines-1], m.fit(lines[maxErrLines-1])+"…")
		}
		for _, l := range lines {
			bottom += errStyle.Render(m.fit(l)) + "\n"
		}
	}
	if m.info != "" {
		bottom += okStyle.Render(m.fit(clean(m.info))) + "\n"
	}
	return bottom + helpStyle.Render(m.fit(m.footer()))
}

// bodyHeight is the rows left for the screen between the header and the footer.
func (m model) bodyHeight() int {
	return m.height - strings.Count(m.top(), "\n") - strings.Count(m.bottom(), "\n")
}

// sizeMsg tells a screen the room it has, so it can scroll within it. The
// shell sends it on a resize and after every reload.
type sizeMsg struct{ width, height int }

func (m model) sized() model {
	for i, s := range m.screens {
		m.screens[i], _ = s.update(sizeMsg{m.width, m.bodyHeight()})
	}
	return m
}

// Linearize is the accessible-mode rendering: the same content, unstyled and
// without the blank lines that fill a screen.
func (m model) Linearize() string { return ansi.StripANSI(m.render(false)) }
