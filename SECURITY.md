# Security policy

## Reporting a vulnerability

Please report suspected vulnerabilities privately — do not open a public issue. Use GitHub's private vulnerability reporting for this repository ("Security" tab → "Report a vulnerability"), or contact the maintainers directly if that is unavailable. Include what you did, what you expected, what happened, and the acline version (`acline version`). We aim to acknowledge a report within a few days.

## What counts

acline's promise is that an agent cannot make work look verified or approved when it was not. Reports we especially want:

- a way for an agent (without the approval token) to approve, complete a gated task, loosen a task's risk or autonomy, accept, reject or supersede an accepted decision, promote autonomy on its own eval, approve a spec/plan, or record evidence a person is meant to supply;
- a way to alter the audit trail, or to add an approval/check, without `acline verify` or `acline doctor` noticing;
- a way to make the `PreToolUse` guard **allow** something it documents as blocked, or to make it fail *open*;
- a secret leaking into the store, a prompt, or a snapshot.

The guard is documented as a best-effort denylist, not a sandbox; a command that merely evades a pattern is a hardening request rather than a vulnerability — see [docs/THREAT_MODEL.md](docs/THREAT_MODEL.md) for what is and is not in scope.

## Supported versions

Only the latest release and `main`.
