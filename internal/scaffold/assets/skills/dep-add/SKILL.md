---
name: dep-add
description: Add a dependency safely: confirm it is real and needed, record it, and scan it. Use for "add package X", "install a library" or "/dep-add".
argument-hint: <ecosystem> <name> [task id]
allowed-tools: Bash(acline dep *), Bash(acline check run *), Bash(acline search *)
---

# Dep add

A dependency is a supply-chain decision. Never install or upgrade one without the
user's go-ahead, and never mark it verified yourself.

1. Check it is needed. Look for a standard-library or already-present way first, and
   run `acline search "<name>" --json` for a past decision about it.
2. Confirm the package is real: the exact registry name, publisher, recent activity
   and download count. A name that is close to a popular one is a typosquat until
   shown otherwise. Report what you found; do not guess.
3. Ask the user to approve the specific name and version.
4. Install it, then record it:
   `acline dep add <ecosystem> <name>@<version> --task <id>`. It stays unverified
   until a person confirms it.
5. Run `acline check run <id> --kind sca` and `--kind sast`. A `skipped` scan is not
   a pass; name the missing tool.
6. If the choice is durable (why this library over another), propose it with
   `acline decision add`; a person accepts it.
