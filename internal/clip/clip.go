// Package clip shortens text to a byte budget without cutting a character in half.
//
// `s[:n]` on a string that holds multibyte characters can end in the middle of one,
// leaving invalid UTF-8 that is then stored, sent as JSON, or put in a prompt.
package clip

import (
	"strings"
	"unicode/utf8"
)

// Bytes returns s cut to at most n bytes, ending on a character boundary. A
// character that would straddle the limit is dropped whole.
func Bytes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// Tail returns the last at most n bytes of s, starting on a character boundary.
// A character that would straddle the limit is dropped whole.
func Tail(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	i := len(s) - n
	for i < len(s) && !utf8.RuneStart(s[i]) {
		i++
	}
	return s[i:]
}

// WithoutFrontMatter drops a leading YAML front-matter block ("---" … "---") and
// the blank lines after it. Markdown files acline loads into an agent's context
// carry metadata for tools (type, role, protected); to the agent it is noise paid
// for on every session. Text without a complete leading block is returned as is.
func WithoutFrontMatter(s string) string {
	rest, ok := strings.CutPrefix(s, "---\n")
	if !ok {
		return s
	}
	var body string
	if after, found := strings.CutPrefix(rest, "---\n"); found { // an empty block
		body = after
	} else if _, after, found := strings.Cut(rest, "\n---\n"); found {
		body = after
	} else {
		return s
	}
	return strings.TrimLeft(body, "\n")
}
