package config

import (
	"strings"
	"testing"
)

func TestExpandHome(t *testing.T) {
	expanded, err := ExpandHome("~/Kestrel", "/home/alice")
	if err != nil || expanded != "/home/alice/Kestrel" {
		t.Fatalf("got %q, %v", expanded, err)
	}
	if got, err := ExpandHome("~", "/home/alice"); err != nil || got != "/home/alice" {
		t.Fatalf("got %q, %v", got, err)
	}
	if got, err := ExpandHome("/abs/path", "/home/alice"); err != nil || got != "/abs/path" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := ExpandHome("~bob/secret", "/home/alice"); err == nil {
		t.Fatal("~user forms must be refused")
	}
}

func TestAgentArgsStayLiteral(t *testing.T) {
	cfg, err := Parse([]byte(`
agents:
  - id: custom
    name: Custom
    executable: /bin/custom-agent
    args: [";", "rm", "-rf", "/", "$(touch", "/tmp/pwned)", "--flag=$HOME", "a|b"]
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := cfg.Agents[0].Args
	want := []string{";", "rm", "-rf", "/", "$(touch", "/tmp/pwned)", "--flag=$HOME", "a|b"}
	if len(got) != len(want) {
		t.Fatalf("args = %q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("args[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestAgentRedaction(t *testing.T) {
	a := Agent{ID: "a", Name: "A", Executable: "/bin/agent", Args: []string{"--token", "secret"}}
	if redacted := a.RedactedCommand(); redacted != "/bin/agent [redacted]" {
		t.Fatalf("redacted = %q", redacted)
	}
	if a.RedactedCommand() == a.CommandLine() {
		t.Fatal("redacted form must differ from the literal argv display")
	}
	plain := Agent{ID: "a", Name: "A", Executable: "/bin/agent"}
	if plain.RedactedCommand() != "/bin/agent" {
		t.Fatalf("no-arg redaction = %q", plain.RedactedCommand())
	}
}

func TestCommandLineIsNeverExecutedThroughShell(t *testing.T) {
	cfg, err := Parse([]byte(`
agents:
  - id: custom
    name: Custom
    executable: /bin/echo
    args: ["$(touch /tmp/asd-should-not-execute)"]
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if strings.Contains(cfg.Agents[0].CommandLine(), "&&") {
		t.Fatal("join with spaces only for display")
	}
	if cfg.Agents[0].Args[0] != "$(touch /tmp/asd-should-not-execute)" {
		t.Fatal("argv must stay literal")
	}
}
