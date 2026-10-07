package cmd

import (
	"slices"
	"testing"

	"acline/internal/checkrun"
)

// `check runner set test go test -run "TestA TestB"` joined its arguments
// with spaces, so the quoted one came back as two.
func TestJoinCommandRoundTripsThroughSplitCommand(t *testing.T) {
	for _, argv := range [][]string{
		{"go", "test", "./..."},
		{"go", "test", "-run", "TestA TestB", "./..."},
		{"sh", "-c", `echo "it's"; false`},
		{"echo", "", `back\slash`},
	} {
		got, err := checkrun.SplitCommand(joinCommand(argv))
		if err != nil || !slices.Equal(got, argv) {
			t.Errorf("round trip of %q via %q = %q, %v", argv, joinCommand(argv), got, err)
		}
	}
}

// `check runner set test "go test ./..."` arrived as one argument, was quoted as
// one word, and every later `check run` recorded "skipped: go test ./... is not
// installed".
func TestRunnerCommandFromOneQuotedArgument(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"go test ./..."}, []string{"go", "test", "./..."}},
		{[]string{"  npm   test  "}, []string{"npm", "test"}},
		{[]string{`go test -run "TestA TestB" ./...`}, []string{"go", "test", "-run", "TestA TestB", "./..."}},
		{[]string{"go", "test", "./..."}, []string{"go", "test", "./..."}},
		{[]string{"go", "test", "-run", "TestA TestB"}, []string{"go", "test", "-run", "TestA TestB"}},
	} {
		cmd, err := runnerCommand(tc.args)
		if err != nil {
			t.Fatalf("runnerCommand(%q) = %v", tc.args, err)
		}
		got, err := checkrun.SplitCommand(cmd)
		if err != nil || !slices.Equal(got, tc.want) {
			t.Errorf("runnerCommand(%q) = %q, splits to %q (%v), want %q", tc.args, cmd, got, err, tc.want)
		}
	}
	if _, err := runnerCommand([]string{`go test "unterminated`}); err == nil {
		t.Error("an unparsable single-argument command was accepted")
	}
}
