package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	tk "github.com/ows4444/tui"
	"github.com/ows4444/tui/ansi"
	"github.com/ows4444/tui/logview"
	"github.com/ows4444/tui/spinner"

	"acline/internal/app"
	"acline/internal/checkrun"
)

// A check run from the detail runs the project's runner for one kind, as
// `acline check run` does, and streams the tool's output into a log while it
// runs. esc cancels it: the tool is stopped and nothing is recorded.

// liveLines is how many output lines may wait for the screen; past it, lines
// are counted, not shown (the recorded detail keeps the tail either way), so a
// noisy tool never waits on the screen.
const liveLines = 1024

// logLines is how many lines the log keeps.
const logLines = 5000

type checkLineMsg struct{ line string }

type checkDoneMsg struct {
	id      int64
	kind    string
	result  checkrun.Result
	err     error
	dropped int
}

// errRunCheck is how the kind picker hands its answer back: the run is not a
// quick action, so it starts after the prompt closes.
type errRunCheck struct{ kind string }

func (e errRunCheck) Error() string { return "run " + e.kind }

type checkRunning struct {
	kind   string
	cancel context.CancelFunc
	lines  <-chan string
	done   <-chan checkDoneMsg
	log    logview.Model
	spin   spinner.Model
}

// lineWriter turns the tool's output into lines for the screen, dropping (and
// counting) what does not fit rather than blocking the tool.
type lineWriter struct {
	mu      sync.Mutex
	partial []byte
	out     chan<- string
	dropped int
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.partial = append(w.partial, p...)
	for {
		i := strings.IndexByte(string(w.partial), '\n')
		if i < 0 {
			break
		}
		w.send(string(w.partial[:i]))
		w.partial = w.partial[i+1:]
	}
	return len(p), nil
}

func (w *lineWriter) send(line string) {
	select {
	case w.out <- line:
	default:
		w.dropped++
	}
}

func (w *lineWriter) flush() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.partial) > 0 {
		w.send(string(w.partial))
		w.partial = nil
	}
	return w.dropped
}

func (s tasksScreen) startCheck(kind string) (tasksScreen, tk.Cmd) {
	ctx, cancel := context.WithCancel(context.Background())
	lines := make(chan string, liveLines)
	done := make(chan checkDoneMsg, 1)
	id, e := s.detail.task.ID, s.env
	go func() {
		w := &lineWriter{out: lines}
		cid, res, err := app.RunCheck(ctx, e.st, app.RunCheckRequest{TaskID: id, Kind: kind, AllowCwdFallback: true, Hash: e.hash, Output: w})
		dropped := w.flush()
		close(lines)
		done <- checkDoneMsg{id: cid, kind: kind, result: res, err: err, dropped: dropped}
	}()
	run := &checkRunning{kind: kind, cancel: cancel, lines: lines, done: done, log: logview.New(max(s.width, 20), max(s.height-2, 3)), spin: spinner.New()}
	run.log.Max = logLines
	s.running = run
	return s, tk.Batch(run.spin.Start(), run.next())
}

// next waits for the run's next line, or its end once the output is drained.
func (r *checkRunning) next() tk.Cmd {
	lines, done := r.lines, r.done
	return func() tk.Msg {
		if line, ok := <-lines; ok {
			return checkLineMsg{line}
		}
		return <-done
	}
}

// runUpdate handles messages while a check runs.
func (s tasksScreen) runUpdate(msg tk.Msg) (screen, tk.Cmd) {
	r := s.running
	switch msg := msg.(type) {
	case checkLineMsg:
		r.log.Append(ansi.Sanitize(strings.ReplaceAll(msg.line, "\t", "    ")))
		return s, r.next()
	case checkDoneMsg:
		r.cancel()
		s.running = nil
		next, err := s.load(s.env)
		if err != nil {
			return s, errCmd(err)
		}
		switch {
		case errors.Is(msg.err, checkrun.ErrCanceled):
			return next, infoCmd(msg.kind + " check cancelled; nothing was recorded")
		case msg.err != nil:
			return next, errCmd(msg.err)
		}
		info := fmt.Sprintf("%s check #%d: %s", msg.kind, msg.id, msg.result.Status)
		if msg.dropped > 0 {
			info += fmt.Sprintf(" (%d output lines were not shown; the recorded detail keeps the end)", msg.dropped)
		}
		return next, infoCmd(info)
	case tk.Key:
		switch msg.String() {
		case "esc":
			r.cancel()
		default:
			r.log, _ = r.log.Update(msg) // scroll the log
		}
		return s, nil
	case sizeMsg:
		s.width, s.height = msg.width, msg.height
		r.log.Viewport.Width, r.log.Viewport.Height = max(s.width, 20), max(s.height-2, 3)
		return s, nil
	}
	var cmd tk.Cmd
	r.spin, cmd = r.spin.Update(msg)
	return s, cmd
}

func (r *checkRunning) view() string {
	return r.spin.View() + " " + sectionStyle.Render("Running the "+r.kind+" check") + helpStyle.Render("   esc stops it (nothing is recorded)") + "\n\n" + r.log.View()
}
