---
name: retro
description: Close out a milestone: what shipped, what it cost to verify, what was decided and what should be remembered. Use for "retro", "wrap up the milestone", "what did we ship" or "/retro".
argument-hint: "[milestone id] (defaults to the active one)"
allowed-tools: Bash(acline roadmap *), Bash(acline task show *), Bash(acline task list *), Bash(acline history *), Bash(acline decision list *), Bash(acline feature list *), Bash(acline metrics *), Bash(acline memory review *), Bash(acline reflect)
---

# Retro

Report from the record, not from recollection. Read-only until step 5.

1. Pick the milestone: `acline roadmap show <id>`, or `acline roadmap list --status active`.
2. For its tasks, read `acline task show <id>` and `acline history --task <id>`:
   - **Shipped:** done tasks, and any whose gate was overridden (name who and why).
   - **Not shipped:** open, blocked or deferred tasks, and why.
   - **Rework:** tasks whose checks failed before passing, and how often.
3. Run `acline decision list --project <name>` and `acline feature list --project <name>`
   for decisions made and capabilities changed in this window. Flag a decision
   still `proposed` that the work already depends on.
4. Run `acline metrics` for the verification cost. It covers the whole store, not
   one project; say so rather than attributing it to the milestone.
5. Run `acline reflect` and `acline memory review`. Propose the lessons this
   milestone taught (a repeated failure is a `failure_pattern`); promote only what
   the user approves, through `/reflect`.
6. End with what waits on a person: decisions to accept, memory to approve, and
   whether the milestone can move to `done` (`acline roadmap update`, their call).
