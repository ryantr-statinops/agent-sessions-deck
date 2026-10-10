//go:build linux

package cli

import (
	"errors"
	"strings"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

// ErrorDocument is the stable JSON error envelope emitted with --json.
type ErrorDocument struct {
	Command    string             `json:"command"`
	Code       session.Code       `json:"code"`
	Subject    string             `json:"subject,omitempty"`
	Reason     string             `json:"reason"`
	Hint       string             `json:"hint"`
	Attempt    session.Generation `json:"attempt,omitempty"`
	HasAttempt bool               `json:"has_attempt,omitempty"`
}

// ErrorDocumentOf converts typed application errors without exposing wrapped causes.
func ErrorDocumentOf(command string, err error) ErrorDocument {
	var typed *session.Error
	if errors.As(err, &typed) {
		return ErrorDocument{Command: command, Code: typed.Code, Subject: typed.Subject, Reason: typed.Reason, Hint: typed.Hint, Attempt: typed.Attempt, HasAttempt: typed.HasAttempt}
	}
	code := session.CodeUnknown
	hint := "inspect the command help and retry when the cause is resolved"
	if ExitCode(err) == 2 {
		code = session.CodeInvalidConfiguration
		hint = "check the command arguments and run asd --help"
	}
	return ErrorDocument{Command: command, Code: code, Reason: err.Error(), Hint: hint}
}

// ExitCode maps contract errors to the V1 process status values.
func ExitCode(err error) int {
	var typed *session.Error
	if errors.As(err, &typed) {
		switch typed.Code {
		case session.CodeInvalidConfiguration:
			return 2
		case session.CodeOwnerUnavailable, session.CodeNotInteractive, session.CodeCorruptState:
			return 3
		case session.CodeConflict:
			if strings.Contains(strings.ToLower(typed.Reason), "ambiguous") {
				return 2
			}
			return 4
		case session.CodeStaleAttempt:
			return 4
		default:
			return 1
		}
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "unknown command") || strings.Contains(message, "unknown flag") || strings.Contains(message, "unknown shorthand flag") || strings.Contains(message, "accepts ") || strings.Contains(message, "requires --") || strings.Contains(message, "flag needs an argument") {
		return 2
	}
	return 1
}
