package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Exported setters such as SetSpecStatus wrote a status with no authority
// check, so "every rule lives in the store" held only while no adapter called
// them. This test lists every exported *Store method that can change a status
// column, directly or through helpers, and fails on one nobody has reviewed:
// adding a status writer means deciding who may call it.

// reviewedStatusWriters are the exported methods allowed to change a status,
// with what guards each one.
var reviewedStatusWriters = map[string]string{
	"AcceptDecision":          "requirePerson",
	"RejectDecision":          "retireDecision: requirePerson once accepted; rejecting a proposal only tightens",
	"SupersedeDecision":       "retireDecision: requirePerson once accepted",
	"ApproveSpec":             "requirePerson",
	"ReviseSpec":              "only ever withdraws approval (approved -> draft), recorded as spec_revised",
	"ReviewMemory":            "authorize; approving needs a person",
	"CompleteTask":            "the completion gate; --force needs a person",
	"CompleteTaskForTree":     "the completion gate; --force needs a person",
	"SetTaskStatus":           "refuses done (ErrStatusDoneNeedsGate)",
	"SetTaskStatusWithReason": "refuses done (ErrStatusDoneNeedsGate)",
	"BeginSession":            "only moves the task to in_progress",
	"ApprovePlan":             "requirePerson",
	"RejectPlan":              "rejecting only tightens",
	"RevisePlan":              "a new draft supersedes the previous draft only",
	"SetFeatureStatus":        "features are descriptive, not gate inputs",
	"SetMilestoneStatus":      "milestones are descriptive, not gate inputs",
}

var statusWriteRe = regexp.MustCompile(`(?is)UPDATE\s+\w+\s+SET\b[^;]*\bstatus\s*=`)

func TestEveryExportedStatusWriterIsReviewed(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	direct := map[string]bool{}    // function name -> contains a status UPDATE
	calls := map[string][]string{} // function name -> names it calls
	exported := map[string]bool{}  // exported methods on *Store
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			key := fn.Name.Name
			if fn.Recv != nil && ast.IsExported(key) {
				exported[key] = true
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.BasicLit:
					if x.Kind == token.STRING && statusWriteRe.MatchString(x.Value) {
						direct[key] = true
					}
				case *ast.CallExpr:
					switch f := x.Fun.(type) {
					case *ast.Ident:
						calls[key] = append(calls[key], f.Name)
					case *ast.SelectorExpr:
						calls[key] = append(calls[key], f.Sel.Name)
					}
				}
				return true
			})
		}
	}

	memo := map[string]bool{}
	var writes func(name string, seen map[string]bool) bool
	writes = func(name string, seen map[string]bool) bool {
		if v, ok := memo[name]; ok {
			return v
		}
		if seen[name] {
			return false
		}
		seen[name] = true
		result := direct[name]
		for _, c := range calls[name] {
			if !result && writes(c, seen) {
				result = true
			}
		}
		memo[name] = result
		return result
	}

	var unreviewed []string
	for name := range exported {
		if writes(name, map[string]bool{}) {
			if _, ok := reviewedStatusWriters[name]; !ok {
				unreviewed = append(unreviewed, name)
			}
		}
	}
	sort.Strings(unreviewed)
	if len(unreviewed) > 0 {
		t.Errorf("exported methods that can change a status column without a review: %v\n"+
			"decide who may call each (requirePerson? only tightens?) and add it to reviewedStatusWriters", unreviewed)
	}
	for name := range reviewedStatusWriters {
		if !exported[name] || !writes(name, map[string]bool{}) {
			t.Errorf("reviewedStatusWriters lists %s, which is not an exported method that can change a status: remove it", name)
		}
	}
	for _, gone := range []string{"SetSpecStatus", "SetDecisionStatus", "SetMemoryStatus", "UpdateTaskStatus"} {
		if exported[gone] {
			t.Errorf("%s is exported again: it writes a status with no authority check", gone)
		}
	}
}
