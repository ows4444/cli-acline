---
name: recall
description: Answer "what did we decide about X", "have we done this before" or "/recall" from acline's record of decisions, memory, specs and notes, citing each claim by kind and id.
argument-hint: <topic or question>
allowed-tools: Bash(acline search *), Bash(acline decision show *)
---

# Recall

Answer from what was recorded, not from general knowledge.

1. Run `acline search "<topic>" --json`. Add `--project <name>` when the question is
   about one project; omit it to search all. Each hit has `kind`, `ref_id`,
   `project_id`, `snippet` and `status`.
2. **Check each hit's status before citing it.** It is the row's status now, not when
   it was indexed:

   | kind | citable | not current |
   |---|---|---|
   | decision | accepted | proposed, rejected, deprecated, superseded |
   | memory | approved | pending, rejected, stale |
   | spec | approved, implemented | draft, superseded |
   | note | unpromoted | promoted: cite what it became |

   Prefer current hits. If the best hit is not current, cite it with its status
   ("per `decision #12` (rejected)"). Never drop the status.
3. Answer the question directly, citing kind and id so it can be checked
   (`acline decision show 12`).
4. Nothing found: retry with other keywords, and with `VOYAGE_API_KEY` set try
   `acline search "<topic>" --semantic --json` (decisions, memory and specs; not notes). Still nothing: say so.
   Never present general knowledge as recalled memory.
