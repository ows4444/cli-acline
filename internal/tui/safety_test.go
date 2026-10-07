package tui

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"github.com/ows4444/tui/tuitest"

	"acline/internal/store"
)

// privileged is one action only a person may take, as a person reaches it:
// the screen it is on, the keys that get to its row, and the keys that ask
// for it and say yes. Rejecting a task, a plan, a proposed decision or pending
// memory is not here: it only makes things stricter, and needs no token.
type privileged struct {
	name string
	open func(t *testing.T) (*tuitest.Session, *store.Store)
	nav  []string
	act  []string
}

func openDetail(t *testing.T) (*tuitest.Session, *store.Store) {
	return actionSession(t, store.TaskOpts{Risk: "high"})
}

func openReview(t *testing.T) (*tuitest.Session, *store.Store) {
	st := openTestStore(t)
	seedReview(t, st)
	return reviewSession(t, st), st
}

func openMemory(t *testing.T) (*tuitest.Session, *store.Store) {
	st := openTestStore(t)
	if _, err := st.AddMemory("cli", "pitfall", "cobra reads flags before env", store.MemoryOpts{ForcePending: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddMemory("store", "constraint", "one connection per store"); err != nil {
		t.Fatal(err)
	}
	return memorySession(t, st), st
}

func openAudit(t *testing.T) (*tuitest.Session, *store.Store) {
	st := openTestStore(t)
	seedEvents(t, st)
	return auditSession(t, st), st
}

var privilegedActions = []privileged{
	{"detail: approve a task", openDetail, nil, []string{"a", "y"}},
	{"detail: force a task done", openDetail, nil, []string{"D", "y"}},
	{"detail: lower a task's risk", openDetail, nil, []string{"R", "up", "enter"}},
	{"detail: raise a task's autonomy", openDetail, nil, []string{"U", "down", "enter"}},
	{"review: approve a task", openReview, nil, []string{"a", "y"}},
	{"review: approve a spec", openReview, []string{"down"}, []string{"a", "y"}},
	{"review: approve a plan", openReview, []string{"down", "down"}, []string{"a", "y"}},
	{"review: accept a decision", openReview, []string{"down", "down", "down"}, []string{"a", "y"}},
	{"review: approve memory", openReview, []string{"down", "down", "down", "down"}, []string{"a", "y"}},
	{"specs: approve a plan", specsSession, []string{"right", "down"}, []string{"a", "y"}},
	{"memory: approve", openMemory, nil, []string{"a", "y"}},
	{"memory: forget", openMemory, []string{"f"}, []string{"F", "y"}},
	{"memory: reconfirm", openMemory, []string{"f"}, []string{"t", "y"}},
	{"audit: seal the head", openAudit, nil, []string{"S", "y"}},
}

// contents is a digest of every row of every table, but for the events that
// record a refused token: equal digests mean nothing else was written.
func contents(t *testing.T, st *store.Store) string {
	t.Helper()
	rows, err := st.DB.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, n)
	}
	rows.Close()
	h := sha256.New()
	for _, table := range tables {
		q := `SELECT * FROM "` + table + `"`
		if table == "events" {
			q += ` WHERE type <> 'approval_token'`
		}
		r, err := st.DB.Query(q)
		if err != nil {
			continue // a virtual table's shadow that cannot be read directly
		}
		cols, _ := r.Columns()
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		for r.Next() {
			if err := r.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(h, "%s %v\n", table, vals)
		}
		r.Close()
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func refusals(t *testing.T, st *store.Store) (n int) {
	t.Helper()
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE type = 'approval_token' AND message LIKE '%FAILED%'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// With a token on the store, every privileged action asks for it on the TUI's
// own screen. A wrong one is refused and writes nothing but the refusal, which
// goes in the audit trail; the right one works; and the token is on no frame,
// while typed or after.
func TestEveryPrivilegedActionNeedsTheTokenAndNeverShowsIt(t *testing.T) {
	for _, p := range privilegedActions {
		t.Run(p.name, func(t *testing.T) {
			s, st := p.open(t)
			token, err := st.EnableApprovalToken()
			if err != nil {
				t.Fatal(err)
			}
			before := contents(t, st)
			s.Keys(p.nav...)

			s.Keys(p.act...)
			if !strings.Contains(rendered(s), "Approval token for") {
				t.Fatalf("no token prompt:\n%s", rendered(s))
			}
			if contents(t, st) != before {
				t.Fatal("something was written before the token was given")
			}
			s.Keys("not-the-token", "enter")
			shows(t, s, "invalid approval token")
			if contents(t, st) != before {
				t.Fatal("a wrong token wrote to the store")
			}
			if n := refusals(t, st); n != 1 {
				t.Fatalf("the audit trail has %d refused-token event(s), want 1", n)
			}

			s.Keys(p.act...)
			s.Keys(token)
			if got := rendered(s); strings.Contains(got, token[:8]) {
				t.Fatalf("the token is visible while typed:\n%s", got)
			}
			s.Keys("enter")
			if got := rendered(s); strings.Contains(got, token[:8]) {
				t.Fatalf("the token is visible after use:\n%s", got)
			}
			lacks(t, s, "invalid approval token", "Approval token for")
			if contents(t, st) == before {
				t.Fatalf("the right token changed nothing:\n%s", rendered(s))
			}
		})
	}
}

// Run refuses an agent before anything is drawn. Were that ever bypassed, the
// store still refuses: an agent at the keys changes nothing a person must.
func TestAnAgentAtTheKeysCanTakeNoPrivilegedAction(t *testing.T) {
	for _, p := range privilegedActions {
		t.Run(p.name, func(t *testing.T) {
			s, st := p.open(t)
			st.Actor = store.Actor{Type: "agent", ID: "claude-code"}
			before := contents(t, st)
			s.Keys(p.nav...)
			s.Keys(p.act...)
			if contents(t, st) != before {
				t.Fatalf("an agent's action was written:\n%s", rendered(s))
			}
		})
	}
}
