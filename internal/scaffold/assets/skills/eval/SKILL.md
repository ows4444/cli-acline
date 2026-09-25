---
name: eval
description: Measure an eval suite and record the result as evidence for an autonomy change a person makes. Use for "run the evals", "can this go to auto", "how accurate is the agent on X" or "/eval".
argument-hint: <suite name> [task id]
allowed-tools: Bash(acline eval *), Bash(acline task show *), Bash(acline brief *)
---

# Eval

An eval is evidence, not permission. `auto` autonomy is a person's call; you
measure and report.

1. Name the suite and the command that measures it: a test suite, benchmark or
   scripted replay whose output counts passes and cases. No such command: say so
   and stop. A pass rate you estimate is not an eval.
2. Run it against the code as it is now. Count passes and total from the output,
   not from memory.
3. Record exactly what it returned:
   `acline eval record --suite <suite> --pass-rate <0..1> --sample-size <n> --task <id> --note "<command run, what it covers>"`.
4. Run `acline eval list --suite <suite>` and report the trend, not only the latest
   number. Say what the suite does not cover.
5. Never run `acline task promote`. If the rate clears the bar, tell the user the
   command a person runs: `acline task promote <id> --to <hotl|auto> --suite <suite>`,
   and flag a small sample or a falling trend as weak evidence.
