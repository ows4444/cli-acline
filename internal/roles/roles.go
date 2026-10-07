// Package roles holds the one rule about acline's built-in roles that both the
// store and the guard enforce: which roles are read-only. The agent stubs
// internal/scaffold ships describe the same contract to Claude Code (their
// `tools:` line lists no Edit or Write), and scaffold's tests hold the two in
// agreement, so the store no longer reads the stubs to find out.
package roles

// readOnly are the built-in roles whose contract grants no file writes.
var readOnly = map[string]bool{"architect": true, "designer": true, "qa": true, "security": true}

// ReadOnly reports whether name is a built-in role whose contract grants no
// file writes. The guard denies such a role's writes, and the store refuses an
// agent that tries to end a session running as one. A project's own role, or a
// built-in role that may edit, is not read-only.
func ReadOnly(name string) bool { return readOnly[name] }

// ReadOnlyNames lists the read-only roles, for tests that check a description
// of them against this rule.
func ReadOnlyNames() []string {
	names := make([]string, 0, len(readOnly))
	for n := range readOnly {
		names = append(names, n)
	}
	return names
}
