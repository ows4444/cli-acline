package cmd

import "testing"

func TestNeedsStore(t *testing.T) {
	// cobra adds these built-ins lazily, at Execute time
	rootCmd.InitDefaultCompletionCmd()
	rootCmd.InitDefaultHelpCmd()
	find := func(path ...string) bool {
		c, _, err := rootCmd.Find(path)
		if err != nil {
			t.Fatalf("Find(%v): %v", path, err)
		}
		return needsStore(c)
	}
	for _, p := range [][]string{{"version"}, {"init"}, {"guard", "doctor"}, {"completion", "zsh"}, {"help"}} {
		if find(p...) {
			t.Errorf("%v must not open the store", p)
		}
	}
	for _, p := range [][]string{{"task", "list"}, {"guard", "check-tool"}, {"mcp", "serve"}, {"dashboard"}, {"note", "add"}, {"snapshot", "export"}} {
		if !find(p...) {
			t.Errorf("%v needs the store", p)
		}
	}
}
