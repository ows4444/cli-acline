package tui

import "testing"

func TestUnifiedDiffMarksAddedAndRemovedLines(t *testing.T) {
	got, ok := unifiedDiff("charge cards\nrefund in 30 days\nemail receipt", "charge cards\nrefund in 14 days\nemail receipt\nsms receipt")
	want := "@@ -1,3 +1,4 @@\n charge cards\n-refund in 30 days\n+refund in 14 days\n email receipt\n+sms receipt"
	if !ok || got != want {
		t.Fatalf("diff =\n%s\nwant\n%s", got, want)
	}
}
