# acline

A local SDLC tracker for working with coding agents. Specs, tasks, decisions, verification results, approvals and an append-only audit trail live in one SQLite file, driven by the `acline` CLI, an MCP server, and Claude Code hooks. It exists so that *"the agent said it's done"* is never the evidence: a task completes only when the store holds the checks and approvals its risk requires, and every step leaves a record you can verify.

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
make check      # vet, race-detector tests, staticcheck, extension tests (what CI runs)
make vuln       # govulncheck
make generate   # regenerate the VS Code extension's TypeScript types from the MCP structs
```

**Claude Code plugin.** `acline plugin export <dir>` writes acline's skills and agents as one versioned plugin (`claude --plugin-dir <dir>`), instead of the copies `acline init` puts in each repo. The guard hooks, deny rules and agent identity can't live in a plugin, so still run `acline init` in each project.

The VS Code extension lives in `vscode-acline/` and is documented there.

## License

MIT — see [LICENSE](LICENSE).
