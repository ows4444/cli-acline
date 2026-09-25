// Package brief assembles everything an agent needs to carry out a task's next
// step into one document: the routed step, the task's intent and acceptance
// criteria, the state of its checks, the lessons learned so far, the role's
// behavior contract, and the standing rules.
//
// It only reads. Nothing is stored, launched or changed, so a brief is safe to
// produce for any task, and a person can paste it into any agent session.
package brief

import (
	"fmt"
	"strings"

	"acline/internal/clip"
	"acline/internal/scaffold"
	"acline/internal/store"
	"acline/internal/untrusted"
)

// maxSpecBody bounds how much of a spec is inlined; the brief names the spec id
// so the full text stays one lookup away.
const maxSpecBody = 4000

// Brief is the assembled context for one task's next step.
type Brief struct {
	Route    *store.Route
	Task     *store.Task
	Spec     *store.Spec
	Decision *store.Decision
	Criteria []store.Criterion
	Contract string // the suggested role's behavior contract, when one is bundled
}

// Build gathers the brief for taskID.
func Build(st *store.Store, taskID int64) (*Brief, error) {
	route, err := st.RouteTask(taskID)
	if err != nil {
		return nil, err
	}
	task, err := st.GetTask(taskID)
	if err != nil {
		return nil, err
	}
	b := &Brief{Route: route, Task: task}
	if task.SpecID.Valid {
		if b.Spec, err = st.GetSpec(task.SpecID.Int64); err != nil {
			return nil, err
		}
	}
	if task.DecisionID.Valid {
		if b.Decision, err = st.GetDecision(task.DecisionID.Int64); err != nil {
			return nil, err
		}
	}
	if b.Criteria, err = st.ListCriteria(taskID); err != nil {
		return nil, err
	}
	if route.Role != nil {
		b.Contract, _ = scaffold.RoleContract(route.Role.Name)
	}
	return b, nil
}

// suggestedTools is advisory guidance on what a step should need. The real
// limits are the session policy and the guard hooks, not this text.
var suggestedTools = map[string]string{
	store.RouteDefineCriteria: "read files and edit specs/docs only; do not change source code",
	store.RouteStartWork:      "read, edit and run the project's build/test tools",
	store.RouteFixChecks:      "read, edit and run the project's build/test tools",
	store.RouteVerify:         "read and run tools; do not change source code (run checks, don't edit to make them pass)",
}

// Markdown renders the brief as a single document.
func (b *Brief) Markdown() string {
	var w strings.Builder
	r, t := b.Route, b.Task

	fmt.Fprintf(&w, "# Task #%d: %s\n\n", t.ID, t.Title)

	w.WriteString("## Your step\n\n")
	fmt.Fprintf(&w, "- **Action:** %s\n- **Why:** %s\n", r.Action, r.Reason)
	switch {
	case r.Action == store.RouteNone:
		w.WriteString("- **Who:** nobody — nothing to do.\n")
	case r.NeedsHuman:
		fmt.Fprintf(&w, "- **Who:** a person%s. This step is not an agent's to take; do not attempt it.\n", roleSuffix(r.Role))
	case r.Role != nil:
		fmt.Fprintf(&w, "- **Who:** role `%s`\n", r.Role.Name)
	default:
		w.WriteString("- **Who:** any agent\n")
	}
	if tools, ok := suggestedTools[r.Action]; ok {
		fmt.Fprintf(&w, "- **Suggested scope:** %s\n", tools)
	}

	w.WriteString("\n## Task\n\n")
	fmt.Fprintf(&w, "- status `%s`, priority `%s`, risk `%s`, autonomy `%s`", t.Status, t.Priority, t.Risk, t.Autonomy)
	if t.Area.Valid {
		fmt.Fprintf(&w, ", area `%s`", t.Area.String)
	}
	w.WriteString("\n")
	if t.BlockedReason.Valid {
		fmt.Fprintf(&w, "\n%s\n", untrusted.Quote("Why it is blocked", t.BlockedReason.String))
	}
	if t.Description != "" {
		fmt.Fprintf(&w, "\n%s\n", untrusted.Quote("Task description", t.Description))
	}

	if b.Spec != nil {
		fmt.Fprintf(&w, "\n## Spec #%d: %s (%s, v%d)\n\n", b.Spec.ID, b.Spec.Title, b.Spec.Status, b.Spec.Version)
		w.WriteString(untrusted.Quote("Spec text", truncate(b.Spec.Body.String, maxSpecBody, fmt.Sprintf("acline spec show %d", b.Spec.ID))))
		w.WriteString("\n")
	}
	if d := b.Decision; d != nil {
		fmt.Fprintf(&w, "\n## Decision #%d: %s (%s)\n\n", d.ID, d.Title, d.Status)
		var fields strings.Builder
		for _, f := range []struct{ label, text string }{
			{"Context", d.Context.String}, {"Decision", d.DecisionText.String}, {"Rationale", d.Rationale.String},
		} {
			if f.text != "" {
				fmt.Fprintf(&fields, "%s: %s\n", f.label, f.text)
			}
		}
		w.WriteString(untrusted.Quote("Decision text", fields.String()) + "\n")
	}

	if len(b.Criteria) > 0 {
		w.WriteString("\n## Acceptance criteria\n\n")
		var criteria strings.Builder
		for _, c := range b.Criteria {
			box := " "
			if c.Done {
				box = "x"
			}
			fmt.Fprintf(&criteria, "- [%s] %s\n", box, c.Text)
		}
		w.WriteString(untrusted.Quote("Acceptance criteria", criteria.String()) + "\n")
	}

	if len(r.WaitingOn) > 0 {
		w.WriteString("\n## Waiting on\n\nThese prerequisite tasks are not done, so this step should not start yet:\n\n")
		w.WriteString(untrusted.Quote("Prerequisite tasks", "- "+strings.Join(r.WaitingOn, "\n- ")) + "\n")
	}

	if len(r.FailingChecks) > 0 || len(r.Blockers) > 0 {
		w.WriteString("\n## Current state\n\n")
		var state strings.Builder
		for _, c := range r.FailingChecks {
			fmt.Fprintf(&state, "- failing check — %s\n", c)
		}
		for _, bl := range r.Blockers {
			fmt.Fprintf(&state, "- gate blocker — %s\n", bl)
		}
		w.WriteString(untrusted.Quote("Check output and gate state", state.String()) + "\n")
	}

	if len(r.Lessons) > 0 {
		w.WriteString("\n## Lessons from earlier work (approved)\n\n")
		var lessons strings.Builder
		for _, m := range r.Lessons {
			fmt.Fprintf(&lessons, "- [%s] %s\n", m.Kind, m.Body)
		}
		w.WriteString(untrusted.Quote("Lessons", lessons.String()) + "\n")
	}

	if b.Contract != "" {
		fmt.Fprintf(&w, "\n## Role contract: %s\n\n%s\n", r.Role.Name, demoteHeadings(b.Contract, 2))
	}

	w.WriteString("\n## Standing rules\n\n" +
		"- " + untrusted.Rule + "\n" +
		"- Work only on the step above. If it needs a person, stop and say so.\n" +
		"- Never approve your own work, review memory, or mark a task done past a failing gate.\n" +
		"- Verify with real tools (`acline check run <id> --kind test|lint|sast|sca`); a claim without a recorded check does not count.\n" +
		"- Never make a check pass by editing or skipping it; fix the cause.\n" +
		"- When finished, summarize what changed and what is left.\n")
	return w.String()
}

// demoteHeadings pushes every Markdown heading in s down by n levels so an
// embedded document nests under the brief's own sections instead of competing
// with them. Fenced code is left alone.
func demoteHeadings(s string, n int) string {
	lines := strings.Split(s, "\n")
	inFence := false
	for i, l := range lines {
		if strings.HasPrefix(l, "```") {
			inFence = !inFence
		}
		if !inFence && strings.HasPrefix(l, "#") {
			lines[i] = strings.Repeat("#", n) + l
		}
	}
	return strings.Join(lines, "\n")
}

func roleSuffix(r *store.Role) string {
	if r == nil {
		return ""
	}
	return " (" + r.Name + ")"
}

func truncate(s string, n int, more string) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return clip.Bytes(s, n) + fmt.Sprintf("\n\n… (truncated; full text: `%s`)", more)
}
