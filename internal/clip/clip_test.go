package clip

import (
	"testing"
	"unicode/utf8"
)

func TestBytesNeverSplitsACharacter(t *testing.T) {
	for _, s := range []string{"a" + string(make([]rune, 0)) + "€€€€€€€€", "héllo wörld ünïcode", "日本語のテキスト", "emoji 🎉🎉🎉🎉", "plain ascii text"} {
		for n := 0; n <= len(s)+2; n++ {
			got := Bytes(s, n)
			if !utf8.ValidString(got) {
				t.Fatalf("Bytes(%q, %d) = %q is not valid UTF-8", s, n, got)
			}
			if len(got) > n {
				t.Fatalf("Bytes(%q, %d) = %q is longer than the budget", s, n, got)
			}
			if len(s) >= n && n >= 4 && len(got) < n-3 {
				t.Fatalf("Bytes(%q, %d) = %q gave up more than one character", s, n, got)
			}
		}
	}
}

func TestBytesLeavesShortAndExactTextAlone(t *testing.T) {
	if got := Bytes("€", 3); got != "€" {
		t.Errorf("exact fit = %q", got)
	}
	if got := Bytes("€", 2); got != "" {
		t.Errorf("a character that does not fit is dropped whole, got %q", got)
	}
	if got := Bytes("short", 100); got != "short" {
		t.Errorf("short text changed: %q", got)
	}
	if got := Bytes("anything", 0); got != "" {
		t.Errorf("zero budget = %q", got)
	}
	if got := Bytes("anything", -5); got != "" {
		t.Errorf("negative budget = %q", got)
	}
}

func TestWithoutFrontMatterDropsOnlyALeadingBlock(t *testing.T) {
	for in, want := range map[string]string{
		"---\ntype: soul\nprotected: true\n---\n\n# SOUL\nbody\n": "# SOUL\nbody\n",
		"---\ntype: x\n---\nbody":                                 "body",
		"# no front matter\n---\nnot a block\n---\n":              "# no front matter\n---\nnot a block\n---\n",
		"---\nunterminated\nbody":                                 "---\nunterminated\nbody",
		"":                                                        "",
		"---\n---\nbody":                                          "body",
	} {
		if got := WithoutFrontMatter(in); got != want {
			t.Errorf("WithoutFrontMatter(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTailKeepsTheEndOnACharacterBoundary(t *testing.T) {
	for _, tc := range []struct {
		s    string
		n    int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello", 3, "llo"},
		{"aé", 1, ""}, // 'é' is two bytes and would be cut
		{"aéb", 2, "b"},
		{"aéb", 3, "éb"},
		{"x", 0, ""},
	} {
		if got := Tail(tc.s, tc.n); got != tc.want || !utf8.ValidString(got) {
			t.Errorf("Tail(%q, %d) = %q, want %q", tc.s, tc.n, got, tc.want)
		}
	}
}
