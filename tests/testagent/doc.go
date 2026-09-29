//go:build linux

// Package testagent is the Stage 01 deterministic fake agent and its test
// harness.
//
// The fixture under fakeagent/ stands in for a coding-agent CLI during
// integration tests. It needs no installed agent, no network, no account and
// no credentials, and it never sleeps to make progress: every mode advances on
// an explicit control command, an explicit signal, or explicit terminal input.
//
// # Control channel
//
// A test starts the fixture with --mode=<mode> and passes a full-duplex
// AF_UNIX socket as child fd 3 (see the control subpackage). The fixture writes
// newline-delimited JSON events to fd 3 and reads commands from it. stdin,
// stdout and stderr stay bound to the pty, so terminal bytes never share a
// descriptor with control bytes and either stream can be asserted separately.
//
// # Cleanup
//
// The harness signals only pids it recorded at spawn time; it never matches on
// a process name. Every wait and every child reap is deadline bounded. After
// the suite, TestMain re-checks each recorded pid and pty path, so a leak
// fails the run rather than passing silently.
//
// # Scope
//
// The package is a test fixture only. It contains no session, runtime or
// product logic; Stage 05 owns the pty session runtime.
//
// This package is Linux only, matching the project's Linux-first scope.
package testagent
