package store

import (
	"errors"
	"fmt"
)

// ErrLinkCycle is returned by AddLink when a dependency would make tasks wait
// on each other forever.
var ErrLinkCycle = errors.New("link would create a dependency cycle")

// precedes lists, for every task, the tasks that must be done before it: a
// "depends_on" link (A depends_on B) makes B precede A, and a "blocks" link
// (A blocks B) makes A precede B. "related" links order nothing.
func (s *Store) precedenceEdges() (map[int64][]int64, error) {
	rows, err := s.DB.Query(`SELECT task_id, related_task_id, relation FROM task_links WHERE relation IN ('depends_on', 'blocks')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	successors := map[int64][]int64{} // u -> tasks that wait on u
	for rows.Next() {
		var a, b int64
		var rel string
		if err := rows.Scan(&a, &b, &rel); err != nil {
			return nil, err
		}
		if rel == "depends_on" {
			successors[b] = append(successors[b], a)
		} else {
			successors[a] = append(successors[a], b)
		}
	}
	return successors, rows.Err()
}

// wouldCycle reports whether making `first` precede `then` closes a loop, i.e.
// `first` is already reachable from `then` through existing precedence.
func (s *Store) wouldCycle(first, then int64) (bool, error) {
	successors, err := s.precedenceEdges()
	if err != nil {
		return false, err
	}
	seen := map[int64]bool{then: true}
	queue := []int64{then}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		if u == first {
			return true, nil
		}
		for _, v := range successors[u] {
			if !seen[v] {
				seen[v] = true
				queue = append(queue, v)
			}
		}
	}
	return false, nil
}

// validateLink rejects links that cannot make sense: a task linked to itself,
// or an ordering that would deadlock.
func (s *Store) validateLink(taskID, relatedID int64, relation string) error {
	if taskID == relatedID {
		return fmt.Errorf("a task cannot be linked to itself (#%d)", taskID)
	}
	var first, then int64
	switch relation {
	case "depends_on":
		first, then = relatedID, taskID
	case "blocks":
		first, then = taskID, relatedID
	default:
		return nil
	}
	cyc, err := s.wouldCycle(first, then)
	if err != nil {
		return err
	}
	if cyc {
		return fmt.Errorf("%w: #%d already (transitively) has to wait for #%d", ErrLinkCycle, first, then)
	}
	return nil
}

// Prerequisites returns the tasks that must be done before taskID, whatever
// their state.
func (s *Store) Prerequisites(taskID int64) ([]Task, error) {
	rows, err := s.DB.Query(`
		SELECT related_task_id FROM task_links WHERE task_id = ? AND relation = 'depends_on'
		UNION
		SELECT task_id FROM task_links WHERE related_task_id = ? AND relation = 'blocks'
		ORDER BY 1`, taskID, taskID)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []Task
	for _, id := range ids {
		t, err := s.GetTask(id)
		if errors.Is(err, ErrNotFound) {
			continue // a dangling link waits on nothing
		}
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, nil
}

// OpenPrerequisites are the prerequisites that are not yet done. A cancelled
// one still counts: it will never finish, so a person has to relink or drop it,
// and silently treating it as satisfied would hide that.
func (s *Store) OpenPrerequisites(taskID int64) ([]Task, error) {
	all, err := s.Prerequisites(taskID)
	if err != nil {
		return nil, err
	}
	var open []Task
	for _, t := range all {
		if t.Status != "done" {
			open = append(open, t)
		}
	}
	return open, nil
}

// prerequisiteLabel is "#3 title [status]" for messages.
func prerequisiteLabel(t Task) string {
	return fmt.Sprintf("#%d %s [%s]", t.ID, t.Title, t.Status)
}
