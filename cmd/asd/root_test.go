package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestExecuteHelpAndVersion(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantStdout string
	}{
		{
			name:       "help",
			args:       []string{"--help"},
			wantStdout: "Usage:\n  asd",
		},
		{
			name:       "version",
			args:       []string{"--version"},
			wantStdout: "asd version dev (commit unknown, built unknown)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := execute(tt.args, &stdout, &stderr); code != 0 {
				t.Fatalf("execute(%v) exit code = %d, stderr = %q", tt.args, code, stderr.String())
			}
			if !strings.Contains(stdout.String(), tt.wantStdout) {
				t.Fatalf("stdout %q does not contain %q", stdout.String(), tt.wantStdout)
			}
			if stderr.Len() != 0 {
				t.Fatalf("unexpected stderr: %q", stderr.String())
			}
		})
	}
}

func TestExecuteRejectsUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"resume"}, &stdout, &stderr); code != 2 {
		t.Fatalf("execute unknown command exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("stderr %q does not report the unknown command", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}
}

func TestJSONErrorUsesStableTypedEnvelopeAndUsageExit(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"resume", "--json"}, &stdout, &stderr); code != 2 {
		t.Fatalf("usage exit code = %d", code)
	}
	if stderr.Len() != 0 {
		t.Fatalf("JSON error wrote diagnostics to stderr: %q", stderr.String())
	}
	var result struct {
		Command string `json:"command"`
		Code    string `json:"code"`
		Reason  string `json:"reason"`
		Hint    string `json:"hint"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("error JSON %q: %v", stdout.String(), err)
	}
	if result.Command != "resume" || result.Code != "INVALID_CONFIGURATION" || result.Reason == "" || result.Hint == "" {
		t.Fatalf("JSON error envelope = %+v", result)
	}
}
