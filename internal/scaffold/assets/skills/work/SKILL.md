---
name: work
description: Implement one acline task end to end, from brief to a passing gate, leaving a verifiable trail. Use for "work on task N", "implement this task" or "/work".
argument-hint: "[task id] (defaults to the next task)"
allowed-tools: Bash(acline brief *), Bash(acline next *), Bash(acline session *), Bash(acline check run *), Bash(acline task show *), Bash(acline task criteria check *), Bash(acline task update *), Bash(acline task assign *), Bash(acline task done *), Bash(acline dep add *)
---

# Work

1. Run `acline brief <id>` (bare `acline brief` picks the next task). Read the
   criteria, failing checks and approved lessons. If the task has no criteria or its
   spec is unapproved, stop and say so; do not guess.
2. Run `acline session start --task <id>`. Work only inside this task's scope.
3. Implement the smallest change that meets the criteria. Write the test that proves
   each criterion, then the code. A new dependency is a stop: ask, then record it
   with `acline dep add <ecosystem> <name>@<version> --task <id>`. Stuck: run
   `acline task update <id> --status blocked --reason "..."` and stop.
4. Verify with the tools, after your last edit:
   `acline check run <id> --kind test`, then `--kind lint`, and `--kind sast` or
   `--kind sca` when the risk or the change calls for it. A `fail` or `skipped` is
   not a pass: fix it, or report why it cannot run.
5. For each criterion a passing test proves (`acline task show <id>` lists their
   ids), run `acline task criteria check <criterion-id>`. Say which stay unproven.
6. Hand off or complete. If `acline task show <id>` prints a `next (advisory)` role,
   the project runs a role pipeline: run `acline task assign <id> <that role>`;
   that role verifies and completes it. Otherwise run `acline task done <id>`. If
   the gate refuses because it needs approval, tell the user which person must
   approve. Never override the gate.
7. Either way, end the session with `acline session end -m "<what changed, what was checked>"`.
   Write any durable lesson as a `MEMORY_LOG:` line.
