package session

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
)

// ErrFutureGeneration is the cause of a refusal for an observation that names an
// attempt the session has not reached. It is distinct from the benign T19 stale
// drop: a late callback from a replaced attempt is expected, while an
// observation for a future attempt means the caller mis-attributed it, and a
// caller must be able to surface that difference with errors.Is.
var ErrFutureGeneration = errors.New("the observation names an attempt the session has not reached")

// Code is the typed, user-visible error code from the CLI contract
// (docs/architecture/cli-contract.md) plus the codes Stage 02 added for the
// domain. Every user-visible failure carries one.
type Code string

const (
	CodeNotFound             Code = "NOT_FOUND"
	CodeNotRunning           Code = "NOT_RUNNING"
	CodeNotInteractive       Code = "NOT_INTERACTIVE"
	CodeUnsupported          Code = "UNSUPPORTED"
	CodePermissionDenied     Code = "PERMISSION_DENIED"
	CodeInvalidConfiguration Code = "INVALID_CONFIGURATION"
	CodeSessionIOFailed      Code = "SESSION_IO_FAILED"
	CodeLaunchFailed         Code = "LAUNCH_FAILED"
	CodeOwnerUnavailable     Code = "OWNER_UNAVAILABLE"
	CodeConflict             Code = "CONFLICT"
	CodeStaleAttempt         Code = "STALE_ATTEMPT"
	CodeCorruptState         Code = "CORRUPT_STATE"
	CodeUnknown              Code = "UNKNOWN"
)

// AllCodes returns every code in contract order.
func AllCodes() []Code {
	return []Code{
		CodeNotFound,
		CodeNotRunning,
		CodeNotInteractive,
		CodeUnsupported,
		CodePermissionDenied,
		CodeInvalidConfiguration,
		CodeSessionIOFailed,
		CodeLaunchFailed,
		CodeOwnerUnavailable,
		CodeConflict,
		CodeStaleAttempt,
		CodeCorruptState,
		CodeUnknown,
	}
}

// String returns the wire name of the code.
func (c Code) String() string { return string(c) }

// Valid reports whether the code is one of the defined ones.
func (c Code) Valid() bool {
	switch c {
	case CodeNotFound, CodeNotRunning, CodeNotInteractive, CodeUnsupported, CodePermissionDenied,
		CodeInvalidConfiguration, CodeSessionIOFailed, CodeLaunchFailed, CodeOwnerUnavailable,
		CodeConflict, CodeStaleAttempt, CodeCorruptState, CodeUnknown:
		return true
	default:
		return false
	}
}

// Error is the single typed domain error.
//
// It names the subject (session, agent or workspace), the reason and the next
// action, because the CLI contract forbids a bare "Error: failed" and requires a
// hint. The wrapped cause keeps the underlying failure inspectable with
// errors.Is and errors.As.
type Error struct {
	// Code is the typed code.
	Code Code
	// Subject names what the error is about, e.g. a session id.
	Subject string
	// Attempt is the attempt generation the error refers to, when relevant.
	Attempt Generation
	// HasAttempt records whether Attempt is meaningful.
	HasAttempt bool
	// Reason explains the cause in one clause.
	Reason string
	// Hint is the next action for the user.
	Hint string
	// Err is the wrapped cause, if any.
	Err error
}

// NewError builds a typed error.
func NewError(code Code, subject, reason, hint string) *Error {
	return &Error{Code: code, Subject: subject, Reason: reason, Hint: hint}
}

// WrapError builds a typed error around a cause.
func WrapError(code Code, subject, reason, hint string, err error) *Error {
	return &Error{Code: code, Subject: subject, Reason: reason, Hint: hint, Err: err}
}

// ForAttempt records the attempt generation on the error.
func (e *Error) ForAttempt(generation Generation) *Error {
	e.Attempt = generation
	e.HasAttempt = true
	return e
}

// Unwrap exposes the wrapped cause.
func (e *Error) Unwrap() error { return e.Err }

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString(string(e.Code))
	if e.Subject != "" {
		b.WriteString(": ")
		b.WriteString(e.Subject)
		if e.HasAttempt {
			fmt.Fprintf(&b, " (attempt %d)", e.Attempt)
		}
	}
	if e.Reason != "" {
		b.WriteString(": ")
		b.WriteString(e.Reason)
	}
	if e.Err != nil {
		b.WriteString(": ")
		b.WriteString(e.Err.Error())
	}
	if e.Hint != "" {
		b.WriteString(" (hint: ")
		b.WriteString(e.Hint)
		b.WriteString(")")
	}
	return b.String()
}

// CodeOf extracts the typed code from any error, translating the typed errors
// the agent and workspace packages raise into the shared taxonomy. An error
// without a code is UNKNOWN; callers must never print it without a reason.
func CodeOf(err error) Code {
	if err == nil {
		return ""
	}
	var typed *Error
	if errors.As(err, &typed) {
		return typed.Code
	}
	var unsupported *agent.UnsupportedError
	if errors.As(err, &unsupported) {
		return CodeUnsupported
	}
	var invalidCmd *agent.InvalidCommandError
	if errors.As(err, &invalidCmd) {
		return CodeInvalidConfiguration
	}
	var invalidID *agent.InvalidIDError
	if errors.As(err, &invalidID) {
		return CodeInvalidConfiguration
	}
	var invalidReq *agent.InvalidRequestError
	if errors.As(err, &invalidReq) {
		return CodeInvalidConfiguration
	}
	var invalidPath *workspace.InvalidPathError
	if errors.As(err, &invalidPath) {
		return CodeInvalidConfiguration
	}
	var invalidGit *workspace.InvalidGitError
	if errors.As(err, &invalidGit) {
		return CodeInvalidConfiguration
	}
	return CodeUnknown
}
