package cmd

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Review-finding IDs (a finding number used as a prefix or in parentheses) in code comments point at a review
// document, and those documents get replaced: the old ones left dozens of
// references to nothing. A comment says why; the history lives in the
// CHANGELOG.
var reviewIDRe = regexp.MustCompile(`^\s*(//|\*)\s*[A-Z]-?\d{1,2}: |\((?:see )?[A-Z]-?\d{1,2}\)`)

func TestCommentsCiteNoReviewIDs(t *testing.T) {
	var bad []string
	for _, root := range []string{"../../internal", "../../vscode-acline/src"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "node_modules" || d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !(strings.HasSuffix(path, ".go") || strings.HasSuffix(path, ".ts")) || strings.Contains(path, "generated") {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for i, line := range strings.Split(string(src), "\n") {
				trimmed := strings.TrimSpace(line)
				if (strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "*")) && reviewIDRe.MatchString(line) {
					bad = append(bad, filepath.ToSlash(path)+":"+strconv.Itoa(i+1)+": "+trimmed)
				}
			}
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	if len(bad) > 0 {
		t.Errorf("comments cite review IDs; say why instead:\n%s", strings.Join(bad, "\n"))
	}
}
