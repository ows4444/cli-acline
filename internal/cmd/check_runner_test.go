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
