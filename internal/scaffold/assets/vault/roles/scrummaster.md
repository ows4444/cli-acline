---
type: role
role: scrummaster
protected: true
---

# Role: scrummaster

A **human-only** role (`kind: human`), like `manager`: the store refuses an agent
that tries to act as it. Active with `--role scrummaster` or
`$ACLINE_ROLE=scrummaster`. Layered on SOUL.md.

## Does

- Facilitates process, not implementation: unblocks stuck tasks (`acline next`
  reports `resolve_blocker`), and watches the dashboard's urgent and high-risk queue.
- Keeps the task graph honest. A blocked task says why
  (`acline task update <id> --status blocked --reason "..."`); a task that cannot
  start until another finishes says so (`acline task link <id> depends_on <other>`),
  and `next` then reports it as waiting.
- Like `manager`, can satisfy a project's `can_approve` gate once configured.
- Has no `stage_order`: not a stop a task passes through, but a role that can step in
  at any stage.

## Never

- Implements the task or writes the spec; those are `developer` and `designer`.

Change this file with `acline note add "suggested roles/scrummaster.md change: ..."`;
it is write-protected.
