package mcp

import (
	"os"
	"regexp"
	"strconv"
	"testing"
)

// docs/ARCHITECTURE.md says how many tools the server offers; it drifted
// twice (71, then 73, while the server had more).
func TestArchitectureDocCountsEveryTool(t *testing.T) {
	doc, err := os.ReadFile("../../docs/ARCHITECTURE.md")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`\((\d+) tools`).FindSubmatch(doc)
	if m == nil {
		t.Fatal(`ARCHITECTURE.md no longer says "(N tools"; update this test`)
	}
	documented, _ := strconv.Atoi(string(m[1]))
	cs, _ := connectedTestServer(t)
	res, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if documented != len(res.Tools) {
		t.Fatalf("ARCHITECTURE.md says %d tools, the server registers %d", documented, len(res.Tools))
	}
}
