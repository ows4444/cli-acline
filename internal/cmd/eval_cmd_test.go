package cmd

import "testing"

// `eval promote` is `task promote` under the command it builds on.
func TestEvalPromoteIsTaskPromote(t *testing.T) {
	c := newTestCLI(t)
	for _, args := range [][]string{
		{"task", "add", "--autonomy", "hitl", "a"},
		{"task", "add", "--autonomy", "hitl", "b"},
		{"eval", "record", "--task", "1", "--suite", "s", "--pass-rate", "0.95", "--sample-size", "100"},
		{"eval", "record", "--task", "2", "--suite", "s", "--pass-rate", "0.95", "--sample-size", "100"},
		{"eval", "promote", "1", "--to", "hotl", "--suite", "s"},
		{"task", "promote", "2", "--to", "hotl", "--suite", "s"},
	} {
		if err := c.run(args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	for _, id := range []int64{1, 2} {
		task, err := c.st.GetTask(id)
		if err != nil || task.Autonomy != "hotl" {
			t.Fatalf("task #%d = %+v, %v; want autonomy hotl", id, task, err)
		}
	}
}
