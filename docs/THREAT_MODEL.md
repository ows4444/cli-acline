# Threat model

This is what acline protects, from whom, how, and — just as important — what it does **not** protect. It is meant to be read before relying on the completion gate.

## What is protected

The claim acline makes: **an agent cannot make work look verified, reviewed or approved when it was not, and cannot do so silently.** Concretely, the integrity of:

1. **The gate's inputs** — approvals, checks (and where and against what they were run), risk and autonomy, spec text, accepted decisions.
2. **The audit trail** — events, and the seals on approvals and checks.
3. **What agents are told** — approved specs, decisions and lessons that feed briefs and context.
4. **Secrets** — credentials on the developer's machine, kept out of agent reach and out of the store.

## Who and what we defend against

| Actor | Capability assumed | In scope |
|---|---|---|
| **A well-meaning agent that is wrong** | Runs commands as the user; reports success it has not achieved | yes — the main case |
| **A prompt-injected agent** | Follows instructions planted in a web page, a file, a task description or a spec | yes, as a mitigation (fencing, least privilege), not a guarantee |
| **One agent steering another** | Writes task text, criteria, specs, notes that a later agent reads | yes — recorded text is fenced as data |
| **Someone with write access to the store file** | Edits SQLite directly | detect, not prevent (see below) |
| A hostile local user / malware running as the same user | Everything the user can do | **out of scope** |
| Several people sharing one store | — | **out of scope** (single-machine, single-user) |

## Trust boundaries

- **The store is the source of truth; adapters are not.** Every rule lives in `internal/store`, so the CLI, the MCP server and the orchestrator all get it.
- **A person is a human actor at a terminal.** Until `acline auth init`, "human" is whatever `ACLINE_ACTOR_TYPE` says; an agent that sets it is treated as a person. The guard blocks that in an agent's *shell* commands, but the environment variable itself is not protected from anything that starts `acline` directly. **Enable the token.** With it on, the privileged actions in [ARCHITECTURE.md](ARCHITECTURE.md#authority-who-may-do-what) need a secret the agent never holds.
- **The token** is 256 bits, stored only as a SHA-256 hash in the database (excluded from snapshots), entered on `/dev/tty` (or, in `acline tui`, in a masked field on its own screen), never read from the MCP server's environment, and stripped from the orchestrator's.
- **Claude Code's permission model is a separate layer.** The guard hook and the orchestrator's allow/deny lists add to it; they do not replace it.

## What is enforced

| Threat | Defence | Evidence |
|---|---|---|
| Code changes after a person approved it (the agent edits, re-runs the checks, and completes on the old approval) | each approval records the tree it was given for; the gate asks for a new one when the code differs | `store/approval_tree_test.go`, `cmd/integration_test.go` |
| Agent approves its own work, or names a person to do it | `RecordApproval` refuses an agent without the token; naming a person is not proof | `store/auth_test.go` |
| Agent completes a gated task with `--force` | `CompleteTask` needs a person or the token to override | `store/force_auth_test.go` |
| Agent downgrades a task out of the gate (risk, autonomy, `auto`) | loosening needs a person/token; every change is an event | `store/task_level_test.go` |
| Agent rewrites an approved spec | revising withdraws approval; old text kept; event | `store/spec_revise_test.go` |
| Agent retires what later work is checked against: rejects, supersedes or deprecates an accepted decision, or supersedes an approved spec | needs a person/token; superseded and deprecated are final | `store/retire_promote_test.go`, `store/supersede_deprecate_test.go` |
| Agent forges evidence: a `human_review`, a verified dependency, a snapshot import | each needs a person/token | `store/evidence_auth_test.go`, `snapshot_auth_test.go` |
| Agent retires an approved constraint or lesson (`memory forget`), or keeps one alive (`memory touch`) | needs a person/token; recorded as `memory_forgotten` / `memory_restored` / `memory_reconfirmed` in the same transaction | `store/memory_retire_test.go`, `mcp/errorcode_test.go` |
| A high-risk task completes with nothing verified (every result `skipped`) | high/critical risk needs a passing check acline ran, whatever the other results | `store/check_evidence_test.go` |
| Hand-typed "pass" satisfies a high-risk gate; a pass about old code counts | high risk needs every runnable kind's pass to be runner-sourced and about the current tree; a tree that cannot be fingerprinted, or a runner pass bound to no tree, blocks; a `test` check acline ran must have passed, so a lint or scan pass alone is not enough; at any risk an agent's typed pass never counts | `store/check_evidence_test.go` |
| A typed "pass" hides a failure acline observed | a manual pass cannot replace the newest runner failure of that kind (blocks for an agent or at high risk) | `store/check_evidence_test.go` |
| Agent picks the command a "runner" check runs (`check run --cmd true`) | an ad-hoc command needs a person/token, and a refused command never runs | `store/check_command_test.go`, `cmd/integration_test.go` |
| `check run` tests one directory and seals another's tree, or runs from a subfolder so only a subset of the tests runs and only a subset of the code is fingerprinted | the CLI and the MCP tools run checks in, and fingerprint, the task's registered project directory (`app.TaskDir`), from wherever they are invoked; `task gate`/`task done` compare against the same directory | `mcp/coverage_test.go`, `cmd/integration_test.go` (`TestCheckRunAndGateUseTheProjectRootFromASubfolder`), `app/tree_test.go` |
| A check or approval recorded while `task done` evaluates the gate | the gate's inputs are fingerprinted and re-read inside the completion transaction; a change re-evaluates | `store/complete_test.go` |
| Rows inserted outside acline | unsealed record after the watermark (after a reseal: any unsealed record) ⇒ `acline verify`/`doctor` fail | `store/seal_watermark_test.go`, `store/reseal_test.go` |
| Events rewritten and the chain recomputed | the head seal's HMAC (keyed by the approval token) no longer matches: `verify --check-seal` | `store/head_seal_test.go` |
| An event or seal edited | hash chain and seals break; an event's role and a removed hash (unhashed row after hashing began) are covered too | `store/seal_test.go`, `store/anchor_test.go` |
| A read-only role writes files | the guard reads the session role, or the subagent's name from the hook input (`agent_type`: qa, security, architect, designer), and denies writes, including common indirect writers (git, formatters, `make`, archives) | `cmd/guard_role_test.go` |
| A read-only role lifts itself by ending its session | an agent cannot end a read-only-role session (person/token); the guard also denies `session end/start` there | `store/session_role_test.go`, `cmd/guard_role_test.go` |
| Agent widens the guard's write scope by registering a project (`project add x /`) | registering a path needs a person/token; `/`, `$HOME` and its ancestors are refused for everyone; `doctor` fails on one already registered | `store/project_auth_test.go`, `cmd/doctor_test.go` |
| Agent writes into another tracked project | write scope is the session's own project (opt in to all with `ACLINE_GUARD_ALL_PROJECTS=1`) | `cmd/guard_test.go` |
| Agent removes the guard by editing `.claude/settings*.json` | those files are write-protected like SOUL.md | `cmd/guard_test.go` |
| Agent rewrites what later sessions are told or run: a skill, an agent definition, a slash command, `.mcp.json` (servers Claude Code starts) or a git hook (runs on the person's next commit) | write-protected for the file tools and common shell writes, plus `permissions.deny` rules | `cmd/guard_test.go` (`TestAgentInstructionFilesAreWriteProtected`, `TestBashWritesToAgentInstructionFilesAreBlocked`) |
| Agent reads secrets through a search glob (`Grep glob: "**/.env*"`, `rg -g .env*`) | a glob that selects secret files is denied; `Read(**/.env*)`-style deny rules cover broad searches | `cmd/guard_glob_test.go` |
| Another project's session policy or role judges this project's tool calls | the active session is scoped to the project the directory resolves to | `store/session_begin_test.go` |
| Guard cannot decide (crash, hang, missing binary, store unreadable) | denies; `\|\| exit 2` blocks | `cmd/hook_test.go` |
| Orchestrated agent inherits cloud/GitHub/npm credentials | environment allow-list | `orchestrate/env_guard_test.go` |
| Orchestrated test code reads `~/.ssh`/`~/.aws` or phones home | Bash sandbox: credential paths unreadable, writes and network allow-listed, fails closed (`--no-sandbox` to opt out) | `orchestrate/sandbox_test.go` |
| Orchestrator launched where no guard runs | preflight refuses | `orchestrate/env_guard_test.go` |
| Recorded text read as instructions | fenced as data with a rule in every prompt | `untrusted/`, `brief/`, `orchestrate/` tests |
| Agent drives the terminal UI, where every approval is one key away | `acline tui` needs a terminal on stdin and stdout and refuses an agent actor; the guard and the settings deny list refuse it in an agent's shell; the store refuses an agent each privileged action regardless | `cmd/tui_test.go`, `cmd/guard_sensitive_test.go`, `scaffold/scaffold_test.go`, `tui/safety_test.go` |
| The approval token is shown or left on the person's screen | the TUI asks for it in a masked field only when the store requires it, draws it on no frame, and a wrong one writes only the refusal's event | `tui/safety_test.go`, `tui/actions_test.go` |
| Text an agent recorded carries terminal escape sequences (retitle the window, write the clipboard, redraw over what is being approved) | the TUI passes stored text through a sanitiser before drawing it; tested for spec titles on the dashboard and the project name in the header, not for every field | `tui/dashboard_test.go` |
| A migration corrupts the shared store | pre-migration backup; newer store refused | `store/migration_backup_test.go` |

## Known limits (open items)

Being honest about these matters more than the table above.

**Identity and authority**
- Identity is self-declared until the token is enabled (see above). `acline mcp serve` refuses to start without a declared identity (`--as human|agent`, `ACLINE_ACTOR_TYPE` or `ACLINE_MODEL`), and `--as human` is refused in an environment that declares an agent. The VS Code extension declares `human` only when it inherited no identity. A client that sets `ACLINE_ACTOR_TYPE=human` itself is still believed without the token.
- A **person's** `snapshot import` is trusted: it can add rows that satisfy a gate. `acline verify` then fails on the unsealed record, so it is detectable, not prevented.

**Tamper evidence**
- Only approvals and checks are sealed. Task status, spec/decision/memory status and plan approvals are protected by the store's rules but **not** by a seal; someone editing the file can change them undetected.
- The hash chain itself is unkeyed SHA-256: anyone with write access to the file can rewrite events and recompute it, and plain `verify` will pass. A **head seal** (`verify --seal-head`, an HMAC of the head keyed by the approval token) makes a rewrite of anything up to the sealed event fail `verify --check-seal`. Checking a seal needs the token, and a seal cannot show that the newest seal and everything after it were not deleted. `verify --head` anchors cover that, and are only as good as where you keep them.
- A record forged before the first sealed insert is grandfathered in with older records, and events from before hashing (or hashed without their role) are tolerated rather than checked, **until a person runs `acline verify --reseal`**. That seals every unsealed record, attests the pre-marker events by digest, and makes `verify` strict from then on; `doctor` warns while a store still has unattested legacy. Resealing vouches for whatever is there, so it refuses a store that doesn't verify, but it can't tell a record forged before sealing began from a genuine old one.

**The guard**
- It is a **best-effort denylist, not a sandbox.** A program that writes as a side effect, an interpreter running a script file, or a command it cannot parse is not caught. Patterns are compiled in; a project cannot extend them.
- A session policy's path rules apply to file tools, not to Bash.
- A plain recursive `grep -r X .` in Bash can still read a secret file in the tree; only secret-selecting globs and the file tools are covered.
- Read-only roles are held to their contract for the file tools and common shell writes, not for every way a process can write.

**Scoping and leakage**
- A directory that resolves to **no project** runs unscoped, which shows *every* project's decisions, specs, memory and task titles — including in the SessionStart context. With two or more projects, register each checkout (`acline init`) or use `--project`. `acline doctor` warns.
- `context export` fences what anyone may record (task titles, features) as data, but shows accepted decisions, approved specs and approved memory as authoritative, because a person approved them. An instruction planted in an agent's draft becomes part of what every later agent is told once a person approves the draft without reading it.
- Semantic search, if you set `VOYAGE_API_KEY`, sends spec/decision/memory text to Voyage AI. There is no per-project opt-out.
- A memory entry you `forget` stays in the search index, flagged stale.

**The orchestrator**
- The sandbox covers shell commands only, and its `denyRead` list names known credential locations; a secret kept elsewhere under `$HOME` is readable. The Go module proxy is reachable, so a module fetch can carry data out in its path. It is not exercised by CI (it needs `claude`): the settings are tested, the sandbox itself is Claude Code's.
- The sandbox lets shell commands write the store's directory (`acline check run` records there), so code a step runs, such as a test the agent just wrote, can open the SQLite file and change it directly. `acline verify` catches a change to sealed records (approvals, checks) and to the event chain; a change to an unsealed status (see Tamper evidence) goes unnoticed.
- `Edit`/`Write` in the allow list are not path-scoped; the guard's write scope (which the preflight now requires to be installed) is what confines them.
- `research` can fetch from the open web with your decisions and approved memory in its prompt. Fetched content is treated as data and a person reads the draft, but there is no egress allow-list or fetch cap, and a "citation" is checked only textually.
- One session is active per project (an unscoped session applies everywhere). A crashed session keeps its policy in force and blocks the next one in its project until a person ends it; `acline doctor` reports sessions idle for over 12 hours.

**Verification evidence**
- A runner result proves that the project's runner exited 0 against the tree it records, **not** that the tests are meaningful. The runner's command is set by a person, but what it executes (test files, `Makefile` targets, `package.json` scripts) lives in the working tree, where an agent with `Edit`/`Write` can delete a test, add a skip, or rewrite a script. The tree hash shows *that* the code changed, not *what* changed. A reviewer approving high-risk work should read the diff to tests and build scripts, not just the check status.

**Other**
- `dependencies.verified` records that a person said so; nothing checks a registry.
- The advisory `next` router evaluates the gate without a working-tree fingerprint, so it may say "complete" for a task `task done` will refuse as stale.

## Reporting

See [SECURITY.md](../SECURITY.md). A way past anything in the "What is enforced" table is a vulnerability; anything in "Known limits" is a known, documented limit — but a report that shows one is worse than described is welcome.
