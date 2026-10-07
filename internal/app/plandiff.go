package app

import (
	"slices"

	"acline/internal/store"
)

// PlanChange is one item of a plan compared with the spec's previous plan,
// matched by Ref: Change is added, removed, changed, dropped (kept in the plan
// but marked not to become a task) or unchanged. Item is the item as it is in
// this plan (in the previous one, for removed); Fields names what changed.
type PlanChange struct {
	Ref    string
	Change string
	Item   store.PlanItem
	Fields []string
}

// PlanDiff compares plan planID's items with the newest earlier plan of the
// same spec, so a person reviewing a revised plan sees what moved. previous
// is nil when this is the spec's first plan (every item is then added).
func PlanDiff(st *store.Store, planID int64) (previous *store.Plan, changes []PlanChange, err error) {
	plan, err := st.GetPlan(planID)
	if err != nil {
		return nil, nil, err
	}
	items, err := st.PlanItems(planID)
	if err != nil {
		return nil, nil, err
	}
	plans, err := st.ListPlans(&plan.SpecID, "")
	if err != nil {
		return nil, nil, err
	}
	for i := range plans {
		p := plans[i]
		if p.ID != plan.ID && p.Version < plan.Version && (previous == nil || p.Version > previous.Version) {
			previous = &p
		}
	}
	old := map[string]store.PlanItem{}
	if previous != nil {
		prevItems, err := st.PlanItems(previous.ID)
		if err != nil {
			return nil, nil, err
		}
		for _, it := range prevItems {
			old[it.Ref] = it
		}
	}
	seen := map[string]bool{}
	for _, it := range items {
		seen[it.Ref] = true
		c := PlanChange{Ref: it.Ref, Item: it}
		was, existed := old[it.Ref]
		switch {
		case it.Dropped && !(existed && was.Dropped):
			c.Change = "dropped"
		case !existed:
			c.Change = "added"
		default:
			c.Fields = changedFields(was, it)
			c.Change = "unchanged"
			if len(c.Fields) > 0 {
				c.Change = "changed"
			}
		}
		changes = append(changes, c)
	}
	if previous != nil {
		prevItems, _ := st.PlanItems(previous.ID)
		for _, it := range prevItems {
			if !seen[it.Ref] {
				changes = append(changes, PlanChange{Ref: it.Ref, Change: "removed", Item: it})
			}
		}
	}
	return previous, changes, nil
}

func changedFields(a, b store.PlanItem) []string {
	var out []string
	add := func(name string, differ bool) {
		if differ {
			out = append(out, name)
		}
	}
	add("title", a.Title != b.Title)
	add("description", a.Description != b.Description)
	add("area", a.Area != b.Area)
	add("type", a.Type != b.Type)
	add("risk", a.Risk != b.Risk)
	add("autonomy", a.Autonomy != b.Autonomy)
	add("parent", a.Parent != b.Parent)
	add("milestone", a.Milestone != b.Milestone)
	add("size", a.Size != b.Size)
	add("depends on", !slices.Equal(a.DependsOn, b.DependsOn))
	return out
}
