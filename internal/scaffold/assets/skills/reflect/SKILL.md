---
name: reflect
description: Review captured notes and propose which are durable enough to become decisions or memory. Use for "reflect", "what should we save from this session" or "/reflect".
argument-hint: "[project name] (defaults to the current project)"
allowed-tools: Bash(acline reflect *)
---

# Reflect

Notes come from `MEMORY_LOG:` lines and `acline note add`. Nothing here becomes
trusted without a person: a promoted decision lands `proposed` and promoted memory
lands `pending`.

1. Run `acline reflect` (add `--project <name>`) to list unpromoted notes.
2. Sort each note: an architectural choice is a **decision**; a durable, non-obvious
   lesson, pitfall or constraint is **memory**; anything routine stays a note.
3. Show the user the suggestions grouped by kind, with the exact title and text you
   would record.
4. Promote each one they approve, one at a time, not as a batch:
   - `acline reflect promote <note-id> decision "<title>" --decision "..." --rationale "..."`
   - `acline reflect promote <note-id> memory --kind lesson`. The memory text is the
     note's own text, so if it needs rewording, add a new note first.
5. Tell them what now waits for a person: `acline decision accept <id>` and
   `acline memory approve <id>` (`acline memory review` lists what is pending).
6. If nothing is worth keeping, say so instead of inventing something.
