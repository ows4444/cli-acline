package cmd

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// commandRefRe matches a command the way the scaffolded prose writes it:
// inside backticks, `acline <word> [<word> [<word>]]`.
var commandRefRe = regexp.MustCompile("`acline ((?:[a-z][a-z-]*)(?: [a-z][a-z-]*){0,2})")

// resolveCommand walks the real command tree as deep as the words go. It is
// valid when the first word is a real command and any word that did not resolve
// could plausibly be an argument, i.e. it is not a typo'd subcommand of a group
// that takes none.
func resolveCommand(words []string) bool {
	cur := rootCmd
	i := 0
	for ; i < len(words); i++ {
		var next *cobra.Command
		for _, c := range cur.Commands() {
			if c.Name() == words[i] {
				next = c
				break
			}
			for _, a := range c.Aliases {
				if a == words[i] {
					next = c
					break
				}
			}
		}
		if next == nil {
			break
		}
		cur = next
	}
	return i > 0 && (i == len(words) || cur.Runnable() || !cur.HasSubCommands())
}

func checkCommandRefs(t *testing.T, where, text string) int {
	t.Helper()
	n := 0
	for _, m := range commandRefRe.FindAllStringSubmatch(text, -1) {
		n++
		if !resolveCommand(strings.Fields(m[1])) {
			t.Errorf("%s names `acline %s`, which is not a real command", where, m[1])
		}
	}
	return n
}

// The scaffolded guidance is what every agent in every project is told to run.
// A renamed or removed command must fail here, not in someone's session.
func TestScaffoldedGuidanceOnlyNamesRealCommands(t *testing.T) {
	assets := filepath.Join("..", "scaffold", "assets")
	total := 0
	err := filepath.WalkDir(assets, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return err
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		rel, _ := filepath.Rel(assets, path)
		total += checkCommandRefs(t, rel, string(b))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if total < 30 {
		t.Errorf("only %d command references found in the assets: the extraction is probably broken", total)
	}
}

func TestGeneratedClaudeMDOnlyNamesRealCommands(t *testing.T) {
	if n := checkCommandRefs(t, "the generated CLAUDE.md", renderClaudeMD(initAnswers{Scaffold: true})); n < 10 {
		t.Errorf("only %d command references found in the generated CLAUDE.md", n)
	}
}

func TestResolveCommandRejectsTypos(t *testing.T) {
	for _, ok := range [][]string{{"note", "add"}, {"check", "run"}, {"plan", "propose"}, {"task", "assign", "developer"}, {"dashboard"}, {"reflect", "promote"}} {
		if !resolveCommand(ok) {
			t.Errorf("%v should resolve", ok)
		}
	}
	for _, bad := range [][]string{{"nope"}, {"task", "assing"}, {"plan", "aprove"}, {"check", "runn"}} {
		if resolveCommand(bad) {
			t.Errorf("%v should not resolve", bad)
		}
	}
}
