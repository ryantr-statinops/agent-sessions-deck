package generic

import (
	"context"
	"testing"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
)

// Literal argv survives metacharacters untouched: there is no shell between
// ASD and execve, so an argument like ';' runs as data, not as a payload.
func TestResolveKeepsArgvLiteral(t *testing.T) {
	p, err := New("mine", "Mine", "/usr/bin/true", []string{"-n", "; rm -rf ~", "$(id)", "`id`", "a;b|c"})
	if err != nil {
		t.Fatal(err)
	}
	req, err := agent.NewResolveRequest("/tmp/ws", "s", []string{"--flag", "x y"})
	if err != nil {
		t.Fatal(err)
	}
	cmd, err := p.Resolve(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.ValidateForLaunch(); err != nil {
		t.Fatalf("launch validation: %v", err)
	}
	want := []string{"-n", "; rm -rf ~", "$(id)", "`id`", "a;b|c", "--flag", "x y"}
	got := cmd.Args()
	if len(got) != len(want) {
		t.Fatalf("args = %#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("arg %d = %q, want %q", i, got[i], want[i])
		}
	}
	spec, err := p.LaunchSpec(req)
	if err != nil {
		t.Fatal(err)
	}
	if spec.WorkDir != "/tmp/ws" {
		t.Errorf("workdir = %q", spec.WorkDir)
	}
}

func TestNewValidates(t *testing.T) {
	if _, err := New("Bad ID", "n", "/bin/a", nil); err == nil {
		t.Error("bad id")
	}
	if _, err := New("ok", "n", "", nil); err == nil {
		t.Error("empty executable")
	}
	if _, err := New("ok", "n", "/bin/a", []string{""}); err == nil {
		t.Error("empty arg")
	}
	rel, _ := New("rel", "n", "true", nil)
	req, _ := agent.NewResolveRequest("/tmp", "s", nil)
	if _, err := rel.Resolve(context.Background(), req); err == nil {
		t.Error("relative executable must fail launch validation")
	}
}
