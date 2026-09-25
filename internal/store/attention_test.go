package store

import "testing"

func TestNeedsAttention(t *testing.T) {
	cases := []struct {
		name string
		task Task
		want bool
	}{
		{"plain todo", Task{Status: "todo", Priority: "normal", Risk: "low"}, false},
		{"blocked", Task{Status: "blocked", Priority: "normal", Risk: "low"}, true},
		{"urgent", Task{Status: "todo", Priority: "urgent", Risk: "low"}, true},
		{"high priority", Task{Status: "in_progress", Priority: "high", Risk: "low"}, true},
		{"high risk", Task{Status: "todo", Priority: "normal", Risk: "high"}, true},
		{"critical risk", Task{Status: "review", Priority: "normal", Risk: "critical"}, true},
		{"done never", Task{Status: "done", Priority: "urgent", Risk: "critical"}, false},
		{"cancelled never", Task{Status: "cancelled", Priority: "urgent", Risk: "high"}, false},
	}
	for _, c := range cases {
		if got := c.task.NeedsAttention(); got != c.want {
			t.Errorf("%s: NeedsAttention() = %v, want %v", c.name, got, c.want)
		}
	}
}
