// Package redact scrubs live secret values out of free text before it's
// written to the store. It's a separate package (not part of internal/cmd's
// guard.go, where this logic originally lived) so both the CLI (internal/cmd)
// and the MCP server (internal/mcp) can call it without one importing the
// other — cmd owns cobra/global CLI state that mcp has no business depending
// on, and this scrubbing has no dependency on either.
package redact

import "regexp"

// secretValuePatterns match live secret *values* that may get pasted into
// free text (a acline log message or memory body) rather than referenced via
// a path or shell command — guard.go's secretPathPatterns/secretBashPatterns
// catch the latter case instead. Each pattern's first capture group (if
// any) is the literal secret to redact; where there's no group, the whole
// match is redacted. Ordered roughly most-specific-first so a private-key
// block isn't partially clobbered by a more generic pattern first.
var secretValuePatterns = []*regexp.Regexp{
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`\bacl_[0-9a-f]{64}\b`),                                              // acline's own approval token (store.newToken)
	regexp.MustCompile(`\bghp_[A-Za-z0-9]{36}\b`),                                           // GitHub personal access token
	regexp.MustCompile(`\bgh[oisur]_[A-Za-z0-9]{36}\b`),                                     // GitHub oauth/app/server/refresh tokens
	regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{22,}\b`),                                  // GitHub fine-grained PAT
	regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`),                                  // Slack token
	regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}`),                                           // OpenAI/Anthropic-style secret key (sk-ant-…, sk-proj-… contain hyphens/underscores)
	regexp.MustCompile(`\bpa-[A-Za-z0-9_-]{30,}`),                                           // Voyage AI key
	regexp.MustCompile(`\bnpm_[A-Za-z0-9]{36}\b`),                                           // npm access token
	regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{20,}`),                                        // GitLab personal access token
	regexp.MustCompile(`\bhf_[A-Za-z0-9]{30,}\b`),                                           // Hugging Face token
	regexp.MustCompile(`\bya29\.[A-Za-z0-9_-]{20,}`),                                        // Google OAuth access token
	regexp.MustCompile(`(?i)\bbearer\s+([A-Za-z0-9._~+/-]{20,}=*)`),                         // Authorization: Bearer <token> -- only the token is redacted
	regexp.MustCompile(`\b(?:sk|rk)_(?:live|test)_[A-Za-z0-9]{16,}\b`),                      // Stripe key
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),                                              // AWS access key id
	regexp.MustCompile(`\bAIza[0-9A-Za-z\-_]{35}\b`),                                        // Google API key
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`), // JWT
	// Connection-string-embedded credentials, e.g. postgres://user:pass@host/db
	// or redis://:pass@host -- a common leak vector that doesn't match the
	// key=value pattern below since there's no "password=" label at all.
	// Only the password (between ':' and '@') is captured/redacted; the
	// scheme, username, and host stay visible since they're not secret.
	regexp.MustCompile(`(?i)\b(?:postgres(?:ql)?|mysql|mongodb(?:\+srv)?|redis|rediss|amqp|amqps|mssql)://[^:/\s@]*:([^@\s]+)@`),
	regexp.MustCompile(`(?i)\b(?:api[_-]?key|secret|password|passwd|token|access[_-]?key)\b\s*[:=]\s*['"]?([A-Za-z0-9/+_\-.]{12,})['"]?`),
}

// Secrets scans text for values matching secretValuePatterns and replaces
// each one with "[REDACTED]", so a pasted live credential never reaches the
// database. Unlike guard.go's checkBashCommand/checkFilePathForSecrets,
// which block the action outright, this redacts and lets the write through
// — the caller still gets their note recorded, just without the secret in
// it.
func Secrets(text string) (redacted string, found bool) {
	out := text
	for _, p := range secretValuePatterns {
		out = p.ReplaceAllStringFunc(out, func(match string) string {
			found = true
			if loc := p.FindStringSubmatchIndex(match); len(loc) >= 4 && loc[2] != -1 {
				return match[:loc[2]] + "[REDACTED]" + match[loc[3]:]
			}
			return "[REDACTED]"
		})
	}
	return out, found
}

// Fields runs Secrets over each non-nil field in place and reports whether any
// of them contained a secret. Free-text fields (decision and spec bodies, like
// memory bodies and notes) are redacted this way by both the CLI and the MCP
// server; it lives here so there is one copy of that loop.
func Fields(fields ...*string) bool {
	found := false
	for _, f := range fields {
		if f == nil || *f == "" {
			continue
		}
		if r, ok := Secrets(*f); ok {
			*f = r
			found = true
		}
	}
	return found
}
