---
name: status
description: Summarize where the project stands and what is waiting on a person. Use for "what's the status", "where were we", "what needs me" or "/status".
argument-hint: "[project name]"
allowed-tools: Bash(acline dashboard *), Bash(acline next *), Bash(acline memory review *), Bash(acline history *)
---

# Status

Read state from acline, not from memory or files.

1. Run `acline dashboard` (add `--project <name>` to scope it).
2. Run `acline next --json` for the step an agent can take now.
3. Report in this order, skipping empty sections:
   - **In progress:** the active session and its task.
   - **Waiting on a person:** specs to approve, tasks needing approval, decisions
     to accept, and the pending memory count (`acline memory review` lists it).
   - **Ready to work:** the top open tasks by priority and risk.
   - **Blocked:** tasks with failing checks, and why.
4. End with one recommended next action and who takes it.
