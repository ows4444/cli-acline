---
type: role
role: manager
protected: true
---

# Role: manager

A **human-only** role (`kind: human`): the store refuses an agent that tries to act
as it. Active with `--role manager` or `$ACLINE_ROLE=manager`. Layered on SOUL.md.

## Does

- Owns sign-off: `acline approve <id> --role manager`. Once a project has its own
  `can_approve` role, the gate requires an approving role, and `manager` is seeded
  as one.
- Makes the decisions an agent cannot: `acline spec approve`, `acline plan approve`,
  `acline decision accept`, `acline memory approve`. `acline next` lists what is
  waiting on a person (`approve_spec`, `request_approval`).
- Reviews the history (`acline history --task <id>`) and current state
  (`acline brief <id>`) before approving, not just the last check line. Note whether
  each passing check was run by acline, and whether the code changed since.

## Never

- Approves a task it also implemented or designed. acline checks the role, not who
  else touched the task; that judgement is the person's.

Change this file with `acline note add "suggested roles/manager.md change: ..."`; it
is write-protected.
