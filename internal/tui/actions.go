package tui

import (
	"errors"
	"fmt"
	"strconv"

	tk "github.com/ows4444/tui"
	"github.com/ows4444/tui/confirm"
	"github.com/ows4444/tui/form"
	"github.com/ows4444/tui/passwordinput"
	"github.com/ows4444/tui/picker"
	"github.com/ows4444/tui/textinput"
	"github.com/ows4444/tui/widgets"

	"acline/internal/app"
	"acline/internal/checkrun"
	"acline/internal/store"
)

// An action on a task runs in up to three steps: a question (confirm, a line
// of text, or a choice), the action itself, and, only when the store answers
// that it needs the approval token, a masked prompt for it, then the action
// again with the token. The token is asked for on the TUI's own screen, is
// never read from the environment, never shown, and is dropped with the prompt
// as soon as the action has run.

type askKind int

const (
	askConfirm askKind = iota
	askText
	askChoice
	askForm
	askToken
)

// do runs an action with the person's answer and a token ("" until the store
// asks for one), and says what it did.
type do func(answer, token string) (string, error)

type prompt struct {
	kind   askKind
	title  string
	action string // what the token is for, in the token prompt
	run    do
	answer string // the answer, kept while the token is asked for

	yesNo   confirm.Model
	text    textinput.Model
	choice  picker.Model
	choices []string
	form    form.Model
	values  map[string]string // the form's answers, kept while the token is asked for
	secret  passwordinput.Model
}

func askYesNo(title, action string, run do) *prompt {
	return &prompt{kind: askConfirm, title: title, action: action, run: run, yesNo: confirm.New(title)}
}

func askLine(title, action string, run do) *prompt {
	in := textinput.New()
	in.Focus()
	return &prompt{kind: askText, title: title, action: action, run: run, text: in}
}

func askOne(title, action string, choices []string, current string, run do) *prompt {
	items := make([]picker.Item, len(choices))
	cursor := 0
	for i, c := range choices {
		items[i] = picker.Item{Label: clean(c), Value: c}
		if c == current {
			cursor = i
		}
	}
	p := &prompt{kind: askChoice, title: title, action: action, run: run, choice: picker.New(items...), choices: choices}
	p.choice.SetCursor(cursor)
	return p
}

// nextStep is how an answer opens a further question: an action that needs
// several answers (which item, which field, what value) returns one.
type nextStep struct{ p *prompt }

func (nextStep) Error() string { return "next step" }

func then(p *prompt) (string, error) { return "", nextStep{p} }

// askFields asks for several answers at once (tab moves between fields,
// enter sends); run gets them by field name.
func askFields(title, action string, fields []form.Field, run func(values map[string]string, token string) (string, error)) *prompt {
	p := &prompt{kind: askForm, title: title, action: action, form: form.New(fields...)}
	p.form.Focus()
	p.run = func(_, token string) (string, error) { return run(p.values, token) }
	return p
}

// actionDoneMsg is an action's outcome: what it did, or why not.
type actionDoneMsg struct {
	info string
	err  error
}

// step feeds a key to the prompt. It returns the prompt still waiting (nil when
// it is finished) and, once the action ran, its outcome.
func (p *prompt) step(k tk.Key) (*prompt, *actionDoneMsg) {
	if k.String() == "esc" {
		return nil, nil
	}
	switch p.kind {
	case askConfirm:
		switch k.String() {
		case "y", "Y":
			return p.attempt("y", "")
		case "n", "N":
			return nil, nil
		}
		var cmd tk.Cmd
		if p.yesNo, cmd = p.yesNo.Update(k); cmd != nil {
			if c, ok := cmd().(confirm.ConfirmedMsg); ok {
				if !c.Yes {
					return nil, nil
				}
				return p.attempt("y", "")
			}
		}
	case askText:
		if k.String() == "enter" {
			return p.attempt(p.text.Value(), "")
		}
		p.text, _ = p.text.Update(k)
	case askChoice:
		var cmd tk.Cmd
		if p.choice, cmd = p.choice.Update(k); cmd != nil {
			if sel, ok := cmd().(picker.SelectedMsg); ok {
				return p.attempt(p.choices[sel.Index], "")
			}
		}
	case askForm:
		var cmd tk.Cmd
		if k.String() == "enter" {
			p.form, cmd = p.form.Submit()
		} else {
			p.form, cmd = p.form.Update(k)
		}
		if cmd != nil {
			if sub, ok := cmd().(form.SubmittedMsg); ok {
				p.values = sub.Values
				return p.attempt("", "")
			}
		}
	case askToken:
		if k.String() == "enter" {
			token := p.secret.Value()
			p.secret = passwordinput.New() // the typed token goes with the old field
			if token == "" {
				return nil, nil
			}
			return p.attempt(p.answer, token)
		}
		p.secret, _ = p.secret.Update(k)
	}
	return p, nil
}

// attempt runs the action; when the store wants the token and none was given,
// it turns the prompt into the token prompt instead.
func (p *prompt) attempt(answer, token string) (*prompt, *actionDoneMsg) {
	info, err := p.run(answer, token)
	var next nextStep
	if errors.As(err, &next) { // the answer leads to another question
		return next.p, nil
	}
	if errors.Is(err, store.ErrApprovalTokenRequired) && token == "" {
		p.kind, p.answer = askToken, answer
		p.secret = passwordinput.New()
		p.secret.Focus()
		return p, nil
	}
	return nil, &actionDoneMsg{info: info, err: err}
}

func (p *prompt) view() string {
	switch p.kind {
	case askConfirm:
		return p.yesNo.View() + helpStyle.Render("   y/n, esc cancels")
	case askText:
		return sectionStyle.Render(p.title) + helpStyle.Render("   enter sends, esc cancels") + "\n" + p.text.View()
	case askChoice:
		return sectionStyle.Render(p.title) + helpStyle.Render("   enter picks, esc cancels") + "\n" + p.choice.View()
	case askForm:
		return sectionStyle.Render(p.title) + helpStyle.Render("   tab moves, enter sends, esc cancels") + "\n" + p.form.View()
	default:
		return sectionStyle.Render("Approval token for "+p.action) + helpStyle.Render("   hidden; enter sends, esc cancels") + "\n" + p.secret.View()
	}
}

// taskAction is a key on the task detail and the prompt it opens.
type taskAction struct {
	key, name string
	open      func(s tasksScreen) (*prompt, error)
}

func (s tasksScreen) taskActions() []taskAction {
	t := s.detail.task
	id := t.ID
	label := "#" + strconv.FormatInt(id, 10)
	e := s.env
	return []taskAction{
		{"a", "approve", func(s tasksScreen) (*prompt, error) {
			return askYesNo("Approve "+label+" for the code as it is now?", "approving task "+label, func(_, token string) (string, error) {
				aid, err := app.Approve(e.st, app.ApproveRequest{TaskID: id, Token: token, AllowCwdFallback: true, Tree: app.TaskTree(e.st, id, e.hash)})
				return fmt.Sprintf("approval #%d recorded for task %s", aid, label), err
			}), nil
		}},
		{"X", "reject", func(s tasksScreen) (*prompt, error) {
			return askLine("Reject "+label+": why?", "rejecting task "+label, func(note, _ string) (string, error) {
				rid, err := app.Reject(e.st, app.RejectRequest{TaskID: id, Note: note, AllowCwdFallback: true})
				return fmt.Sprintf("rejection #%d recorded for task %s", rid, label), err
			}), nil
		}},
		{"d", "done", func(s tasksScreen) (*prompt, error) {
			return askYesNo("Mark "+label+" done? The gate decides.", "completing task "+label, func(_, token string) (string, error) {
				_, err := app.CompleteTask(e.st, app.CompleteTaskRequest{TaskID: id, Token: token, Hash: e.hash})
				return "task " + label + " is done", err
			}), nil
		}},
		{"D", "force done", func(s tasksScreen) (*prompt, error) {
			return askYesNo("Force "+label+" done past its gate? It is recorded as an override.", "forcing task "+label+" past its gate", func(_, token string) (string, error) {
				_, err := app.CompleteTask(e.st, app.CompleteTaskRequest{TaskID: id, Force: true, Token: token, Hash: e.hash})
				return "task " + label + " forced done (recorded)", err
			}), nil
		}},
		{"c", "criterion", func(s tasksScreen) (*prompt, error) {
			if len(s.detail.criteria) == 0 {
				return nil, errors.New("task " + label + " has no acceptance criteria")
			}
			var choices []string
			byLabel := map[string]store.Criterion{}
			for _, c := range s.detail.criteria {
				box := "[ ] "
				if c.Done {
					box = "[x] "
				}
				l := box + "#" + strconv.FormatInt(c.ID, 10) + " " + c.Text
				choices = append(choices, l)
				byLabel[l] = c
			}
			return askOne("Check or uncheck which criterion?", "", choices, "", func(choice, _ string) (string, error) {
				c := byLabel[choice]
				if err := e.st.SetCriterionDone(c.ID, !c.Done); err != nil {
					return "", err
				}
				if c.Done {
					return fmt.Sprintf("criterion #%d unchecked", c.ID), nil
				}
				return fmt.Sprintf("criterion #%d checked", c.ID), nil
			}), nil
		}},
		{"o", "role", func(s tasksScreen) (*prompt, error) {
			var projectID *int64
			if t.ProjectID.Valid {
				projectID = &t.ProjectID.Int64
			}
			roles, err := e.st.ListRoles(projectID)
			if err != nil {
				return nil, err
			}
			var names []string
			for _, r := range roles {
				names = append(names, r.Name)
			}
			return askOne("Hand "+label+" to which role?", "", names, s.detail.role, func(role, _ string) (string, error) {
				r, err := app.AssignRole(e.st, app.AssignRoleRequest{TaskID: id, Role: role})
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("task %s: %s -> %s", label, r.Previous, r.Role.Name), nil
			}), nil
		}},
		{"z", "defer", func(s tasksScreen) (*prompt, error) {
			if t.Deferred {
				return askYesNo("Lift the deferral of "+label+"?", "", func(_, _ string) (string, error) {
					return "task " + label + " is no longer deferred", app.DeferTask(e.st, id, true, "", "")
				}), nil
			}
			return askLine("Defer "+label+": why?", "", func(reason, _ string) (string, error) {
				return "task " + label + " deferred", app.DeferTask(e.st, id, false, reason, "")
			}), nil
		}},
		{"t", "run check", func(s tasksScreen) (*prompt, error) {
			return askOne("Run which check for "+label+"? (the project's runner, in its root)", "", checkrun.Kinds, "test", func(kind, _ string) (string, error) {
				return "", errRunCheck{kind}
			}), nil
		}},
		{"R", "risk", func(s tasksScreen) (*prompt, error) {
			return askOne("Risk of "+label+"?", "changing the risk of task "+label, riskFilters[1:], t.Risk, func(risk, token string) (string, error) {
				_, err := app.UpdateTask(e.st, id, store.TaskUpdate{Risk: risk, Token: token})
				return "task " + label + " risk: " + risk, err
			}), nil
		}},
		{"U", "autonomy", func(s tasksScreen) (*prompt, error) {
			return askOne("Autonomy of "+label+"?", "changing the autonomy of task "+label, []string{"hitl", "hotl", "auto"}, t.Autonomy, func(autonomy, token string) (string, error) {
				_, err := app.UpdateTask(e.st, id, store.TaskUpdate{Autonomy: autonomy, Token: token})
				return "task " + label + " autonomy: " + autonomy, err
			}), nil
		}},
	}
}

// actionKeys are the detail's action keys, for the footer and the help screen.
func (s tasksScreen) actionKeys() []widgets.Hint {
	var out []widgets.Hint
	for _, a := range s.taskActions() {
		out = append(out, widgets.Hint{Key: a.key, Action: a.name})
	}
	return out
}
