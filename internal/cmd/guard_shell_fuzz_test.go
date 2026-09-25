package cmd

import (
	"strings"
	"testing"
)

// fuzzDangerous are commands checkBashCommand must deny on their own.
var fuzzDangerous = []string{
	"rm -rf /",
	"git push --force origin main",
	"cat .env",
	"printenv",
	"echo x >> SOUL.md",
	"sqlite3 ~/.acline/store.db",
	"export ACLINE_ACTOR_TYPE=human",
}

// fuzzFiller keeps only characters that cannot legitimately turn the
// dangerous command into data: no quotes, backslashes, comments, newlines,
// heredoc/redirect or substitution syntax. With those gone, bash would run
// the dangerous command, so the guard must still see it.
func fuzzFiller(s string) string {
	var b strings.Builder
	for _, r := range s {
		if b.Len() >= 64 {
			break
		}
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune(" _./=-+,:@%", r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// FuzzBashScanTargets: the shell-aware view must never clear a dangerous
// command that bash would run, whatever plain text surrounds it.
func FuzzBashScanTargets(f *testing.F) {
	for i := range fuzzDangerous {
		f.Add(uint8(i), uint8(0), "")
		f.Add(uint8(i), uint8(3), "git status")
		f.Add(uint8(i), uint8(5), "echo done")
	}
	f.Fuzz(func(t *testing.T, which, shape uint8, filler string) {
		danger := fuzzDangerous[int(which)%len(fuzzDangerous)]
		if denied, _ := checkBashCommand(danger); !denied {
			t.Fatalf("premise: %q must be denied on its own", danger)
		}
		b := fuzzFiller(filler)
		if strings.TrimSpace(b) == "" {
			b = "true"
		}
		shapes := []string{
			b + "; " + danger,
			danger + "; " + b,
			b + " && " + danger,
			b + " || " + danger,
			b + " | " + danger,
			danger + " | " + b,
			"echo $(" + danger + ")",
			"echo `" + danger + "`",
			"(" + danger + ")",
			"{ " + danger + "; }",
			b + " & " + danger,
		}
		cmd := shapes[int(shape)%len(shapes)]
		if denied, _ := checkBashCommand(cmd); !denied {
			t.Errorf("guard cleared a command bash would run: %q", cmd)
		}
	})
}

// FuzzBashScanTargetsNeverPanics: any input, however malformed, is lexed
// without panicking or looping (the fuzzer reports hangs as failures).
func FuzzBashScanTargetsNeverPanics(f *testing.F) {
	for _, seed := range []string{
		"", "'", "\"", "`", "$(", "$((", "<<", "<<EOF\n", "<<-'X'\nbody\nX", "a\\", "echo \"$(echo `x`)\"",
		"cat <<A <<B\na\nA\nb\nB", "((((((((", "$($($($($($(rm -rf /))))))",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, cmd string) {
		_ = bashScanTargets(cmd, 0)
		_, _ = checkBashCommand(cmd)
	})
}
