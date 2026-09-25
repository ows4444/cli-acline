# acline

SDLC state (specs, tasks, decisions, verification, audit trail) lives in a single shared database (default `~/.acline/store.db`, override via `--db`/`$ACLINE_DB`), driven by the `acline` CLI and scoped to this project via `acline project use` or `--project`. Use it instead of ad hoc notes. This file is a cheat sheet — command flags via `acline <cmd> --help`.

## Session start

```sh
export ACLINE_ACTOR_TYPE=agent ACLINE_ACTOR=claude-code   # ACLINE_MODEL: set by the SessionStart hook
acline dashboard
```

`dashboard` = active session + urgent/high-risk tasks + accepted decisions + memory + pending reviews. Read it instead of re-deriving state from files.

## Loop

| Step | Command |
|---|---|
| Spec (non-trivial work) | `acline spec add "<title>" --body "..."` → `acline spec approve <id>` |
| Task | `acline task add "<title>" --spec <id> --area <area> --risk <r> --autonomy <a>` (defaults: risk=low, autonomy=hotl) |
| Session | `acline session start --task <id>` ... `acline session end -m "<summary>"` |
| Decision (durable/expensive to reverse) | `acline decision add "<title>" --context .. --decision .. --rationale ..` |
| Note (promote later via `/reflect`) | `acline note add "..."` |
| Audit event (history only, never reaches `/reflect`) | `acline log "..." --type decision\|bug\|commit\|blocker --task <id>` |
| Criteria (EARS: `When X, the system shall Y`) | `acline task criteria add <id> "..."` |
| Verify | `acline check record <id> --kind test\|sast\|sca\|lint\|human_review\|eval --status pass\|fail` |
| Approve (risk=high/critical or autonomy=hitl) | `acline approve <id> --by <person>` (agents cannot self-approve) |
| Complete | `acline task done <id>` |
| Dependency added | `acline dep add <ecosystem> <name>@<version> --task <id>` |

**Gate:** `task done` refuses without a recorded check, with a failing check, or (risk=high/critical or autonomy=hitl) without approval. `--force` overrides and is logged — don't use it to route around a correct gate.

**Areas:** cli, store, hooks, skills, docs. **Default risk:** `low`. **Default autonomy:** `hotl`.

## Memory

`acline memory add "..." --kind constraint\|lesson\|pitfall\|operational\|failure_pattern --area <a>` — non-obvious facts only, not routine notes. Agent-written entries need review (`acline memory review`); mention pending count at session end. Approved entries not reconfirmed in 90+ days surface via `acline memory decay` — `acline memory touch <id>` to reconfirm, `acline memory forget <id>` to retire.

## Regenerating context

`acline context export -o AGENTS.md` renders live decisions/specs/features/tasks from the DB — regenerate it, don't hand-edit it. This file (CLAUDE.md) is the opposite: hand-edited, stable conventions only.

## Persisting state

The database is shared across every tracked project, not per-repo — `acline snapshot export` dumps the *entire* store, not just this project, so it isn't a per-repo git-commit target the way it was in the pre-redesign tool. Back up `~/.acline/store.db` (or your `$ACLINE_DB` location) directly, or use `acline snapshot export`/`import` for a full-store JSON backup, not a per-project one.
