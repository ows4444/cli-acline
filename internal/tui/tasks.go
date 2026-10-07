package tui

import (
	"errors"
	"sort"
	"strconv"
	"strings"

	tk "github.com/ows4444/tui"
	"github.com/ows4444/tui/datatable"
	"github.com/ows4444/tui/textinput"
	"github.com/ows4444/tui/widgets"

	"acline/internal/store"
)

// Filter values cycle in these orders; the first is "no filter" (for status,
// every task not done or cancelled).
var (
	statusFilters = []string{"open", "all", "backlog", "todo", "in_progress", "blocked", "review", "done", "cancelled"}
	riskFilters   = []string{"any", "low", "medium", "high", "critical"}
)

// openTaskMsg asks the shell to show one task in detail.
type openTaskMsg struct{ id int64 }

// tasksScreen lists the project's tasks in a table that can be filtered by
// status, risk and area, searched, and sorted by any column.
type tasksScreen struct {
	all      []store.Task
	projects map[int64]string // names, for the project column when every project is shown
	scoped   bool             // one project is in scope: no project column

	status, risk, area string
	areas              []string // the areas the tasks use, for the area filter
	search             textinput.Model
	searching          bool

	table         datatable.Model
	shown         []store.Task // the rows of table, in the table's order
	width, height int

	env        env
	detail     taskDetail // the task opened with enter, while detailOpen
	detailOpen bool
	prompt     *prompt       // an action waiting on the person, over the detail
	running    *checkRunning // a check running from the detail
}

func (tasksScreen) title() string { return "Tasks" }

func (s tasksScreen) load(e env) (screen, error) {
	tasks, err := e.st.ListTasks(store.TaskFilter{ProjectID: e.projectID, All: true}) // closed ones too, for the status filter
	if err != nil {
		return s, err
	}
	s.all, s.scoped, s.env = tasks, e.projectID != nil, e
	if s.detailOpen {
		if s, err = s.openDetail(s.detail.task.ID); err != nil {
			return s, err
		}
	}
	s.projects = map[int64]string{}
	if !s.scoped {
		projects, err := e.st.ListProjects()
		if err != nil {
			return s, err
		}
		for _, p := range projects {
			s.projects[p.ID] = clean(p.Name)
		}
	}
	seen := map[string]bool{}
	s.areas = nil
	for _, t := range tasks {
		if a := t.Area.String; a != "" && !seen[a] {
			seen[a] = true
			s.areas = append(s.areas, a)
		}
	}
	sort.Strings(s.areas)
	if s.status == "" {
		s.status, s.risk, s.area = statusFilters[0], riskFilters[0], ""
		s.search = textinput.NewSearch()
		s.search.Prompt = "/"
	}
	if s.area != "" && !seen[s.area] {
		s.area = ""
	}
	return s.refilter(), nil
}

func (s tasksScreen) headers() []string {
	h := []string{"ID", "Status", "Priority", "Risk", "Autonomy", "Area"}
	if !s.scoped {
		h = append(h, "Project")
	}
	return append(h, "Title")
}

func (s tasksScreen) row(t store.Task) []string {
	status := t.Status
	if t.Deferred {
		status += " (deferred)"
	}
	r := []string{strconv.FormatInt(t.ID, 10), status, t.Priority, t.Risk, t.Autonomy, clean(t.Area.String)}
	if !s.scoped {
		r = append(r, s.projects[t.ProjectID.Int64])
	}
	return append(r, clean(t.Title))
}

func (s tasksScreen) matches(t store.Task) bool {
	switch s.status {
	case "open":
		if t.Status == "done" || t.Status == "cancelled" {
			return false
		}
	case "all":
	default:
		if t.Status != s.status {
			return false
		}
	}
	if s.risk != "any" && t.Risk != s.risk {
		return false
	}
	if s.area != "" && t.Area.String != s.area {
		return false
	}
	if q := strings.ToLower(strings.TrimSpace(s.search.Value())); q != "" {
		hay := strings.ToLower(strings.Join([]string{strconv.FormatInt(t.ID, 10), t.Title, t.Description, t.Area.String}, " "))
		for _, word := range strings.Fields(q) {
			if !strings.Contains(hay, word) {
				return false
			}
		}
	}
	return true
}

// refilter rebuilds the table's rows from the filters, keeping the sort the
// person chose and the task under the cursor.
func (s tasksScreen) refilter() tasksScreen {
	var current int64
	if c := s.table.Cursor(); c < len(s.shown) {
		current = s.shown[c].ID
	}
	s.shown = s.shown[:0:0]
	for _, t := range s.all {
		if s.matches(t) {
			s.shown = append(s.shown, t)
		}
	}
	col, desc, sorted := s.table.Sorted()
	headers := s.headers()
	if len(s.table.Headers) != len(headers) {
		sorted = false // the columns changed (project column): the old sort no longer applies
	}
	if sorted {
		sortTasks(s.shown, func(t store.Task) string { return s.row(t)[col] }, desc)
	}
	rows := make([][]string, len(s.shown))
	cursor := 0
	for i, t := range s.shown {
		rows[i] = s.row(t)
		if t.ID == current {
			cursor = i
		}
	}
	if len(s.table.Headers) != len(headers) {
		s.table = datatable.New(headers, rows)
	} else {
		s.table.SetRows(rows)
	}
	s.table.SetCursor(cursor)
	return s.sized()
}

// sortTasks orders tasks by a cell as the table sorts a column: numbers as
// numbers, other text as text, stable.
func sortTasks(tasks []store.Task, cell func(store.Task) string, desc bool) {
	less := func(x, y string) bool {
		fx, ex := strconv.ParseFloat(x, 64)
		fy, ey := strconv.ParseFloat(y, 64)
		if ex == nil && ey == nil {
			return fx < fy
		}
		return x < y
	}
	sort.SliceStable(tasks, func(a, b int) bool {
		x, y := cell(tasks[a]), cell(tasks[b])
		if desc {
			return less(y, x)
		}
		return less(x, y)
	})
}

// filterLines is the filter bar above the table.
const filterLines = 2

func (s tasksScreen) sized() tasksScreen {
	s.table.Width = s.width
	s.table.Height = max(s.height-filterLines-2, 1) // the table's own header and divider
	s.search.Width = max(s.width-4, 10)
	return s
}

// capturing reports that typed text belongs to the screen (the search box),
// so the shell's single-key shortcuts must not take it.
func (s tasksScreen) capturing() bool { return s.searching || s.prompt != nil || s.running != nil }

func cycle(values []string, current string) string {
	for i, v := range values {
		if v == current {
			return values[(i+1)%len(values)]
		}
	}
	return values[0]
}

// openDetail shows task id in detail, keeping the column and scroll the
// person had when it is the task already shown.
func (s tasksScreen) openDetail(id int64) (tasksScreen, error) {
	d, err := loadDetail(s.env, id)
	if err != nil {
		return s, err
	}
	if s.detailOpen && s.detail.task.ID == id {
		d.focus, d.scroll = s.detail.focus, s.detail.scroll
	}
	d.width, d.height = s.width, s.height
	s.detail, s.detailOpen = d.clamp(), true
	return s, nil
}

func (s tasksScreen) update(msg tk.Msg) (screen, tk.Cmd) {
	if s.running != nil {
		return s.runUpdate(msg)
	}
	if open, ok := msg.(openTaskMsg); ok {
		next, err := s.openDetail(open.id)
		if err != nil {
			return s, errCmd(err)
		}
		return next, nil
	}
	if s.detailOpen {
		if k, ok := msg.(tk.Key); ok {
			return s.detailKey(k)
		}
		if size, ok := msg.(sizeMsg); ok {
			s.width, s.height = size.width, size.height
			s = s.sized()
		}
		s.detail = s.detail.update(msg)
		return s, nil
	}
	switch msg := msg.(type) {
	case sizeMsg:
		s.width, s.height = msg.width, msg.height
		return s.sized(), nil
	case datatable.SelectedMsg:
		if msg.Row < len(s.shown) {
			id := s.shown[msg.Row].ID
			return s, func() tk.Msg { return openTaskMsg{id} }
		}
		return s, nil
	case tk.Key:
		if s.searching {
			switch msg.String() {
			case "esc":
				s.searching = false
				s.search.Blur()
				s.search.SetValue("")
				return s.refilter(), nil
			case "enter":
				s.searching = false
				s.search.Blur()
				return s, nil
			}
			var cmd tk.Cmd
			s.search, cmd = s.search.Update(msg)
			return s.refilter(), cmd
		}
		switch msg.String() {
		case "/":
			s.searching = true
			return s, s.search.Focus()
		case "f":
			s.status = cycle(statusFilters, s.status)
			return s.refilter(), nil
		case "R":
			s.risk = cycle(riskFilters, s.risk)
			return s.refilter(), nil
		case "a":
			s.area = cycle(append([]string{""}, s.areas...), s.area)
			return s.refilter(), nil
		case "x":
			s.status, s.risk, s.area = statusFilters[0], riskFilters[0], ""
			s.search.SetValue("")
			return s.refilter(), nil
		}
	}
	var cmd tk.Cmd
	s.table, cmd = s.table.Update(msg)
	return s, cmd
}

func (s tasksScreen) view(width, height int) string {
	if s.running != nil {
		return s.running.view()
	}
	if s.detailOpen {
		if s.prompt != nil {
			// The prompt takes the top of the screen; the detail stays visible below.
			p := s.prompt.view()
			d := s.detail
			d.height = max(d.height-countLines(p)-1, 1)
			return p + "\n" + helpStyle.Render(strings.Repeat("─", max(s.width, 1))) + "\n" + d.clamp().view()
		}
		return s.detail.view()
	}
	area := s.area
	if area == "" {
		area = "any"
	}
	bar := helpStyle.Render("status: ") + s.status + helpStyle.Render("  risk: ") + s.risk + helpStyle.Render("  area: ") + clean(area) +
		helpStyle.Render("  ·  ") + strconv.Itoa(len(s.shown)) + helpStyle.Render(" of ") + strconv.Itoa(len(s.all)) + helpStyle.Render(" tasks")
	search := helpStyle.Render("/ to search")
	if s.searching || s.search.Value() != "" {
		search = s.search.View()
	}
	if len(s.shown) == 0 {
		return bar + "\n" + search + "\n\n" + helpStyle.Render("  no task matches (x clears the filters)")
	}
	return bar + "\n" + search + "\n" + s.table.View()
}

func (s tasksScreen) keys() []widgets.Hint {
	if s.running != nil {
		return []widgets.Hint{{Key: "esc", Action: "stop the check"}, {Key: "↑/↓", Action: "scroll the output"}}
	}
	if s.prompt != nil {
		return nil
	}
	if s.detailOpen {
		return append(s.detail.keys(), s.actionKeys()...)
	}
	if s.searching {
		return []widgets.Hint{{Key: "enter", Action: "keep search"}, {Key: "esc", Action: "clear search"}}
	}
	return []widgets.Hint{
		{Key: "enter", Action: "open"}, {Key: "/", Action: "search"}, {Key: "f", Action: "status"},
		{Key: "R", Action: "risk"}, {Key: "a", Action: "area"}, {Key: "x", Action: "clear filters"},
		{Key: "←/→ s", Action: "sort by column"},
	}
}

func countLines(s string) int { return strings.Count(s, "\n") + 1 }

// detailKey handles a key while a task is open: the prompt first, then the
// action keys, then esc (back to the table), then scrolling.
func (s tasksScreen) detailKey(k tk.Key) (screen, tk.Cmd) {
	if s.prompt != nil {
		next, done := s.prompt.step(k)
		s.prompt = next
		if done == nil {
			return s, nil
		}
		return s.afterAction(*done)
	}
	for _, a := range s.taskActions() {
		if k.String() == a.key {
			p, err := a.open(s)
			if err != nil {
				return s, errCmd(err)
			}
			s.prompt = p
			return s, nil
		}
	}
	if k.String() == "esc" {
		s.detailOpen = false
		return s, nil
	}
	s.detail = s.detail.update(k)
	return s, nil
}

// afterAction rereads the task and the table, and reports the outcome.
func (s tasksScreen) afterAction(done actionDoneMsg) (screen, tk.Cmd) {
	next, err := s.load(s.env)
	if err != nil {
		return s, errCmd(err)
	}
	var run errRunCheck
	if errors.As(done.err, &run) {
		return next.(tasksScreen).startCheck(run.kind)
	}
	if done.err != nil {
		return next, errCmd(done.err)
	}
	return next, infoCmd(done.info)
}
