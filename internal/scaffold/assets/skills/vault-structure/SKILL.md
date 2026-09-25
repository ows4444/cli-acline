---
name: vault-structure
description: Decide where something should be recorded, in the acline database or in .claude/vault. Use whenever unsure whether to write a note, memory, decision, task or file.
allowed-tools: Read
---

# Where does this go?

| I have | It goes in | How |
|---|---|---|
| A choice that is expensive to reverse | a decision | `acline decision add` |
| A wrong accepted decision | a superseding decision | `acline decision add`; a person runs `acline decision supersede` |
| A durable, non-obvious lesson or constraint | memory (pending until approved) | `acline memory add` |
| A thought worth keeping, not yet judged | a note, promoted later by `/reflect` | `acline note add`, or a `MEMORY_LOG:` line |
| Intended behaviour | a spec (draft until approved) | `acline spec add` |
| Work to do | a task, with criteria | `acline task add`, `acline task criteria add` |
| A tool's result | a check | `acline check run` |
| A measured pass rate | an eval | `/eval` |
| A bug, blocker or commit | an event | `acline log --type bug` |
| Who the agent is, its boundaries | `.claude/vault/SOUL.md` | suggest via a note |
| One role's behaviour | `.claude/vault/roles/<name>.md` | suggest via a note |
| The person's preferences | `.claude/vault/USER.md` | edit it |

## Rules

1. Anything structured or decidable goes through `acline`, not markdown: that
   keeps it queryable and auditable.
2. `SOUL.md` and `roles/*.md` are write-protected: suggest a change with
   `acline note add "suggested SOUL.md change: ..."`.
3. `.claude/vault/` holds only `SOUL.md`, `USER.md`, role personas and
   `BOOTSTRAP.md`; add no folders unasked.
4. To find something, use `/recall`, not grep.
