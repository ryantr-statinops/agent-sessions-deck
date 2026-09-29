package app

import (
	"bytes"
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
			if code := Execute(tt.args, &stdout, &stderr); code != 0 {
				t.Fatalf("Execute(%v) exit code = %d, stderr = %q", tt.args, code, stderr.String())
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
	if code := Execute([]string{"resume"}, &stdout, &stderr); code != 1 {
		t.Fatalf("Execute unknown command exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("stderr %q does not report the unknown command", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}
}

func TestHelpDoesNotExposeUnimplementedCommands(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Execute([]string{"--help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("help exit code = %d, stderr = %q", code, stderr.String())
	}
	for _, command := range []string{"completion", "new", "open", "resume"} {
		if strings.Contains(stdout.String(), command) {
			t.Fatalf("help exposes unimplemented command %q: %q", command, stdout.String())
		}
	}
}
