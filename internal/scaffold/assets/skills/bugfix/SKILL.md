---
name: bugfix
description: Fix a bug reproduce-first, with a regression test and an audit record. Use for "fix this bug", "this is broken" or "/bugfix".
argument-hint: <symptom or task id>
allowed-tools: Bash(acline brief *), Bash(acline task add *), Bash(acline task criteria add *), Bash(acline session *), Bash(acline log *), Bash(acline check run *), Bash(acline task done *), Bash(acline search *)
---

# Bugfix

1. Run `acline search "<symptom>" --json`. A past pitfall or failure pattern may
   name the cause.
2. Reproduce it first, with a failing test or a command that shows the failure. If
   you cannot reproduce it, say so and stop; do not fix a guess.
3. Track it. Use the task you were given, or
   `acline task add "<bug title>" --area <area> --risk <r>`. Add the regression
   criterion: `acline task criteria add <id> "When <trigger>, the system shall <correct behaviour>"`.
4. Record it: `acline log "<symptom, root cause>" --type bug --task <id>`.
5. Run `acline session start --task <id>`. Find the root cause, not the nearest
   symptom, and make the smallest fix. Confirm the new test fails without the fix
   and passes with it.
6. Run `acline check run <id> --kind test` and `--kind lint` after your last edit.
   Then `acline task done <id>`; if it needs approval, stop and say who must give it.
7. If the cause was non-obvious, end with a `MEMORY_LOG:` line so `/reflect` can
   keep it as a pitfall.
