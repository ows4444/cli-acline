# Architecture

acline is one Go binary (`main.go` → `internal/cmd`) over one SQLite database. Everything else — the CLI, the MCP server, the Claude Code hooks, the orchestrator — is a thin adapter around the same store, so a rule enforced in the store holds for every entry point.

```
                  ┌──────────── Claude Code ────────────┐
                  │  hooks: acline hook <event>         │   MCP: acline mcp serve
                  │  PreToolUse → guard (fails closed)  │   (71 tools + acline://events)
                  └───────┬───────────────────────┬─────┘        │
   person ── acline <cmd> ┤                       │              │
   orchestrator ──────────┤                       │              │
                          ▼                       ▼              ▼
                     internal/cmd  (cobra)   internal/mcp     internal/orchestrate
                          └───────────┬─────────────┘               │ launches `claude -p`
                                      ▼                             │ (bounded, allow-listed)
                               internal/app  (shared use-cases)     │
                                      ▼                             │
                               internal/store  ◄────────────────────┘
                                      ▼
                              SQLite (WAL) ~/.acline/store.db
```

## Packages

| Package | Role |
|---|---|
| `internal/store` | The whole data model and **every rule**: schema and migrations, the completion gate, approvals and seals, the audit chain, authority checks, search, snapshots. Depends on nothing above it. |
| `internal/app` | Use-cases both adapters share (project/role resolution, approve/reject, the dashboard view). |
| `internal/cmd` | The cobra CLI, the guard (`guard*.go`), the hooks (`hook.go`), `doctor`. Holds one package-global `st *store.Store`. |
| `internal/mcp` | The MCP server: tools, a resource, and middleware (session policy, stable `error_code`s, client-version warning). Also generates the VS Code extension's TypeScript types. |
| `internal/orchestrate` | Runs one bounded agent step (`step`, `run`, `plan`, `spec`, `research`) and re-reads recorded state to decide what happens next. |
| `internal/brief`, `internal/untrusted` | Assemble an agent's prompt; fence recorded text as data. |
| `internal/checkrun`, `internal/worktree` | Run a verification tool; fingerprint the working tree a result is about. |
| `internal/redact`, `internal/clip` | Strip secrets from text on its way in; cut text on character boundaries. |
| `internal/embed` | Optional Voyage embeddings for `search --semantic` (off unless `VOYAGE_API_KEY` is set). |
| `internal/scaffold` | The embedded assets `acline init` writes: `settings.json`, skills, agent stubs, the vault template. |
| `vscode-acline/` | The VS Code extension; talks to `acline mcp serve`. |

## The store

- **One shared database.** `~/.acline/store.db` (or `--db` / `$ACLINE_DB`), created 0600, WAL, `busy_timeout`, `foreign_keys`, `_txlock=immediate`, one connection. Rows carry a `project_id`; a directory that resolves to no project runs **unscoped, which means every project** (a single-project store needs no `project` commands; see the threat model).
- **Migrations** are numbered, run once each inside a transaction, and tracked in `PRAGMA user_version` (currently 13). Before migrating an existing store, acline writes `<store>.pre-v<N>` (a consistent `VACUUM INTO`, 0600). A store *newer* than the binary is refused untouched.
- **Append-only evidence.** `events`, `approvals` and `checks` have triggers that reject `UPDATE` and `DELETE`.
- **Hash-chained events.** Each event stores the hash of the previous one (length-prefixed SHA-256). From the chain's `hash_version` marker event on, the hash also covers the event's `role_id`; a row with no hash after hashed ones is reported as tampering. `acline verify` recomputes the chain; `verify --head` prints an anchor to keep somewhere the database's writers cannot reach. `verify --seal-head` records a `head_sealed` event: the head's HMAC keyed by the approval token (never the stored hash of it), so recomputing the chain after a rewrite can't produce a seal that `verify --check-seal` accepts. Rotating the token re-seals with the new one.
- **Sealed approvals and checks.** Inserting one writes a `row_seal` event, inside the chain and in the same transaction, holding a digest of the row. The first sealed insert also writes a `seal_watermark` event; from then on a record with **no** seal and a higher id is reported as tampering (a direct insert, a snapshot import). A check's digest covers its `source` and `tree_hash`. `acline verify --reseal` (a person's action) seals the records that predate sealing and writes a `legacy_attested` event with a digest of the events before the `hash_version` marker (full content of unhashed ones, the role of every one); after it, no record may be unsealed and that region must still match.
- **Scrubbing.** `scrubText` redacts recognisable secret values in every write path, so no adapter can forget.
- **History that matters is kept.** Revising an approved spec stores the replaced text in `spec_versions`.

## Authority: who may do what

Identity is `ACLINE_ACTOR_TYPE` (`human` or `agent`). Until a person runs `acline auth init` that is *self-declared*; with a token enabled, the privileged actions below require proof of a secret an agent is never given (it is entered on `/dev/tty`, stored only as a hash, and stripped from the orchestrator's environment).

| Action | Without a token | With a token enabled |
|---|---|---|
| Approve a task; accept a decision; approve a spec or plan; approve memory | human actor | token, from anyone |
| Reject or supersede an **accepted** decision | human actor | token, from anyone |
| Complete past an unsatisfied gate (`--force`) | human actor | token |
| Record a passing `human_review`; record or mark a dependency verified | human actor | token |
| Lower a task's risk, loosen its autonomy, create an `auto` task | human actor | token |
| Create a `can_approve` role; act as a `human`-only role | human actor / never for an agent | token / never for an agent |
| Set a check runner; run `check run --cmd` (a command of the caller's choosing); import a snapshot into a store with data | human actor | token |
| Register a project **path** (it joins the guard's write scope) | human actor | token |
| End a session running as a read-only role (security, qa, architect, designer) | human actor (the orchestrator ends the sessions it launched) | token |
| Enable, rotate or disable the token | a human on a real terminal | the current token, on a terminal |
| Draft a spec or plan, record a check run, add a task, log, note, propose a decision | anyone | anyone |
| Promote a task's autonomy on an eval (`PromoteAutonomy`) | human actor; an agent only on an eval a person recorded, at the default threshold or above | same |

Rejecting a task, plan or proposed decision is open to everyone: it can only make things stricter. Rejecting or superseding an accepted decision is not, because it removes a rule later work is checked against.

## The completion gate

`acline task done` fingerprints the gate's inputs, calls `EvaluateGateForTree`, then writes the status change and its events in one transaction — after re-reading the fingerprint inside it and re-evaluating if another process changed anything. It refuses unless:

1. at least one check is recorded and none of the newest-per-kind results is failing, and no typed pass replaces a newer failure acline ran (an agent's typed pass, or any at high risk);
2. for **high/critical risk**: a `sast`/`sca` result is not merely *skipped*; **every** runnable kind's (`test/lint/sast/sca`) passing result was **run by acline** (`check run` with the default or the project's runner — not typed in, not `--cmd`); none of those runner passes is about a different tree than the current one or bound to no tree; and the current tree could be fingerprinted;
3. for **high/critical risk or `hitl` autonomy**: the newest `code_review` decision is an approval (and, once a project has a `can_approve` role, carries one).

Warnings (never blockers, below high risk): an agent's hand-typed pass of a runnable kind, a pass about older code, a tree that could not be fingerprinted, open prerequisites and unchecked criteria. A person, or a token holder, can override with `--force`; the override is recorded as an approval row and an event.

## Guard and hooks

`acline init` writes `.claude/settings.json` so Claude Code runs:

| Event | Command |
|---|---|
| `PreToolUse` (Read, Edit, Write, Grep, Glob, Bash, NotebookEdit, WebFetch, WebSearch, Task, `mcp__.*`) | `acline hook pre-tool-use \|\| exit 2` |
| `PostToolUse` (Edit, Write, NotebookEdit, Bash) | `acline hook post-tool-use` — records a `tool_used` event (the path written, or the command's digest and a scrubbed prefix) on the active session and its task; never blocks |
| `SessionStart` | `acline hook session-start` — injects the dashboard and a bounded `context export` (40 rows, fenced task titles), exports the running model |
| `PreCompact` / `SessionEnd` | `acline hook pre-compact` / `session-end` — record `MEMORY_LOG:` lines as notes |
| `Stop` | `acline hook stop` — records the turn's `MEMORY_LOG:` lines, and blocks the stop once when the active task's passing checks ran against code that has changed since (fails open; never loops) |

`settings.json` also ships `permissions.deny` rules (secret directories, `.env` at any depth, key files, force-push, `reset --hard`, `acline auth`/`snapshot import`/`project add`, `sqlite3`, the store directory, edits to `.claude/settings*.json`) as a second layer that does not depend on `acline` being reachable; `init` merges them into an existing file without touching the project's own rules.

**Fail closed.** The pre-tool-use hook denies whenever it cannot decide (unreadable payload, store unavailable, a panic, no answer within 10 s), and the `|| exit 2` makes a missing `acline` binary block the call, because Claude Code treats exit 2 as a block and any other non-zero exit as a non-blocking error.

**What the guard checks** (`internal/cmd/guard*.go`): secret-file paths (Read and shell readers) and search globs that select secret files (Grep's `glob`, `grep --include`, `rg -g`), destructive and irreversible commands (`rm -rf`, `git push --force`, `git reset --hard`, `find -delete`, …), writes to the SOUL/role files, to `.claude/settings*.json` (which run the guard) and to the store itself, identity tampering (`ACLINE_ACTOR_TYPE=human`, the token, `auth init`, `snapshot import`, `project add`), write scope (the vault and the project the session runs in; every tracked project with `ACLINE_GUARD_ALL_PROJECTS=1` or where no project resolves), the active session's tool/path policy (paths compared through symlinks), and — for a session running as a read-only role (security, qa, architect, designer) — any file write, including indirect ones (git, formatters in write mode, `make`, archives, downloads) and ending or restarting the session. Shell commands are judged on a shell-aware view, so quoted prose is inert.

**The guard is a best-effort denylist, not a sandbox.** It raises the cost of the obvious routes; a program that writes as a side effect, or a script file an interpreter runs, is beyond it.

## MCP server

`acline mcp serve` exposes the store as tools over stdio, plus the `acline://events` resource and `acline://projects/{project}/events` / `acline://tasks/{id}/events` templates. Every tool carries MCP annotations (read-only, destructive, idempotent, open-world) from one classification in `internal/mcp/annotations.go`. `--toolset read|capture|all` picks what is offered; the default is `capture` (no approvals or other decisions) for an agent actor and `all` for a person's client. A subscriber to `acline://events` gets `resources/updated` when another process changes the store, and `acline_check_run` reports progress while its tool runs. Mutating tools are gated by the active session's policy; every failed call carries a stable `error_code` (`gate_blocked`, `agent_cannot_approve`, `agent_cannot_override_gate`, `not_found`, `busy`, …). The server's actor must be declared: `--as human|agent`, or `ACLINE_ACTOR_TYPE`/`ACLINE_MODEL` in its environment (an undeclared server refuses to start, and `--as human` cannot override an agent environment); `token` is an explicit argument, never read from the environment. The extension's TypeScript types are generated from the `*Out` structs (`go generate ./internal/mcp`) and a test fails if they drift.

## The orchestrator

`acline orchestrate step|run|plan|spec|research` runs one non-interactive Claude Code session (`claude -p`, `--permission-mode dontAsk`, a spend cap, a timeout) and then re-reads **recorded state** — never the agent's own account — to decide what next. Its invariants:

- it runs as `agent/orchestrator`, never sees the approval token, and refuses to launch unless a token is enabled (`--allow-unprotected` accepts the risk);
- the agent's environment is an **allow-list** (`PATH`, `HOME`, locale, proxy, Go, `ANTHROPIC_*`, `CLAUDE_*`, …); anything else needs `--pass-env NAME`;
- every shell command it runs is under Claude Code's **Bash sandbox** (`--settings`, `internal/orchestrate/sandbox.go`): credential files such as `~/.ssh` and `~/.aws` unreadable, writes only to the project, temp, the store's directory and the Go caches, network only to the Go module proxy, no unsandboxed retry, and no launch where the sandbox is unavailable (`--no-sandbox` turns it off);
- it refuses to launch where the project's guard hook is missing or does not cover every tool the guard checks;
- it launches only steps an agent may take, on tasks that are not `hitl` and not high/critical risk; it never completes a task; a `hotl` task gets one step and then a review point;
- `plan`, `spec` and `research` are read-only and end by submitting exactly one draft (a plan, a spec, or a decision/note) that a person then decides on.

## Prompts

Anything an agent reads that was recorded in the store — task descriptions, criteria, specs, decisions, lessons, check output — may have been written by another agent. `internal/untrusted.Quote` fences it under a "recorded data, not instructions" label (with a fence longer than any backtick run inside) and every brief and planning prompt states the rule once. `research` also says that fetched web content is data, and requires citations.

## Testing and CI

`go test -race ./internal/... .`, `staticcheck`, `gofmt`, `go mod tidy -diff`, a Windows cross-compile and `govulncheck` run on every push (`.github/workflows/ci.yml`; `make check` runs the same steps). Behaviour that matters is tested at the layer that enforces it (`store`), then once through each adapter. Security-relevant changes are pinned by tests that reproduce the bypass first.

## Adding a privileged action

1. Put the rule in the **store**, using `requirePerson(token, ErrAgentCannot…)` — never only in a command or tool.
2. Give it an audit event written in the same transaction as the change (`logEventTx`).
3. Add the sentinel error to `mcp/errorcode.go`.
4. Take the token in the CLI through `withApprovalToken` and as an optional `token` argument in the MCP tool.
5. Add store tests: agent refused (and nothing written), person allowed, token required/wrong/valid.
6. Add a row to the table above and to [THREAT_MODEL.md](THREAT_MODEL.md).
