package session

import (
	"errors"
	"strings"
	"testing"
)

func TestSanitizeRedactsSensitiveMaterial(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		wantPass bool
		contains string
	}{
		{
			name:     "plain text passes through unchanged",
			input:    "failed to open terminal: connection refused",
			wantPass: true,
			contains: "connection refused",
		},
		{
			name:     "bearer token redacted",
			input:    "request failed: Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.xyz123== to https://api.vendor.com",
			wantPass: false,
			contains: "Bearer [REDACTED]",
		},
		{
			name:     "openai api key redacted",
			input:    "invalid response using key sk-abcdef1234567890abcdef123456",
			wantPass: false,
			contains: "[REDACTED]",
		},
		{
			name:     "github personal access token redacted",
			input:    "git auth failed: ghp_1234567890abcdef1234567890abcdef12",
			wantPass: false,
			contains: "[REDACTED]",
		},
		{
			name:     "secret env var assignment redacted",
			input:    "failed command: ANTHROPIC_API_KEY=secret_val_12345 ./run.sh",
			wantPass: false,
			contains: "ANTHROPIC_API_KEY=[REDACTED]",
		},
		{
			name:     "token env var assignment redacted",
			input:    "spawn failed with GITHUB_TOKEN=ghp_secretval ./tool",
			wantPass: false,
			contains: "GITHUB_TOKEN=[REDACTED]",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Sanitize(tc.input)
			if !strings.Contains(got, tc.contains) {
				t.Fatalf("Sanitize(%q) = %q, want containing %q", tc.input, got, tc.contains)
			}
			if !tc.wantPass && strings.Contains(got, "secret_val") {
				t.Fatalf("Sanitize leaked raw secret: %q", got)
			}
		})
	}
}

func TestNewErrorAndWrapErrorSanitizeInputs(t *testing.T) {
	err := NewError(CodeConflict, "session-1",
		"failed with sk-1234567890abcdef123456",
		"check Bearer secret_token_1234567890")

	if strings.Contains(err.Reason, "sk-") {
		t.Fatalf("NewError did not sanitize Reason: %s", err.Reason)
	}
	if strings.Contains(err.Hint, "secret_token") {
		t.Fatalf("NewError did not sanitize Hint: %s", err.Hint)
	}

	wrapped := WrapError(CodeUnknown, "session-2",
		"spawn failed",
		"retry",
		errors.New("child exited: API_SECRET=super_secret_value"))

	if strings.Contains(wrapped.Error(), "super_secret_value") {
		t.Fatalf("WrapError leaked secret in Error(): %s", wrapped.Error())
	}
}
