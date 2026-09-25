---
name: ship
description: Prepare a finished task for human sign-off with a summary of criteria, checks, diff and risk. Use for "ready for review", "prepare this for approval" or "/ship".
argument-hint: <task id>
allowed-tools: Bash(acline task show *), Bash(acline history *), Bash(acline check run *), Bash(acline brief *), Bash(git diff *), Bash(git status *), Bash(git log *)
---

# Ship

You prepare the evidence. A person approves; you never run `acline approve`.

1. Run `acline task show <id>` and `acline history --task <id>`. Note the risk,
   autonomy and gate state.
2. Re-run `acline check run <id> --kind test` if any file changed since the last
   recorded check; a stale pass is not evidence.
3. Read `git diff` for the task's changes. Flag anything outside the task's scope,
   any new dependency, and any file the criteria never mention.
4. Write the summary, brief and factual:
   - **Criteria:** each one, whether it is checked (`[x]` in `acline task show`),
     and the test that proves it. An unchecked criterion is **missing**, not a
     detail: the gate only warns about it, so the approver must see it here.
   - **Checks:** kind, status, and whether acline ran it against the current code.
   - **Risk:** what could break, what you did not verify.
   - **Approval:** who must approve and with which command, if the gate needs one.
5. Say plainly when something is missing or failing. Do not soften it.
