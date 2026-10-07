package tui

import (
	"fmt"
	"strings"
)

// maxDiffLines bounds the line diff (it is quadratic); past it the versions
// are shown without one.
const maxDiffLines = 2000

// unifiedDiff renders the line changes from old to new as one unified-diff
// hunk, for widgets.DiffView. ok is false when the texts are too long to compare.
func unifiedDiff(old, new string) (diff string, ok bool) {
	a, b := strings.Split(old, "\n"), strings.Split(new, "\n")
	if len(a) > maxDiffLines || len(b) > maxDiffLines {
		return "", false
	}
	// lcs[i][j] is the longest common subsequence of a[i:] and b[j:].
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var out []string
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		switch {
		case i < len(a) && j < len(b) && a[i] == b[j]:
			out = append(out, " "+a[i])
			i, j = i+1, j+1
		case i < len(a) && (j == len(b) || lcs[i+1][j] >= lcs[i][j+1]): // removals first, as diff prints them
			out = append(out, "-"+a[i])
			i++
		default:
			out = append(out, "+"+b[j])
			j++
		}
	}
	return fmt.Sprintf("@@ -1,%d +1,%d @@\n", len(a), len(b)) + strings.Join(out, "\n"), true
}
