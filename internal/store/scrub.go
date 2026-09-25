package store

import "acline/internal/redact"

// scrubText redacts live secret values from free text on its way into the
// store. Redaction used to be applied only by whichever adapter (CLI command
// or MCP tool) remembered to call it, which left whole write paths -- notes,
// tasks, check details, eval notes, session summaries -- unprotected and made
// every new adapter a chance to forget. Doing it here makes it an invariant of
// the store: no caller can persist a recognizable credential by omission.
//
// Adapters that want to *tell the user* a secret was removed (memory, spec and
// decision commands do) still call redact.Secrets themselves first; the second
// pass is a no-op on already-redacted text.
func scrubText(s string) string {
	if s == "" {
		return s
	}
	out, _ := redact.Secrets(s)
	return out
}
