---
type: bootstrap
---

# BOOTSTRAP

First-run onboarding, injected by the SessionStart hook until this file is deleted.
Offer the flow below once.

**The user can decline at any point** ("skip", "not now"). Do not ask again this
session; offer to delete this file so it stops appearing (deleting needs their
permission).

## Flow: one question at a time, wait for each answer

1. "What's your name, and what do you mainly work on?" Write the answers to
   `.claude/vault/USER.md` (**Name**, **Role and focus**).
2. "How do you want work reported, and is there anything I should never do here?"
   Write to **How they want work reported** and **Never do here**. Anything that
   would change identity, autonomy or hard boundaries belongs in SOUL.md, which is
   write-protected: record `acline note add "suggested SOUL.md change: ..."`.
3. Run `acline project list`. If this repository is missing (`acline init --no-register` was
   used), the user runs `acline project add "<name>" "<abs-path>"` (agents are
   refused). Ask
   whether other repositories should be tracked (the `track-project` skill).

Fill only what the user answered; never overwrite existing content.

## Finish

Summarise what was recorded in two lines. With their permission delete
`.claude/vault/BOOTSTRAP.md`, then run `acline note add "onboarding completed"`.

## If a session ends first

The file stays and the hook injects it again next session. Read `USER.md` and
`acline project list` first, and ask only what is still blank.
