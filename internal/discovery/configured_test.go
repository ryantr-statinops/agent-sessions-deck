package discovery

import (
	"strings"
	"testing"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/providers/generic"
)

func TestValidateConfiguredAgents(t *testing.T) {
	builtinProvider, err := generic.New("claude", "Claude", "/usr/bin/claude", nil)
	if err != nil {
		t.Fatal(err)
	}
	builtins, err := agent.NewMapRegistry(builtinProvider)
	if err != nil {
		t.Fatal(err)
	}
	dupID := []config.Agent{
		{ID: "x", Name: "one", Executable: "/bin/a"},
		{ID: "x", Name: "two", Executable: "/bin/b"},
	}
	if err := ValidateConfiguredAgents(dupID, builtins); err == nil || !strings.Contains(err.Error(), "duplicate agent id") {
		t.Errorf("dup ID: %v", err)
	}
	dupName := []config.Agent{
		{ID: "a", Name: "same", Executable: "/bin/a"},
		{ID: "b", Name: "same", Executable: "/bin/b"},
	}
	if err := ValidateConfiguredAgents(dupName, builtins); err == nil || !strings.Contains(err.Error(), "duplicate agent name") {
		t.Errorf("dup name: %v", err)
	}
	badID := []config.Agent{{ID: "BadCase", Name: "n", Executable: "/bin/a"}}
	if err := ValidateConfiguredAgents(badID, builtins); err == nil {
		t.Errorf("bad id should fail")
	}
	builtInCollision := []config.Agent{{ID: "claude", Name: "custom claude", Executable: "/bin/custom"}}
	if err := ValidateConfiguredAgents(builtInCollision, builtins); err == nil || !strings.Contains(err.Error(), "built-in provider") {
		t.Errorf("built-in ID collision: %v", err)
	}
	ok := []config.Agent{{ID: "mine", Name: "mine", Executable: "/bin/a"}}
	if err := ValidateConfiguredAgents(ok, builtins); err != nil {
		t.Errorf("ok: %v", err)
	}
}
