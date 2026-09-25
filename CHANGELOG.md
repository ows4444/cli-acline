# Changelog

## Unreleased

The completion gate can no longer be bypassed by an agent without the approval token, and the audit trail notices more. **Several of these change behaviour**; the ones marked ⚠ can stop a workflow that worked before.

### Security

- ⚠ **`snapshot import`** into a store that already has data needs a person (or the token). Seeding an empty store from `acline init` still works.
- ⚠ **Risk and autonomy:** lowering a task's risk, loosening its autonomy, or creating an `auto` task needs a person/token. Every change is recorded (`risk_changed`, `autonomy_changed`). Eval-backed `PromoteAutonomy` still works for agents, on an eval a person recorded (see below).
- **Revising an approved spec** withdraws its approval (it becomes a draft) and keeps the replaced text (`acline spec show <id> --version N`).
- ⚠ **Agents cannot approve by naming a person** (`--by alice`); they need the token. `acline dashboard` and `acline init` now say when no token is enabled.
- ⚠ **`task done --force`** needs a person/token; an agent without it is refused.
- ⚠ **Retiring an accepted decision** (`decision reject`, `decision supersede`, and their MCP tools) needs a person/token. A proposed decision can still be rejected or superseded by anyone. The VS Code extension asks for the token when the decision is accepted.
- ⚠ **Eval-backed promotion by an agent** needs an eval a person recorded, and cannot lower `--threshold` below the default (90%). `eval record` takes the pass rate as typed, so an agent could otherwise record 100% and promote itself.
- ⚠ **`human_review` checks and dependency verification** are person/token-only.
- ⚠ **Roles:** agents cannot create a `can_approve` role or act as a human-only role (`manager`, `scrummaster`).
- ⚠ **High/critical risk needs a check acline ran** (`acline check run`, not `check record`) **against the current code.** A hand-typed pass, or one about code that has since changed, blocks completion; below high risk it only warns. Projects whose language has no default runner set one with `acline check runner set`.
- ⚠ **`check run --cmd`** is a person's choice: an agent is refused (or needs the token) *before* the command runs. It used to record a command such as `true` as runner evidence. MCP code `agent_cannot_choose_check_command`.
- ⚠ **A typed pass can no longer hide a failure acline ran.** A manual pass after a newer runner failure of the same kind blocks when an agent recorded it or the risk is high; a person's override below high risk warns.
- ⚠ **High/critical risk:** *every* runnable kind (`test/lint/sast/sca`) whose newest result is a pass must come from the runner. One runner pass of another kind no longer carries a typed one. A runner pass bound to no working tree no longer counts for a known tree, any stale runner pass blocks, and a tree that cannot be fingerprinted (too large, unreadable) blocks instead of silently disabling the staleness check.
- **`acline_check_run` (MCP)** runs the tool in the task's project directory (it used the server's working directory while fingerprinting the project's), so another checkout's tests can no longer stand in.
- **`task done`** re-reads the gate's inputs inside the completion transaction and re-evaluates if another process changed them in between.
- ⚠ **Registering a project path** (`project add`, `init`) needs a person/token: every registered path is inside the guard's write scope. `/`, `$HOME` and its ancestors are refused for everyone, and `acline doctor` fails on one already registered. Recorded as `project_added`. The `track-project` skill now asks the user to register.
- ⚠ **A read-only-role session** (security, qa, architect, designer) can no longer be ended by an agent (person/token; the orchestrator closes the sessions it launched), and the guard denies `session end/start` from inside one, which was the way to drop the role. Its write detection also covers git writes, formatters in write mode, `make`, `npm run`, archives and downloads.
- ⚠ **Guard write scope** is the project the session runs in, not every tracked project; set `ACLINE_GUARD_ALL_PROJECTS=1` in the hook's environment to widen it. Directories that resolve to no project keep the old scope.
- ⚠ **`.claude/settings.json` and `settings.local.json`** (which run the guard) are write-protected against the agent, like SOUL.md.
- **Secret reads through search globs** (`Grep` with `glob: "**/.env*"`, `grep --include`, `rg -g`) are denied, and `settings.json` gains `Read(**/.env*)`-style rules that Claude Code also applies to Grep and Glob, plus denies for `acline auth`, `snapshot import`, `project add`, `sqlite3` and the store directory.
- ⚠ **Sessions are per project.** A project's session (and its policy and role) applies only where the directory resolves to that project, and no longer blocks a session in another project. An unscoped session still applies everywhere. `acline doctor` reports sessions idle for over 12 hours; they are never ended automatically.
- **`acline plugin export <dir>`** writes a Claude Code plugin with acline's skills and agents (`--with-mcp` adds `acline mcp serve --as agent`). It carries no hooks or settings, which a plugin can't set safely, so `acline init` still installs the guard per project.
- **`acline verify --reseal`** (person/token): seals approvals/checks from before sealing and attests events from before hashing, so `verify` stops tolerating them and becomes strict; it refuses a store that doesn't verify. `verify` and `doctor` say when a store still has unattested legacy records.
- **Head seals:** `acline verify --seal-head` MACs the audit trail's head with the approval token; `verify --check-seal` fails if anything up to it was rewritten, even with the chain recomputed. A token rotation re-seals.
- ⚠ **Orchestrated steps run their shell commands in Claude Code's sandbox** (`claude --settings`): `~/.ssh`, `~/.aws` and other credential files are unreadable, writes are limited to the project, temp, the store and the Go caches, and the network to the Go module proxy. A platform without the sandbox (e.g. Linux without bubblewrap) refuses to launch; `--no-sandbox` opts out.
- ⚠ **`acline mcp serve` needs a declared identity:** `--as human|agent`, `ACLINE_ACTOR_TYPE` or `ACLINE_MODEL`. An undeclared server used to act as a person. `--as human` is refused where the environment declares an agent. The VS Code extension declares `human` when it inherited no identity; other MCP clients must set one.
- **`acline verify`** now fails on an approval/check that has no seal and was created after sealing began (a direct insert or an imported row), on an event whose hash was removed after hashing began, and on a changed event role (covered from the chain's new `hash_version` marker on).
- **Audit trail:** accepting, rejecting and superseding decisions, approving specs and reviewing memory write their event in the same transaction as the change, exactly once (the adapters used to add their own copy afterwards). Criteria added and checked, task links, deferrals, priority/area/type changes and runner changes are now recorded too.
- The approval token (`acl_…`) is redacted from anything written to the store. `snapshot export` and the audit `export` are written 0600, like the store.
- **`snapshot import`** treats projects, runners and custom roles as data: a store holding them is no longer "empty" for an unprivileged import.
- **Guard:** also blocks `git push --force`/`-f`/`+ref`, `git reset --hard`, `git clean -f`, `find -delete`, and reading `.kube/config`, `.docker/config.json`, `.git-credentials`, `.pgpass`, `*.tfstate`, `id_ecdsa`, `*.p8`, gcloud/azure credential directories. `chmod -R` is logged. A session running as `security`, `qa`, `architect` or `designer` cannot write files. The hook matcher now includes `WebFetch`, `WebSearch`, `Task` and MCP tools so a session policy can deny them.
- ⚠ **Orchestrator:** the agent gets an allow-listed environment (add more with `--pass-env NAME`), and `orchestrate` refuses to launch where the project's guard hook is missing or incomplete.
- Recorded text (task descriptions, criteria, specs, decisions, lessons, check output) is fenced as data in briefs and planning prompts.
- A `.acline-project` marker is honoured only inside the named project's registered path; project paths match through symlinks. `acline project use` refuses a directory outside the project.

### Hooks

- **New `PostToolUse` hook** (`acline hook post-tool-use`, added by `acline init`): records each file write and shell command made during a session as a `tool_used` event on the session and its task, so a reviewer can see what an agent actually did next to the gate's evidence. Reads are not recorded. It never blocks.
- **SessionStart context is bounded**: at most 40 open-work and capability rows (`context export --max-rows`), a byte cap per section, and task titles and features fenced as recorded data.
- **MEMORY_LOG capture reads only what the transcript appended** since the previous turn (it re-read the whole transcript every turn). `acline init` adds `.claude/vault/.state/` and `.acline-project` to a git checkout's `.gitignore`.

- ⚠ **The Python hooks are gone.** `settings.json` now runs `acline hook session-start|pre-compact|session-end` and `acline hook pre-tool-use || exit 2` (a missing binary or crash *blocks* the call). Re-run `acline init` in each project to migrate; it replaces the old commands and warns about the leftover `.claude/hooks/*.py`, which you can delete. `acline` must be on the `PATH` Claude Code uses; `python3` is no longer needed.
- ⚠ **New `Stop` hook** (`acline hook stop`, added by `acline init`): records each turn's `MEMORY_LOG:` lines, and blocks the end of a turn once when the active task's passing checks ran against code that has changed since and nothing has passed against the current code. It fails open and never blocks twice in a row.
- **`/work`** checks off each acceptance criterion a passing test proves, and hands the task to the next pipeline role (`next (advisory)` in `task show`) instead of completing it itself. **`/ship`** reports unchecked criteria as missing.
- **New skills:** `/eval` (measure a suite and record it; a person runs `task promote`), `/retro` (close out a milestone from the record), `/research` (the `orchestrate research` contract in-session: sourced claims, a proposed decision or a note). `/review` and `vault-structure` now say how a wrong accepted decision gets replaced.

### Added

- The shipped agent files, skills and vault text were rewritten and are now held to a rubric by tests: every `acline` command and flag they mention must exist, known-stale claims are banned, and each kind has a word budget. `SOUL.md` + `USER.md` (loaded every session) went from about 960 to about 465 words; `USER.md` is near-empty; `BOOTSTRAP.md` can be skipped; agent descriptions are written as delegation triggers and say exactly how far the read-only limit is enforced; the `reflect` skill no longer promises to set memory text it ignores.
- `settings.json` ships `permissions.deny` rules and a `$schema`; `init` merges them without touching your own.

- `acline doctor` — read-only health check (store, integrity, audit chain and seals, token, backups, hooks, project, `claude`).
- A backup (`<store>.pre-v<N>`) before any schema migration.
- `acline spec show --version`, `acline orchestrate --pass-env`, `make vuln`.
- `acline orchestrate` steps may also run the project's configured check runners directly (e.g. `npm test`), so non-Go projects can build and test; only a person sets runners.
- `acline_metrics` and `acline_plan_list` take `project` (tool contract version 2). `store.ComputeMetricsFor` scopes every metric the way the project event resources do.
- MCP tools `acline_task_update` (risk/autonomy/priority/area/type; loosening still needs a person or the token), `acline_check_runner_list`, `acline_spec_show` (with earlier versions) and `acline_decision_show`.
- Release pipeline: a `v*` tag builds linux/darwin/windows × amd64/arm64 archives (goreleaser), checksums signed keyless with cosign, an SBOM per archive, build-provenance attestations, and the VSIX, all on a **draft** GitHub release for a person to publish.
- CI: race detector, staticcheck, gofmt, `go mod tidy` check, Windows cross-compile, govulncheck, macOS; Dependabot.
- README, `docs/ARCHITECTURE.md`, `docs/THREAT_MODEL.md`, `SECURITY.md`.

### Fixed

- Text truncated on a byte boundary could leave invalid UTF-8 (auto-drafted memory, search snippets, briefs, agent summaries).
- The dashboard's pending/drafted/decaying memory counts ignored the project.
- A wall-clock test that failed under `-race` now widens its budget under the race detector.
- Schema version is now **13** (`spec_versions`; `checks.source`, `checks.tree_hash`; indexes on `events(type)` and `project_id`). Older acline binaries refuse a migrated store; the backup above is the way back. Concurrent first opens of an old store no longer race on the backup and the migrations.
- `check run` runners: quoted arguments are honoured (still no shell); `check runner set` keeps argument grouping. A run is bounded (its process group is killed on timeout, output is capped), and a cancelled run records nothing instead of a failure. `acline doctor` suggests runner commands for Node.js, Python and Rust projects.
