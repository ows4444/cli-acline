---
name: track-project
description: Register another repository with acline so decisions, tasks and memory can be scoped to it. Use for "track this project", "add this repo" or "/track-project".
argument-hint: <project-name> <absolute-repo-path>
allowed-tools: Bash(acline project *)
---

# Track project

`acline init` already registers the repository it runs in and scaffolds `.claude/`.
Use this to register **another** repository.

1. Get the project name and the absolute path. If either is missing, ask; never
   guess a path.
2. Ask the person to run `acline project add "<name>" "<abs-path>"` in their own
   terminal. A registered path widens where agents may write, so acline refuses
   an agent. It reads and changes nothing inside that repository.
3. Scope a command with a flag (`acline task list --project <name>`), or run it from
   inside the repository, where the path resolves the project. To pin a subdirectory, run
   `acline project use <name>` from inside the project; from outside it refuses.
4. Confirm to the user. From now on decisions, tasks and memory for it go through
   `acline decision add`, `acline task add` and `acline memory add`, or a quick
   `acline note add` promoted later with `/reflect`.
