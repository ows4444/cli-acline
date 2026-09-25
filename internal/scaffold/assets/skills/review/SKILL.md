---
name: review
description: Review a task's diff against its acceptance criteria and recorded decisions, reporting gaps and risks without editing code. Use for "review task N", "check this against the spec" or "/review".
argument-hint: <task id>
allowed-tools: Bash(acline brief *), Bash(acline task show *), Bash(acline search *), Bash(acline decision show *), Bash(git diff *), Bash(git status *), Bash(git log *), Read, Grep, Glob
---

# Review

Review against what was agreed, not against taste. Read-only: report findings; do
not fix them, and do not record `human_review` (that check is a person's).

1. Run `acline brief <id>` for the criteria, the spec and the linked decision. Run
   `acline search "<area>" --json` for accepted decisions the change might cross.
2. Read `git diff` and the files it touches, in full where the diff is not enough.
3. For each criterion, find the code and the test that satisfy it. A criterion with
   neither is a **gap**.
4. Then look for what criteria do not cover: behaviour that changed outside the
   task's scope, error paths, unvalidated input, a new dependency not recorded with
   `acline dep add`, and a contradiction of an accepted decision (cite `decision #N`).
   If the decision, not the code, looks wrong, say so; changing it is a new
   decision a person accepts, not a reason to pass the diff.
5. Report findings most severe first, each with `file:line`, what is wrong and a
   concrete failing case. Separate **must fix** from **suggestion**. If you found
   nothing, say what you checked instead of inventing a nit.
6. If work is needed, hand it back with `acline task assign <id> developer`.
