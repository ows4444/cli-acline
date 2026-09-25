package cmd

import (
	"runtime/debug"
	"testing"
)

func TestVcsCommitAndDate(t *testing.T) {
	settings := func(modified string) []debug.BuildSetting {
		return []debug.BuildSetting{
			{Key: "vcs.revision", Value: "5aa2619deadbeef"},
			{Key: "vcs.time", Value: "2026-09-22T20:17:49Z"},
			{Key: "vcs.modified", Value: modified},
		}
	}
	cases := []struct {
		name, modified, commit, date string
		wantCommit, wantDate         string
	}{
		{"clean tree", "false", "", "", "5aa2619", "2026-09-22T20:17:49Z"},
		{"dirty tree", "true", "", "", "5aa2619-dirty", "2026-09-22T20:17:49Z"},
		{"ldflags commit wins, no suffix", "true", "v1abcde", "", "v1abcde", "2026-09-22T20:17:49Z"},
		{"ldflags date wins", "true", "", "2026-01-01", "5aa2619-dirty", "2026-01-01"},
	}
	for _, c := range cases {
		gotC, gotD := vcsCommitAndDate(settings(c.modified), c.commit, c.date)
		if gotC != c.wantCommit || gotD != c.wantDate {
			t.Errorf("%s: got (%q, %q), want (%q, %q)", c.name, gotC, gotD, c.wantCommit, c.wantDate)
		}
	}
}
