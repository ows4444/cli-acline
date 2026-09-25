package cmd

import (
	"fmt"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// version/commit/date are set via -ldflags at release build time, e.g.:
//
//	go build -ldflags "-X acline/internal/cmd.version=v0.2.0 -X acline/internal/cmd.commit=$(git rev-parse --short HEAD) -X acline/internal/cmd.date=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
//
// A plain `go build`/`go install` leaves these as "dev" and falls back to
// the VCS info Go embeds automatically (module-aware builds only).
var (
	version = "dev"
	commit  = ""
	date    = ""
)

func buildVersionString() string {
	v := version
	c := commit
	d := date
	if v == "dev" {
		if info, ok := debug.ReadBuildInfo(); ok {
			c, d = vcsCommitAndDate(info.Settings, c, d)
		}
	}
	out := "acline " + v
	if c != "" {
		out += " (" + c
		if d != "" {
			out += ", " + d
		}
		out += ")"
	}
	return out
}

// vcsCommitAndDate fills whichever of commit/date -ldflags left empty from
// Go's embedded VCS settings. A commit taken from a working tree with
// uncommitted changes gets a "-dirty" suffix, so a locally rebuilt binary
// can't pass for the clean commit it started from.
func vcsCommitAndDate(settings []debug.BuildSetting, c, d string) (string, string) {
	fromVCS := false
	modified := false
	for _, s := range settings {
		switch s.Key {
		case "vcs.revision":
			if c == "" && len(s.Value) >= 7 {
				c = s.Value[:7]
				fromVCS = true
			}
		case "vcs.time":
			if d == "" {
				d = s.Value
			}
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if fromVCS && modified {
		c += "-dirty"
	}
	return c, d
}

// shortVersionString omits the "acline " prefix, since cobra's built-in
// --version flag already prints "<name> version <this>".
func shortVersionString() string {
	full := buildVersionString()
	return full[len("acline "):]
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the acline version",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println(buildVersionString())
		return nil
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
	rootCmd.Version = shortVersionString()
}
