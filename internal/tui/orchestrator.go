package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	tk "github.com/ows4444/tui"
	"github.com/ows4444/tui/ansi"
	"github.com/ows4444/tui/form"
	"github.com/ows4444/tui/logview"
	"github.com/ows4444/tui/spinner"
	"github.com/ows4444/tui/widgets"

	"acline/internal/proc"
)

// The orchestrator runs as its own process, `acline orchestrate step|run`,
// exactly as from a terminal: it acts as an agent identity for its whole
// process, and running it inside the TUI would make the person's own actions
// there an agent's too. Its output streams into a log. The stop marker
// (`acline orchestrate stop`) stops it before its next launch; stopping now
// interrupts the process, which ends the running step as Ctrl-C would.

// interruptGrace is how long a run may take to end after an interrupt before
// it is killed.
const interruptGrace = 10 * time.Second

type orchLineMsg struct{ line string }

type orchDoneMsg struct{ err error }

type orchRunning struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
	lines  <-chan string
	done   <-chan error
	spin   spinner.Model
}

// orchestratorScreen starts the orchestrator, shows what it reports, and
// stops it.
type orchestratorScreen struct {
	log      logview.Model
	running  *orchRunning
	last     string // how the last run ended
	stopSet  bool
	stopPath string
	project  string // the name of the project in scope, "" for all
	prompt   *prompt

	env           env
	width, height int
}

func (orchestratorScreen) title() string { return "Orchestrator" }

func (s orchestratorScreen) load(e env) (screen, error) {
	s.env = e
	s.stopPath = filepath.Join(filepath.Dir(e.st.Path), "orchestrator.stop")
	_, err := os.Stat(s.stopPath)
	s.stopSet = err == nil
	s.project = ""
	if e.projectID != nil {
		projects, err := e.st.ListProjects()
		if err != nil {
			return s, err
		}
		for _, p := range projects {
			if p.ID == *e.projectID {
				s.project = p.Name
			}
		}
	}
	if s.log.Viewport.Width == 0 {
		s.log = logview.New(max(s.width, 20), max(s.height-4, 3))
		s.log.Max = logLines
	}
	return s, nil
}

// errStartOrch carries the chosen arguments out of the start form.
type errStartOrch struct{ args []string }

func (errStartOrch) Error() string { return "start" }

func (s orchestratorScreen) startForm() *prompt {
	return askFields("Start the orchestrator", "", []form.Field{
		{Name: "mode", Label: "Run", Kind: form.FieldSelect, Options: []string{"step", "run"}},
		{Name: "task", Label: "Task id (empty: the next launchable)", Width: 12},
		{Name: "steps", Label: "Max steps (run)", Value: "5", Width: 6, Validators: []form.Validator{positiveInt}},
		{Name: "cost", Label: "Max cost USD (run)", Value: "5.00", Width: 8, Validators: []form.Validator{positiveFloat}},
		{Name: "budget", Label: "Budget per step USD", Value: "1.00", Width: 8, Validators: []form.Validator{positiveFloat}},
		{Name: "timeout", Label: "Timeout per step", Value: "15m", Width: 8, Validators: []form.Validator{duration}},
		{Name: "dry", Label: "Dry run (launch nothing)", Kind: form.FieldCheckbox},
	}, func(v map[string]string, _ string) (string, error) {
		args := []string{"--db", s.env.st.Path, "orchestrate", v["mode"],
			"--step-budget", v["budget"], "--timeout", v["timeout"]}
		if v["mode"] == "run" {
			args = append(args, "--max-steps", v["steps"], "--max-cost", v["cost"])
		}
		if v["dry"] == "true" {
			args = append(args, "--dry-run")
		}
		if task := strings.TrimPrefix(strings.TrimSpace(v["task"]), "#"); task != "" {
			if _, err := strconv.ParseInt(task, 10, 64); err != nil {
				return "", fmt.Errorf("%q is not a task id", task)
			}
			args = append(args, task)
		} else if s.project != "" {
			args = append(args, "--project", s.project)
		}
		return "", errStartOrch{args}
	})
}

func positiveInt(v string) error {
	if n, err := strconv.Atoi(strings.TrimSpace(v)); err != nil || n <= 0 {
		return errors.New("a whole number above 0")
	}
	return nil
}

func positiveFloat(v string) error {
	if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err != nil || f <= 0 {
		return errors.New("a number above 0")
	}
	return nil
}

func duration(v string) error {
	if d, err := time.ParseDuration(strings.TrimSpace(v)); err != nil || d <= 0 {
		return errors.New("a duration such as 15m")
	}
	return nil
}

// start launches `acline <args>` and streams what it prints.
func (s orchestratorScreen) start(args []string) (orchestratorScreen, tk.Cmd) {
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, s.env.self, args...)
	cmd.Dir, _ = os.Getwd()
	proc.Bound(cmd) // the run and what it spawned die together if it must be killed
	lines := make(chan string, liveLines)
	done := make(chan error, 1)
	w := &lineWriter{out: lines}
	cmd.Stdout, cmd.Stderr = w, w
	s.log = logview.New(max(s.width, 20), max(s.height-4, 3))
	s.log.Max = logLines
	s.log.Append(helpStyle.Render("$ acline " + strings.Join(args[2:], " ")))
	if err := cmd.Start(); err != nil {
		cancel()
		return s, errCmd(err)
	}
	finished := make(chan struct{})
	if s.env.cleanup != nil {
		// Quitting the TUI ends the run the way K does, and waits for it.
		s.env.cleanup.add(func() {
			select {
			case <-finished:
				return
			default:
			}
			stopProcess(cmd, cancel)
			select {
			case <-finished:
			case <-time.After(interruptGrace):
				cancel()
				<-finished
			}
		})
	}
	go func() {
		err := cmd.Wait()
		w.flush()
		close(finished)
		close(lines)
		done <- err
	}()
	r := &orchRunning{cmd: cmd, cancel: cancel, lines: lines, done: done, spin: spinner.New()}
	s.running, s.last = r, ""
	return s, tk.Batch(r.spin.Start(), r.next())
}

// stopProcess interrupts a run, and kills it if it has not ended in time.
func stopProcess(cmd *exec.Cmd, cancel context.CancelFunc) {
	if cmd.Process == nil {
		cancel()
		return
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil { // Windows has no interrupt to send
		cancel()
		return
	}
	go func() {
		time.Sleep(interruptGrace)
		cancel()
	}()
}

func (r *orchRunning) next() tk.Cmd {
	lines, done := r.lines, r.done
	return func() tk.Msg {
		if line, ok := <-lines; ok {
			return orchLineMsg{line}
		}
		return orchDoneMsg{<-done}
	}
}

// stopLine colours the lines that say why the orchestrator stopped.
func stopLine(line string) string {
	l := ansi.Sanitize(strings.ReplaceAll(line, "\t", "    "))
	switch {
	case strings.Contains(l, "stopped:") && (strings.Contains(l, "needs_a_person") || strings.Contains(l, "review_point") || strings.Contains(l, "ready_to_complete")):
		return warnStyle.Render(l)
	case strings.Contains(l, "stopped:") && (strings.Contains(l, "policy_violation") || strings.Contains(l, "agent_failed") || strings.Contains(l, "budget_reached")):
		return errStyle.Render(l)
	case strings.Contains(l, "permission denied:"), strings.HasPrefix(l, "error:"):
		return errStyle.Render(l)
	case strings.Contains(l, "stopped:"):
		return sectionStyle.Render(l)
	}
	return l
}

func (s orchestratorScreen) capturing() bool { return s.prompt != nil || s.running != nil }

func (s orchestratorScreen) update(msg tk.Msg) (screen, tk.Cmd) {
	switch msg := msg.(type) {
	case sizeMsg:
		s.width, s.height = msg.width, msg.height
		s.log.Viewport.Width, s.log.Viewport.Height = max(s.width, 20), max(s.height-4, 3)
		return s, nil
	case orchLineMsg:
		s.log.Append(stopLine(msg.line))
		if s.running != nil {
			return s, s.running.next()
		}
		return s, nil
	case orchDoneMsg:
		if s.running != nil {
			s.running.cancel()
		}
		s.running = nil
		s.last = "finished"
		if msg.err != nil {
			s.last = "ended: " + msg.err.Error()
		}
		next, err := s.load(s.env)
		if err != nil {
			return s, errCmd(err)
		}
		return next, infoCmd("orchestrator " + s.last)
	case tk.Key:
		return s.key(msg)
	}
	if s.running != nil {
		var cmd tk.Cmd
		s.running.spin, cmd = s.running.spin.Update(msg)
		return s, cmd
	}
	return s, nil
}

func (s orchestratorScreen) key(k tk.Key) (screen, tk.Cmd) {
	if s.prompt != nil {
		next, done := s.prompt.step(k)
		s.prompt = next
		if done == nil {
			return s, nil
		}
		var start errStartOrch
		if errors.As(done.err, &start) {
			return s.start(start.args)
		}
		if done.err != nil {
			return s, errCmd(done.err)
		}
		next2, err := s.load(s.env)
		if err != nil {
			return s, errCmd(err)
		}
		return next2, infoCmd(done.info)
	}
	switch k.String() {
	case "S":
		return s.setStop(true)
	case "C":
		if s.running == nil {
			return s.setStop(false)
		}
	case "enter":
		if s.running == nil {
			s.prompt = s.startForm()
			return s, nil
		}
	case "K":
		if s.running != nil {
			stopProcess(s.running.cmd, s.running.cancel)
			s.log.Append(warnStyle.Render("interrupted: the running step is being ended"))
			return s, nil
		}
	}
	var cmd tk.Cmd
	s.log, cmd = s.log.Update(k) // scroll the output
	return s, cmd
}

// setStop sets or clears the stop marker, as `acline orchestrate stop [--clear]` does.
func (s orchestratorScreen) setStop(on bool) (screen, tk.Cmd) {
	if on {
		if err := os.WriteFile(s.stopPath, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o600); err != nil {
			return s, errCmd(err)
		}
		s.stopSet = true
		return s, infoCmd("stop set: no further step will launch (a running step finishes; K ends it now)")
	}
	if err := os.Remove(s.stopPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return s, errCmd(err)
	}
	s.stopSet = false
	return s, infoCmd("stop cleared: the orchestrator may launch again")
}

func (s orchestratorScreen) view(width, height int) string {
	var head []string
	stop := helpStyle.Render("stop marker: ") + "clear"
	if s.stopSet {
		stop = helpStyle.Render("stop marker: ") + warnStyle.Render("SET, nothing will launch") + helpStyle.Render("  (C clears it)")
	}
	head = append(head, stop)
	switch {
	case s.running != nil:
		head = append(head, s.running.spin.View()+" "+sectionStyle.Render("running")+helpStyle.Render("   S stops it before the next launch, K ends it now"))
	case s.last != "":
		head = append(head, helpStyle.Render("last run "+s.last+"   enter starts another"))
	default:
		head = append(head, helpStyle.Render("enter starts `acline orchestrate step` or `run` (an agent identity in its own process; launching needs the approval token enabled and the guard hook)"))
	}
	if s.prompt != nil {
		return strings.Join(head, "\n") + "\n\n" + s.prompt.view()
	}
	return strings.Join(head, "\n") + "\n\n" + s.log.View()
}

func (s orchestratorScreen) keys() []widgets.Hint {
	switch {
	case s.prompt != nil:
		return nil
	case s.running != nil:
		return []widgets.Hint{{Key: "S", Action: "stop before next launch"}, {Key: "K", Action: "end it now"}, {Key: "↑/↓", Action: "scroll"}}
	}
	h := []widgets.Hint{{Key: "enter", Action: "start"}, {Key: "S", Action: "set stop"}}
	if s.stopSet {
		h = append(h, widgets.Hint{Key: "C", Action: "clear stop"})
	}
	return append(h, widgets.Hint{Key: "↑/↓", Action: "scroll"})
}

// cleanups are run when the TUI exits: a child process must not outlive it.
type cleanups struct {
	mu  sync.Mutex
	fns []func()
}

func (c *cleanups) add(fn func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fns = append(c.fns, fn)
}

func (c *cleanups) run() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, fn := range c.fns {
		fn()
	}
	c.fns = nil
}
