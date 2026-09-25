package cmd

import (
	"fmt"
	"strings"
)

// initAnswers holds the core questions asked at `acline init` time. They shape
// the generated CLAUDE.md, which is the onboarding doc for whichever agent
// works in this project next.
type initAnswers struct {
	ProjectName string
	Areas       []string // e.g. backend, frontend, mobile
	DefaultRisk string   // low|medium|high|critical
	DefaultHITL bool     // default new tasks to autonomy=hitl instead of hotl
	Scaffold    bool     // .claude/ hooks, skills, and vault were scaffolded alongside
}

// renderClaudeMD builds the onboarding doc as a dense reference, not prose.
// This file is read at the start of every session an agent works in this
// project, so its size is a recurring token cost — optimize for scanability
// (tables, one-line rules) over explanation. Unlike `acline context export`
// (regenerated from live database state), this file is written once and
// meant to be hand-edited as conventions change.
func renderClaudeMD(a initAnswers) string {
	var b strings.Builder

	title := a.ProjectName
	if title == "" {
		title = "this project"
	}
	risk := a.DefaultRisk
	if risk == "" {
		risk = "low"
	}
	autonomy := "hotl"
	if a.DefaultHITL {
		autonomy = "hitl"
	}
	areas := "none fixed — pick a short, consistent one"
	if len(a.Areas) > 0 {
		areas = strings.Join(a.Areas, ", ")
	}

	fmt.Fprintf(&b, "# %s\n\n", title)
	b.WriteString("SDLC state (specs, tasks, decisions, verification, audit trail) lives in " +
		"a single shared database (default `~/.acline/store.db`, override via `--db`/`$ACLINE_DB`), " +
		"driven by the `acline` CLI and scoped to this project via `acline project use` or `--project`. " +
		"Use it instead of ad hoc notes. This file is a cheat sheet — command flags via `acline <cmd> --help`.\n\n")

	b.WriteString("## Session start\n\n")
	if a.Scaffold {
		b.WriteString("The SessionStart hook injects `acline dashboard` and `acline context export` (SOUL/USER, decisions, specs, tasks, memory) automatically. " +
			"Re-run `acline dashboard` to refresh mid-session.\n\n")
	} else {
		b.WriteString("```sh\nacline dashboard\n```\n\n")
	}
	b.WriteString("Actor attribution (`ACLINE_ACTOR_TYPE`, `ACLINE_ACTOR`) comes from `env` in `.claude/settings.json`; " +
		"the SessionStart hook exports `ACLINE_MODEL` for the model actually running. Don't hardcode either here.\n\n")
	b.WriteString("`dashboard` = active session + urgent/high-risk tasks + accepted decisions + memory + pending reviews. " +
		"Read it instead of re-deriving state from files.\n\n")

	b.WriteString("## Loop\n\n")
	b.WriteString("| Step | Command |\n|---|---|\n")
	b.WriteString("| Spec (non-trivial work) | `acline spec add \"<title>\" --body \"...\"` → `acline spec approve <id>` |\n")
	fmt.Fprintf(&b, "| Task | `acline task add \"<title>\" --spec <id> --area <area> --risk <r> --autonomy <a>` (defaults: risk=%s, autonomy=%s) |\n", risk, autonomy)
	b.WriteString("| Session | `acline session start --task <id>` ... `acline session end -m \"<summary>\"` |\n")
	b.WriteString("| Decision (durable/expensive to reverse) | `acline decision add \"<title>\" --context .. --decision .. --rationale ..` |\n")
	b.WriteString("| Note (promote later via `/reflect`) | `acline note add \"...\"` |\n")
	b.WriteString("| Audit event (history only, never reaches `/reflect`) | `acline log \"...\" --type decision\\|bug\\|commit\\|blocker --task <id>` |\n")
	b.WriteString("| Criteria (EARS: `When X, the system shall Y`) | `acline task criteria add <id> \"...\"` |\n")
	b.WriteString("| Verify | `acline check run <id> --kind test\\|sast\\|sca\\|lint` (runs the tool; high/critical risk needs one of these against the current code). `acline check record` is for results no tool produces (`eval`, a person's `human_review`) |\n")
	b.WriteString("| Approve (risk=high/critical or autonomy=hitl) | `acline approve <id> --by <person>` (agents cannot self-approve) |\n")
	b.WriteString("| Complete | `acline task done <id>` |\n")
	b.WriteString("| Dependency added | `acline dep add <ecosystem> <name>@<version> --task <id>` |\n\n")

	b.WriteString("**Gate:** `task done` refuses without a recorded check, with a failing check, or " +
		"(risk=high/critical or autonomy=hitl) without approval. `--force` overrides and is logged — don't use it to route around a correct gate. " +
		"Approving, `--force`, loosening a task's risk or autonomy, and accepting a decision or spec need a person (or the approval token); " +
		"until `acline auth init` enables the token, identity is self-declared.\n\n")

	fmt.Fprintf(&b, "**Areas:** %s. **Default risk:** `%s`. **Default autonomy:** `%s`.\n\n", areas, risk, autonomy)

	b.WriteString("## Memory\n\n")
	b.WriteString("`acline memory add \"...\" --kind constraint\\|lesson\\|pitfall\\|operational\\|failure_pattern --area <a>` " +
		"— non-obvious facts only, not routine notes. Agent-written entries need review (`acline memory review`); mention pending count at session end. " +
		"Approved entries not reconfirmed in 90+ days surface via `acline memory decay` — `acline memory touch <id>` to reconfirm, `acline memory forget <id>` to retire.\n\n")

	if a.Scaffold {
		b.WriteString("## Memory layer\n\n")
		b.WriteString("- **Capture:** write `MEMORY_LOG: <fact>` on its own line in a response; the Stop, PreCompact and SessionEnd hooks record it as an `acline note`. For anything significant, run `acline note add` directly.\n")
		b.WriteString("- **Promote:** `/reflect` reviews notes and promotes the durable ones to a decision or memory entry, per user approval.\n")
		b.WriteString("- **Look up:** `/recall <topic>` (wraps `acline search`) before re-deriving past context.\n")
		b.WriteString("- **Identity & boundaries:** `.claude/vault/SOUL.md` sets autonomy and the hard boundaries that apply inside the loop above " +
			"(write-protected — suggest changes via `acline note add \"suggested SOUL.md change: ...\"`); `.claude/vault/USER.md` holds preferences. Both are injected every session.\n")
		b.WriteString("- **Where things go:** the `vault-structure` skill. Track another repo with `/track-project`.\n\n")
	}

	b.WriteString("## Regenerating context\n\n")
	if a.Scaffold {
		b.WriteString("The SessionStart hook injects `acline context export` live, so Claude Code needs no generated file. " +
			"For agents that read `AGENTS.md` instead (Codex, Cursor, ...), run `acline context export -o AGENTS.md` — regenerate it, don't hand-edit it. ")
	} else {
		b.WriteString("`acline context export -o AGENTS.md` renders live decisions/specs/features/tasks from the DB — regenerate it, don't hand-edit it. ")
	}
	b.WriteString("This file (CLAUDE.md) is the opposite: hand-edited, stable conventions only.\n\n")

	b.WriteString("## Persisting state\n\n")
	b.WriteString("The database is shared across every tracked project, not per-repo — `acline snapshot export` " +
		"dumps the *entire* store, not just this project, so it isn't a per-repo git-commit target the way it " +
		"was in the pre-redesign tool. Back up `~/.acline/store.db` (or your `$ACLINE_DB` location) directly, " +
		"or use `acline snapshot export`/`import` for a full-store JSON backup, not a per-project one.\n")

	return b.String()
}
