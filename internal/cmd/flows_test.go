package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"acline/internal/store"
)

// step is one command line as a person types it, and what it must print
// (want) or the error it must end with (fails). A flow is steps in order
// against one store: each reads what the ones before wrote.
type step struct {
	args  []string
	want  []string
	fails string
}

func ok(args string, want ...string) step { return step{args: strings.Fields(args), want: want} }
func refused(args, why string) step       { return step{args: strings.Fields(args), fails: why} }

// personCLI is a cli for a person, in a registered project "demo" whose
// directory is the working directory.
func personCLI(t *testing.T) (*cli, string) {
	t.Helper()
	c := newTestCLI(t)
	c.st.Actor = store.Actor{Type: "human", ID: "ana"}
	root := t.TempDir()
	t.Chdir(root)
	if _, err := c.st.AddProject("demo", root, ""); err != nil {
		t.Fatal(err)
	}
	return c, root
}

func runFlow(t *testing.T, c *cli, steps ...step) {
	t.Helper()
	for _, s := range steps {
		var err error
		out := string(captureStdout(t, func() { err = c.run(s.args...) }))
		line := "acline " + strings.Join(s.args, " ")
		if s.fails != "" {
			if err == nil {
				t.Fatalf("%s: succeeded, want an error with %q\n%s", line, s.fails, out)
			}
			if !strings.Contains(err.Error(), s.fails) {
				t.Fatalf("%s: error %q, want it to say %q", line, err, s.fails)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", line, err)
		}
		for _, w := range s.want {
			if !strings.Contains(out, w) {
				t.Fatalf("%s: output lacks %q:\n%s", line, w, out)
			}
		}
	}
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const twoItemPlan = `{"note":"first cut","items":[
	{"ref":"idx","title":"build the index","risk":"medium","criteria":["When a title is saved, the system shall index it"]},
	{"ref":"ui","title":"search box","depends_on":["idx"]}]}`

func TestSpecToPlanToTasks(t *testing.T) {
	c, root := personCLI(t)
	plan := writeFile(t, root, "plan.json", twoItemPlan)
	body := writeFile(t, root, "spec.md", "Index titles and bodies.\n")
	runFlow(t, c,
		ok("spec add search --body Index-titles.", "spec #1 created: search"),
		ok("spec list", "draft", "v1", "search"),
		ok("spec list --status approved --json", "[]"),
		ok("spec show 1", "#1 search (v1)", "status: draft", "Index-titles."),
		refused("spec show 9", "9"),
		refused("plan propose 1 --file "+plan, "only an approved spec can be planned"),
		ok("spec approve 1", "spec #1 approved"),
		refused("plan propose 1 --file "+filepath.Join(root, "missing.json"), "missing.json"),
		ok("plan propose 1 --file "+plan, "plan #1 proposed for spec #1 (2 item(s))"),
		ok("plan list", "#1", "spec #1", "v1", "draft"),
		ok("plan show 1", "plan #1  v1  [draft]", "note: first cut", "approving creates 2 task(s)", "idx    build the index  [risk medium, hotl]", "after=idx"),
		ok("plan edit 1 ui --title search-field --risk high", "plan #1 item ui updated"),
		refused("plan edit 1 nope --title x", "nope"),
		ok("plan show 1", "search-field  [risk high"),
		ok("plan revise 1 --file "+plan, "plan #2 supersedes #1"),
		ok("plan list --status draft", "#2"),
		ok("plan list --spec 1 --status superseded", "#1"),
		refused("plan reject 1 --note old", "only a draft can be changed"),
		refused("plan edit 1 ui --title x", "draft"),
		ok("plan approve 2", "plan #2 approved: 2 task(s) created", "idx    -> task #1", "ui     -> task #2"),
		refused("plan approve 2", "approved"),
		ok("plan show 2", "[approved]", "-> task #1", "-> task #2"),
		ok("task list", "build the index", "search box"),
		ok("task show 1", "#1 build the index", "risk:     medium", "spec:     #1", "task_created"),
		ok("task show 2", "search box"),

		// Revising an approved spec withdraws the approval and keeps the old text.
		ok("spec revise 1 --body-file "+body, "spec #1"),
		ok("spec show 1", "(v2)", "status: draft", "Index titles and bodies."),
		ok("spec show 1 --version 1", "Index-titles."),
		ok("spec add search-two --body second", "spec #2 created"),
		ok("spec supersede 1 2", "#1"),
		ok("spec list --status superseded", "search"),
	)

	// A second plan, rejected with its reason.
	runFlow(t, c,
		ok("spec approve 2", "spec #2 approved"),
		ok("plan propose 2 --file "+plan, "plan #3 proposed"),
		ok("plan reject 3 --note too-big", "plan #3"),
		ok("plan list --status rejected", "#3"),
		ok("plan show 3", "[rejected]"),
	)
}

func TestTaskLifecycleFromTheCommandLine(t *testing.T) {
	c, _ := personCLI(t)
	runFlow(t, c,
		ok("roadmap add v1 --target 2026-12-01 --desc first-release", "milestone #1 created: v1"),
		ok("task add ship-login --desc bounce-fix --priority high --area cli --type bug --risk low --milestone 1", "#1"),
		ok("task add write-docs --parent 1", "#2"),
		refused("task add", "arg"),
		refused("task add x --risk enormous", "enormous"),
		ok("task list", "ship-login", "write-docs"),
		ok("task list --status todo --area cli --risk low", "ship-login"),
		ok("task list --json", `"title":"ship-login"`),
		ok("task show 1", "priority: high", "area", "cli", "bug"),
		refused("task show 99", "99"),

		ok("task criteria add 1 When a user logs in, the system shall return them", "criterion #1"),
		ok("task criteria check 1", "#1"),
		ok("task criteria uncheck 1", "#1"),
		refused("task criteria check 99", "99"),
		ok("task link 2 depends_on 1", "#2"),
		refused("task link 2 sideways 1", "sideways"),
		ok("task assign 1 developer", "developer"),
		refused("task assign 1 nobody", "nobody"),
		ok("task update 1 --status in_progress --priority urgent", "#1"),
		ok("task update 1 --status blocked --reason waiting-on-api", "#1"),
		refused("task update 1 --reason no-status", "nothing to update"),
		refused("task update 1 --priority high --reason not-blocked", "reason"),
		ok("task show 1", "blocked", "waiting-on-api"),
		ok("task update 1 --status in_progress", "#1"),
		ok("task defer 2 --reason after-v1 --trigger v1-ships", "#2"),
		refused("task defer 1", "reason"),
		ok("task show 2", "after-v1"),
		ok("task defer 2 --clear", "#2"),

		// The gate: nothing recorded, so not done; then a passing check.
		ok("task gate 1", "check"),
		refused("task done 1", "gate"),
		ok("check record 1 --kind test --status pass --detail ran-locally", "check"),
		ok("task criteria check 1", "#1"),
		ok("task gate 1", "#1"),
		ok("task done 1", "#1"),
		ok("task show 1", "done"),
		ok("task done 2 --force", "#2"),
		ok("task list", "no tasks"),
		ok("task list --all", "ship-login"),
		ok("task archive", "2"),

		ok("roadmap list", "v1", "2026-12-01", "1/1"),
		ok("roadmap list --json", `"name":"v1"`),
		ok("roadmap list --status planned", "v1"),
		ok("roadmap show 1", "#1 v1", "first-release", "ship-login"),
		refused("roadmap show 9", "9"),
		ok("roadmap update 1 --status active --target 2027-01-01", "milestone #1 updated"),
		refused("roadmap update 1 --status someday", "someday"),
		refused("roadmap update 1", "nothing"),
		ok("roadmap show 1", "active", "2027-01-01"),
	)
}

func TestNextBriefStatusAndDashboard(t *testing.T) {
	c, _ := personCLI(t)
	runFlow(t, c,
		ok("next", "no"),
		ok("status", "no active session"),
		ok("task add build-the-index --risk medium", "#1"),
		ok("next", "task #1", "next: define_criteria", "who:"),
		ok("next 1", "define_criteria"),
		ok("next --project demo", "task #1"),
		ok("next --all-projects", "task #1"),
		refused("next 99", "99"),
		refused("next --project nowhere", "nowhere"),
		ok("brief 1", "# Task #1: build-the-index", "## Your step", "define_criteria"),
		ok("brief", "# Task #1"),
		refused("brief 99", "99"),
		ok("status", "open tasks (1)", "build-the-index"),
		ok("status --project demo", "build-the-index"),
		refused("status --project nowhere", "nowhere"),
		ok("dashboard", "acting as: human/ana", "no approval token is enabled", "open tasks: 1"),
		ok("dashboard --project demo", "open tasks: 1"),
		ok("session start --task 1", "session #1 started", "project #1"),
		ok("status", "active session: #1", "task #1"),
		ok("dashboard", "active session: #1"),
		refused("log --task 1 looked at the index", "--type is required"),
		ok("log --task 1 --type note looked at the index", ""),
		ok("history --task 1", "looked at the index"),
		ok("history --limit 1", "looked at the index"),
		ok("metrics", "tasks:          1 total, 0 done", "sessions:       1"),
	)

	var route map[string]any
	out := captureStdout(t, func() {
		if err := c.run("next", "1", "--json"); err != nil {
			t.Fatal(err)
		}
	})
	if err := json.Unmarshal(out, &route); err != nil {
		t.Fatalf("next --json is not JSON: %v\n%s", err, out)
	}
	// The session start above moved the task to in_progress.
	if route["action"] != "verify" || route["task_id"] != float64(1) || route["role"] != "qa" {
		t.Fatalf("route = %v", route)
	}
	out = captureStdout(t, func() {
		if err := c.run("brief", "1", "--json"); err != nil {
			t.Fatal(err)
		}
	})
	var brief map[string]any
	if err := json.Unmarshal(out, &brief); err != nil || len(brief) == 0 {
		t.Fatalf("brief --json is not a JSON object: %v\n%s", err, out)
	}
}

func TestSessionPolicyFromTheCommandLine(t *testing.T) {
	c, root := personCLI(t)
	runFlow(t, c,
		refused("policy show", "no active session"),
		refused("policy check --tool write_file", "no active session"),
		refused("session end", "no active session"),
		ok("session current", "no active session"),
		ok("session start --policy restricted --deny-tools network --allow-paths "+filepath.Join(root, "*")+" --deny-paths "+filepath.Join(root, "secrets", "*"), "session #1 started"),
		refused("session start", "session"),
		ok("session current", "session #1", "actor human/ana"),
		ok("policy show", "network"),
		refused("policy check --tool network", "denied by policy"),
		ok("policy check --tool read_file --path "+filepath.Join(root, "a.go"), "ALLOW"),
		refused("policy check --tool write_file --path "+filepath.Join(root, "secrets", "k"), "denied"),
		ok("session list", "ID", "human"),
		ok("session list --all-projects", "human"),
		ok("session end --summary wrapped-up --tokens-in 1200 --tokens-out 300 --cost 0.42", "session #1 ended"),
		ok("session list", "wrapped-up"),
		ok("session current", "no active session"),
	)
}

func TestVerifyFromTheCommandLine(t *testing.T) {
	c, _ := personCLI(t)
	runFlow(t, c,
		ok("task add one", "#1"),
		ok("check record 1 --kind test --status pass", "check"),
		ok("verify", "audit trail intact:", "approvals/checks intact: 1 record(s) match their seals"),
		ok("verify --head", "head: ", "acline verify --expect-head"),
		refused("verify --seal-head", "token"),
		refused("verify --expect-head 1:deadbeef", "anchor"),
		refused("verify --expect-head nonsense", "anchor"),
		ok("verify --reseal", "intact"),
	)
	head, _, err := c.st.ChainHead()
	if err != nil {
		t.Fatal(err)
	}
	token, err := c.st.EnableApprovalToken()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("ACLINE_APPROVAL_TOKEN", token)
	runFlow(t, c,
		ok("verify --expect-head "+head.String(), "intact"),
		ok("verify --seal-head", "seal"),
		ok("verify --check-seal", "seal"),
	)

	// An edited event is reported, with a failing exit.
	if _, err := c.st.DB.Exec(`DROP TRIGGER events_no_update`); err != nil {
		t.Fatal(err)
	}
	if _, err := c.st.DB.Exec(`UPDATE events SET message = 'rewritten' WHERE type = 'task_created'`); err != nil {
		t.Fatal(err)
	}
	runFlow(t, c, refused("verify", "verif"))
}

func TestMemoryAndDecisionsFromTheCommandLine(t *testing.T) {
	c, _ := personCLI(t)
	runFlow(t, c,
		ok("memory add --area cli --kind pitfall cobra reads flags before env", "memory #1"),
		refused("memory add --area cli --kind nonsense x", "nonsense"),
		ok("memory list", "cobra reads flags before env"),
		ok("memory list --json", `"body":"cobra reads flags before env"`),
		ok("memory list --status approved --all-projects", "cobra"),
		ok("memory forget 1", "#1"),
		ok("memory list --all", "cobra"),
		ok("memory restore 1", "#1"),
		ok("memory touch 1", "#1"),
		refused("memory forget 9", "9"),
		ok("memory decay", "no"),
		ok("memory decay --days 0", "#1"),
		ok("memory review", "no"),

		ok("decision add use-fts5 --context need-search --decision fts5 --rationale embedded --scope search", "decision #1"),
		ok("decision list", "use-fts5", "proposed"),
		ok("decision list --status accepted --json", "[]"),
		ok("decision show 1", "use-fts5", "need-search", "embedded"),
		refused("decision show 9", "9"),
		ok("decision accept 1", "#1"),
		ok("decision add use-bleve", "decision #2"),
		ok("decision supersede 1 2", "#1"),
		ok("decision accept 2", "#2"),
		ok("decision deprecate 2", "#2"),
		ok("decision add use-grep", "decision #3"),
		ok("decision reject 3", "#3"),
		ok("decision list --all-projects", "use-grep"),
	)

	// An agent's memory waits for a person.
	c.st.Actor = store.Actor{Type: "agent", ID: "claude-code"}
	runFlow(t, c,
		ok("memory add --area store --kind lesson one connection per store", "pending"),
		refused("memory approve 2", "agent"),
		refused("memory forget 1", "agent"),
		refused("decision accept 3", "agent"),
	)
	c.st.Actor = store.Actor{Type: "human", ID: "ana"}
	runFlow(t, c,
		ok("memory review", "one connection per store"),
		ok("memory review --project demo", "one connection per store"),
		ok("memory approve 2", "#2"),
		ok("memory add --area x --kind lesson to be rejected", "memory #3"),
	)
}

func TestRunnersSearchAndExportFromTheCommandLine(t *testing.T) {
	c, root := personCLI(t)
	out := filepath.Join(root, "export.jsonl")
	ctx := filepath.Join(root, "context.md")
	runFlow(t, c,
		ok("check runner list", "no"),
		ok("check runner set test true", "project #1 test runner: true"),
		refused("check runner set nonsense true", "nonsense"),
		ok("check runner list", "test  true"),
		ok("task add fts-index --desc full-text-search", "#1"),
		ok("check run 1 --kind test", "pass"),
		ok("check runner unset test", "project #1 test runner removed"),
		ok("note add remember the tokenizer", "note #1"),
		ok("note list", "remember the tokenizer"),
		ok("note list --unpromoted --json", `"body":"remember the tokenizer"`),
		// Search covers decisions, memory, notes and specs. A hyphen is not an operator.
		ok("search tokenizer", "tokenizer"),
		ok("note add the fts-index needs a rebuild", "note #2"),
		ok("search fts-index", "rebuild"),
		ok("search tokenizer --json --limit 5", "tokenizer"),
		ok("search tokenizer --project demo", "tokenizer"),
		ok("search zzzznothing", "no"),
		ok("export --out "+out, ""),
		ok("export --since 2020-01-01", `"kind":"task"`),
		refused("export --since yesterday", "yesterday"),
		ok("context export --out "+ctx+" --title Demo", ""),
		refused("feature add search --status planned", "invalid feature status"),
		ok("feature add search --status live --owner cli --desc full-text", "feature #1"),
		ok("feature update 1 --status deprecated", "#1"),
		ok("feature list", "search"),
		ok("feature list --json", `"name":"search"`),
		ok("dep add go example.com/x@v1.2.3 --task 1", "example.com/x"),
		ok("dep list", "example.com/x", "v1.2.3"),
		ok("dep list --unverified", "example.com/x"),
		ok("dep verify 1", "#1"),
		ok("dep list", "example.com/x"),
		ok("dep list --unverified", ""),
		ok("eval record --suite smoke --pass-rate 0.95 --sample-size 20 --task 1", "eval"),
		ok("eval list", "smoke"),
	)
	for _, p := range []string{out, ctx} {
		b, err := os.ReadFile(p)
		if err != nil || !strings.Contains(string(b), "fts-index") {
			t.Errorf("%s does not hold the task (err %v)", p, err)
		}
	}
}

// The orchestrator's commands, without an agent: --dry-run launches nothing,
// and the binary named here does not exist, so nothing could launch anyway.
func TestOrchestrateSaysWhatItWouldRunAndWhyItStops(t *testing.T) {
	c, _ := personCLI(t)
	const flags = " --allow-unprotected --dry-run --agent-bin /nonexistent/claude"
	person := func() { c.st.Actor = store.Actor{Type: "human", ID: "ana"} } // a run leaves the store acting as the orchestrator
	flow := func(steps ...step) {
		t.Helper()
		for _, s := range steps {
			person()
			runFlow(t, c, s)
		}
	}
	flow(
		refused("orchestrate step --agent-bin /nonexistent/claude", "no approval token is enabled"),
		ok("orchestrate step"+flags, "nothing to do: no open tasks"),
		ok("task add build-the-index --risk low", "#1"),
		ok("task criteria add 1 When x, the system shall y", "criterion #1"),
		ok("orchestrate step"+flags, "task #1  start_work  (role developer)", "would run: /nonexistent/claude -p", "--permission-mode dontAsk", "--max-budget-usd 1.00"),
		ok("orchestrate step 1 --step-budget 0.25 --allow-tool Bash(make_*)"+flags, "--max-budget-usd 0.25", "Bash(make_*)"),
		ok("orchestrate step --project demo"+flags, "task #1"),
		refused("orchestrate step --project nowhere"+flags, "nowhere"),
		refused("orchestrate step 99"+flags, "99"),
		ok("orchestrate run --max-steps 2"+flags, "step 1: task #1 start_work", "dry_run"),

		ok("spec add search --body Index-titles.", "spec #1"),
		ok("orchestrate plan 1"+flags, "stopped: not_plannable", "only an approved spec can be planned"),
		ok("spec approve 1", "spec #1 approved"),
		ok("orchestrate plan 1 --show-prompt"+flags, "would run: /nonexistent/claude", "Index-titles."),
		ok("orchestrate spec csv export --show-prompt"+flags, "would run:", "Bash(acline spec add *)", "csv export"),
		ok("orchestrate research which tokenizer"+flags, "would run:", "WebSearch", "Bash(acline decision add *)"),

		ok("orchestrate stop", "stop set", "no further steps will launch"),
		ok("orchestrate step 1"+flags, "stopped: stopped", "stop file present"),
		ok("orchestrate stop --clear", "stop cleared"),
		ok("orchestrate stop --clear", "stop cleared"),

		// A task a person must supervise is never launched.
		ok("task add risky --risk high", "#2"),
		ok("orchestrate step 2"+flags, "stopped: supervised_task", "risk is high"),
	)

	// Without --dry-run and with no such binary, the step is reported as failed.
	person()
	runFlow(t, c, ok("orchestrate step 1 --allow-unprotected --agent-bin /nonexistent/claude", "stopped: agent_failed", "is not installed or not on PATH"))
	if task, _ := c.st.GetTask(1); task.Status == "done" {
		t.Fatal("a step that never ran completed the task")
	}
}
