---
name: spec
description: Turn a feature idea into a draft spec with EARS acceptance criteria and a first task. Use for "write a spec for X", "plan this feature" or "/spec".
argument-hint: <what to build>
allowed-tools: Bash(acline search *), Bash(acline spec add *), Bash(acline spec show *), Bash(acline task add *), Bash(acline task criteria add *)
---

# Spec

A spec is only real once a person approves it. Draft it; never approve it.

1. Run `acline search "<topic>" --json` first. If a current decision or spec already
   covers this, say so and stop, or build on it.
2. Ask the user only what the code cannot tell you: the goal, what is out of scope,
   and what "done" looks like. Keep it to a few questions.
3. Record the draft with `acline spec add "<title>" --body-file -`, the body on stdin:
   problem, scope, non-goals, open questions.
4. Add one task per independently verifiable slice:
   `acline task add "<title>" --spec <id> --area <area> --risk <r>`. Raise risk when
   the change touches auth, data loss, money or dependencies; never lower it to
   avoid approval.
5. Give each task criteria in EARS form, one behaviour each:
   `acline task criteria add <task-id> "When X, the system shall Y"`. A criterion
   that no check can prove is not a criterion; rewrite it.
6. Tell the user the spec is `draft` and waits on `acline spec approve <id>`, which
   only a person runs. Do not start implementing.
