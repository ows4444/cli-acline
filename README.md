# acline

A local SDLC tracker for working with coding agents. Specs, tasks, decisions, verification results, approvals and an append-only audit trail live in one SQLite file, driven by the `acline` CLI, a terminal UI, an MCP server, and Claude Code hooks. It exists so that *"the agent said it's done"* is never the evidence: a task completes only when the store holds the checks and approvals its risk requires, and every step leaves a record you can verify.

- **One store** (`~/.acline/store.db`, override with `--db` / `$ACLINE_DB`), shared by every project you track.
- **A completion gate.** `acline task done` refuses without a recorded check, with a failing one, or (for high/critical risk or `hitl` autonomy) without a person's approval — and for high risk the passing evidence must come from a tool acline ran, against the code as it is now.
- **A tamper-evident trail.** Events are hash-chained; approvals and checks are sealed. `acline verify` and `acline doctor` say what was altered.
- **Agents that can propose but not decide.** Agents can draft specs and plans and record checks; a person (or the holder of an approval token) approves, accepts, and loosens risk.
- **A guard** for Claude Code: a `PreToolUse` hook that fails closed and refuses secret files, destructive commands and writes outside the project.

## Install

```sh
go install .                 # or: make install-cli   (builds and installs to ~/.local/bin)
acline version
```

Requires Go (see `go.mod`). The hooks need only the `acline` binary on the `PATH` Claude Code uses; there is no Python dependency.

**Platforms.** macOS and Linux are supported. On Windows the CLI and MCP server build, but the Claude Code hooks are not supported: the guard's `acline hook pre-tool-use || exit 2` needs a POSIX shell, `acline auth` reads the token from `/dev/tty`, and CI only cross-compiles for Windows. Use WSL there. `acline doctor` warns when run on Windows.

## Quickstart

```sh
cd your-project
acline init                 # registers the project, scaffolds .claude/ (hooks, skills, vault) and CLAUDE.md
acline auth init            # enable the approval token: run this yourself, in a terminal
acline doctor               # store, audit trail, token, guard hooks, claude binary

acline spec add "Export as CSV" --body "..."     # an agent may draft; a person approves
acline spec approve 1
acline task add "CSV writer" --spec 1 --risk medium
acline check run 1 --kind test                   # runs the tool and records its real result
acline task done 1                               # the gate decides
```

`acline dashboard` shows what needs attention; `acline next` says what should happen to a task and who should do it; `acline brief <id>` prints everything an agent needs for that step.

## Terminal UI

`acline tui` is the same store as a full-screen interface for a person: what is waiting on you, each task with its gate, and every approval in one place. It follows the store live, so a check an agent records in another terminal appears without a keypress.

```
acline · all projects · human/ana · token: OFF (identity is self-declared) · no active session
No approval token: anything that sets ACLINE_ACTOR_TYPE=human can approve. Run `acline auth init`.
[Tasks]

#1 ship the login fix                          │ Gate: 2 blocker(s)
status    todo                                 │   ✗ (agent) the code has changed since the passing checks ran
priority  high                                 │       (risk=high) — re-run: acline check run 1 --kind test
risk      high                                 │   ✗ (person) human approval required (risk=high, autonomy=hotl) —
autonomy  hotl                                 │       use: acline approve 1 --kind code_review
area      cli                                  │   ! test check passed for a different version of the code than the
by        human/ana                            │       current one — the code changed since; re-run: acline check run 1
created   2026-01-01T00:00:00Z                 │       --kind test
                                               │   ! 1 acceptance criterion/criteria still unchecked
Users bounce to / after login.                 │
                                               │ Newest check per kind
Acceptance criteria (1)                        │   test pass runner 2026-01-01T00:00:00Z by human/ana STALE: about
  [ ] #1 When a user logs in, the system       │       older code
      shall return them to the page they came  │
      from                                     │ Approvals (0)
                                               │   none
                                               │
                                               │ History, newest first
                                               │   2026-01-01T00:00:00Z criterion_added criterion #1 added: When a
                                               │       user logs in, the system shall return them to the page they came
                                               │       from
                                               │   2026-01-01T00:00:00Z check test: pass (runner)
                                               │   2026-01-01T00:00:00Z task_created task #1 created: ship the login
                                               │       fix (risk high, autonomy hotl)
[esc] back  [←/→] column  [↑/↓] scroll  [a] approve  [X] reject  [d] done  [D] force done  [c] criterion  [o] role  [z]
```

| Screen | Shows | Keys |
|---|---|---|
| 1 Dashboard | audit-trail status and everything waiting on a person | `↑/↓` `pgup/pgdn` scroll |
| 2 Tasks | every task, filtered and sorted | `enter` open, `/` search, `f` status, `R` risk, `a` area, `x` clear filters, `←/→` `s` sort |
| Task detail | fields, criteria, the gate with who can clear each blocker, checks, approvals, history | `a` approve, `X` reject, `d` done, `D` force done, `c` criterion, `o` role, `z` defer, `t` run a check, `R` risk, `U` autonomy, `esc` back |
| 3 Review | every task, spec, plan, decision and memory entry waiting on a person | `a` approve, `X` reject, `enter` open |
| 4 Specs | spec → plan → tasks, and decisions | `enter` `→/←` open/close, `v` compare versions, `e` edit a draft plan's item, `a` approve |
| 5 Memory | memory by state, and notes not yet promoted | `n` notes/memory, `f` filter, `a` approve, `X` reject, `F` forget, `t` reconfirm, `R` restore, `enter` promote a note |
| 6 Audit | chain verification, head, seals, events | `y` type, `w` actor, `t` task, `x` clear, `S` seal head, `c` check seal, `A` check anchor |
| 7 Orchestrator | one `orchestrate step` or `run`, with its output | `enter` start, `S` set the stop marker, `K` interrupt |

Everywhere: `1`-`7` or `tab`/`shift+tab` switch screens, `:` (or `ctrl+k`) lists every action, `p` switches project, `r` rereads, `?` shows the keys, `q` quits. `--project <name>` picks the project, `--accessible` prints plain append-only output for a screen reader, and colour follows `NO_COLOR`.

It is a person's interface. It refuses to start without a terminal or when the environment declares an agent, and the guard denies it in an agent's shell. With a token enabled it asks for the token itself, in a masked field, only when an action needs it. See [the architecture](docs/ARCHITECTURE.md#terminal-ui) for the rules it keeps.

## What to read next

| | |
|---|---|
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | Packages, data flow, the authority model, hooks, MCP, the orchestrator |
| [docs/THREAT_MODEL.md](docs/THREAT_MODEL.md) | What is protected, from whom, what is enforced and what is not |
| [SECURITY.md](SECURITY.md) | Reporting a vulnerability |
| [CHANGELOG.md](CHANGELOG.md) | Behaviour changes |
| [RESEARCH.md](RESEARCH.md) | Design of `orchestrate research` |
| `acline <command> --help` | Flags for every command |

## Trust in one paragraph

Until you run `acline auth init`, identity is whatever `ACLINE_ACTOR_TYPE` says, and an agent that sets it to `human` is a person as far as the store can tell. Enabling the token closes that: approvals, gate overrides, accepting or retiring decisions, approving specs and plans, loosening a task's risk or autonomy, and importing a snapshot all then need a secret the agent is never given. The guard is a best-effort denylist, not a sandbox. See the threat model for the full list of limits.

## Development

```sh
make check      # vet, race-detector tests, staticcheck (what CI runs), extension tests if checked out
make vuln       # govulncheck
make generate   # regenerate the VS Code extension's TypeScript types from the MCP structs
```

**Claude Code plugin.** `acline plugin export <dir>` writes acline's skills and agents as one versioned plugin (`claude --plugin-dir <dir>`), instead of the copies `acline init` puts in each repo. The guard hooks, deny rules and agent identity can't live in a plugin, so still run `acline init` in each project.

The VS Code extension ([ACLine AI](https://marketplace.visualstudio.com/items?itemName=ows4444.acline-ai)) lives in its own repo, `vscode-acline`. Clone it next to this repository (or point `EXT_DIR` at it) for `make generate`, `make ext-package` and `make install-ext`; without it `make check` skips the extension's tests, and the Go tests that compare the extension's tool list and generated types with the server skip too (they read `$ACLINE_EXT_DIR`, which make sets).

## License

MIT — see [LICENSE](LICENSE).
