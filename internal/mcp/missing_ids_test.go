package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Every tool, given an id of a record that does not exist, answers in words
// ("task #999: not found"), never with the database's own error (a foreign
// key failure, a scan of no rows). Required arguments get placeholder values,
// so some calls stop at another check first; none may leak SQL.
func TestMissingIDsNeverLeakDatabaseErrors(t *testing.T) {
	cs, _ := connectedTestServer(t)
	tools, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var probed int
	for _, tool := range tools.Tools {
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var schema struct {
			Properties map[string]struct {
				Type any `json:"type"`
			} `json:"properties"`
			Required []string `json:"required"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatal(err)
		}
		for field := range schema.Properties {
			if field != "id" && !strings.HasSuffix(field, "_id") {
				continue
			}
			args := map[string]any{}
			for _, r := range schema.Required {
				switch ty := fmt.Sprint(schema.Properties[r].Type); {
				case strings.Contains(ty, "integer"):
					args[r] = 1
				case strings.Contains(ty, "number"):
					args[r] = 0.5
				case strings.Contains(ty, "array"):
					args[r] = []any{}
				case strings.Contains(ty, "boolean"):
					args[r] = false
				default:
					args[r] = "x"
				}
			}
			args[field] = 999
			res, err := cs.CallTool(t.Context(), &sdkmcp.CallToolParams{Name: tool.Name, Arguments: args})
			msg := fmt.Sprint(err)
			if res != nil {
				for _, c := range res.Content {
					if tc, ok := c.(*sdkmcp.TextContent); ok {
						msg += " " + tc.Text
					}
				}
			}
			probed++
			low := strings.ToLower(msg)
			for _, leak := range []string{"constraint", "sql:", "no rows", "scan"} {
				if strings.Contains(low, leak) {
					t.Errorf("%s with %s=999 leaks a database error: %s", tool.Name, field, msg)
				}
			}
		}
	}
	if probed < 40 {
		t.Fatalf("probed only %d id arguments; the schema walk is broken", probed)
	}
}
