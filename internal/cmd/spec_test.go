package cmd

import (
	"os"
	"strings"
	"testing"
)

// withStdin temporarily replaces os.Stdin with r's content, for --body-file -.
func withStdin(t *testing.T, content string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(content); err != nil {
		t.Fatal(err)
	}
	w.Close()
	prev := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = prev })
}

func TestSpecAddAndReviseReadBodyFromStdin(t *testing.T) {
	withTestStore(t)

	withStdin(t, "line one\nline two\n")
	specBodyFile = "-"
	t.Cleanup(func() { specBodyFile = "" })
	if err := specAddCmd.RunE(specAddCmd, []string{"Stdin", "spec"}); err != nil {
		t.Fatal(err)
	}
	specs, _ := st.ListSpecs("", nil)
	if len(specs) != 1 || specs[0].Body.String != "line one\nline two\n" {
		t.Fatalf("spec = %+v", specs)
	}

	withStdin(t, "revised body\n")
	specBodyFile = "-"
	if err := specReviseCmd.RunE(specReviseCmd, []string{"1"}); err != nil {
		t.Fatal(err)
	}
	sp, _ := st.GetSpec(1)
	if sp.Body.String != "revised body\n" || sp.Version != 2 {
		t.Fatalf("revised spec = %+v", sp)
	}
}

func TestSpecAddPlainBodyFileStillWorks(t *testing.T) {
	withTestStore(t)
	f := t.TempDir() + "/body.txt"
	os.WriteFile(f, []byte("from a file\n"), 0o644)
	specBodyFile = f
	t.Cleanup(func() { specBodyFile = "" })
	if err := specAddCmd.RunE(specAddCmd, []string{"File", "spec"}); err != nil {
		t.Fatal(err)
	}
	specs, _ := st.ListSpecs("", nil)
	if !strings.Contains(specs[0].Body.String, "from a file") {
		t.Fatalf("spec = %+v", specs)
	}
}
