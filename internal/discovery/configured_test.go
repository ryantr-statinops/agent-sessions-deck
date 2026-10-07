package discovery

import (
	"strings"
	"testing"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
)

func TestValidateConfiguredAgents(t *testing.T) {
	builtins, err := agent.NewMapRegistry()
	if err != nil {
		t.Fatal(err)
	}
	_ = builtins
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
	ok := []config.Agent{{ID: "mine", Name: "mine", Executable: "/bin/a"}}
	if err := ValidateConfiguredAgents(ok, builtins); err != nil {
		t.Errorf("ok: %v", err)
	}
}
