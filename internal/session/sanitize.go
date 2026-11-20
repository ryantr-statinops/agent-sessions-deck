package session

import (
	"regexp"
)

var (
	// bearerRegex matches HTTP Authorization Bearer tokens.
	bearerRegex = regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9\-\._~\+\/]+=*`)

	// apiKeyRegex matches OpenAI, Anthropic, GitHub, and common vendor tokens.
	apiKeyRegex = regexp.MustCompile(`\b(sk-[A-Za-z0-9_-]{16,}|gh[pousr]_[A-Za-z0-9]{20,})\b`)

	// envSecretRegex matches KEY=value or SECRET=value in error strings.
	envSecretRegex = regexp.MustCompile(`\b([A-Za-z0-9_]*(?:TOKEN|KEY|SECRET|PASSWORD|CREDENTIAL)[A-Za-z0-9_]*)=([^\s]+)`)
)

// Sanitize scans a diagnostic string and redacts sensitive material (Bearer tokens,
// API keys, and secret environment assignments) to prevent accidental leakage in
// error logs, JSON envelopes, and terminal outputs.
func Sanitize(s string) string {
	if s == "" {
		return ""
	}
	s = bearerRegex.ReplaceAllString(s, "${1}[REDACTED]")
	s = apiKeyRegex.ReplaceAllString(s, "[REDACTED]")
	s = envSecretRegex.ReplaceAllString(s, "${1}=[REDACTED]")
	return s
}
