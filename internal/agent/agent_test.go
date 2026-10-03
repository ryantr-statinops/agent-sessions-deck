package agent

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestAgentIDGrammar(t *testing.T) {
	valid := []ID{"opencode", "claude", "codex", "omp", "orca", "aider", "my-asd", "agent_2", "acme.tool"}
	for _, id := range valid {
		if err := id.Validate(); err != nil {
			t.Errorf("id %q was refused: %v", id, err)
		}
	}

	invalid := []struct {
		id     ID
		reason string
	}{
		{id: "", reason: "empty"},
		{id: "Opencode", reason: "uppercase"},
		{id: "-leading", reason: "leading dash"},
		{id: ".leading", reason: "leading dot"},
		{id: "with space", reason: "space"},
		{id: "with/slash", reason: "slash"},
		{id: "with\x00nul", reason: "nul byte"},
		{id: ID(strings.Repeat("a", maxIDLen+1)), reason: "too long"},
	}
	for _, tc := range invalid {
		err := tc.id.Validate()
		if err == nil {
			t.Errorf("id %q (%s) was accepted", tc.id, tc.reason)
			continue
		}
		var typed *InvalidIDError
		if !errors.As(err, &typed) {
			t.Errorf("id %q produced an untyped error %v", tc.id, err)
		}
	}
}

// TestCommandKeepsArgvLiteral proves the resolved command is an argv array and not
// a shell string: shell characters survive untouched and nothing is re-split.
func TestCommandKeepsArgvLiteral(t *testing.T) {
	args := []string{"--flag", "value with spaces", "$(whoami)", "a;b && c", "`id`", "*.go", ""}
	command, err := NewCommand("/usr/local/bin/opencode", args[:6])
	if err != nil {
		t.Fatalf("NewCommand: %v", err)
	}
	got := command.Args()
	want := []string{"--flag", "value with spaces", "$(whoami)", "a;b && c", "`id`", "*.go"}
	if !slices.Equal(got, want) {
		t.Fatalf("args = %q, want %q", got, want)
	}
	argv := command.Argv()
	if argv[0] != "/usr/local/bin/opencode" || len(argv) != 7 {
		t.Fatalf("argv = %q, want the executable followed by %d arguments", argv, len(want))
	}
	if command.ArgsLen() != len(want) {
		t.Fatalf("ArgsLen = %d, want %d", command.ArgsLen(), len(want))
	}
	if arg, ok := command.Arg(1); !ok || arg != "value with spaces" {
		t.Fatalf("Arg(1) = %q, %v", arg, ok)
	}
	if _, ok := command.Arg(99); ok {
		t.Fatalf("Arg(99) reported success")
	}

	// The command copies the caller's slice, so later mutation is invisible.
	args[0] = "--mutated"
	if command.Args()[0] != "--flag" {
		t.Fatalf("the command aliased the caller's slice")
	}
	// And every accessor hands back a copy.
	returned := command.Args()
	returned[0] = "--tampered"
	if command.Args()[0] != "--flag" {
		t.Fatalf("args were mutated through an accessor copy")
	}
	if !command.Equal(mustCommand(t, "/usr/local/bin/opencode", want...)) {
		t.Fatalf("equal commands compared unequal")
	}
	if command.Equal(mustCommand(t, "/usr/local/bin/opencode", "--other")) {
		t.Fatalf("different commands compared equal")
	}
}

func TestCommandValidation(t *testing.T) {
	cases := []struct {
		name       string
		executable string
		args       []string
	}{
		{name: "empty executable", executable: ""},
		{name: "nul in executable", executable: "/bin/\x00sh"},
		{name: "empty argument", executable: "/bin/sh", args: []string{""}},
		{name: "nul in argument", executable: "/bin/sh", args: []string{"a\x00b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewCommand(tc.executable, tc.args); err == nil {
				t.Fatalf("command was accepted")
			} else {
				var typed *InvalidCommandError
				if !errors.As(err, &typed) {
					t.Fatalf("error %v is not typed", err)
				}
			}
		})
	}

	if _, err := NewCommandFromArgv(nil); !errors.Is(err, ErrEmptyArgv) {
		t.Fatalf("empty argv error = %v, want %v", err, ErrEmptyArgv)
	}
	fromArgv, err := NewCommandFromArgv([]string{"/bin/sh", "-c", "echo hi"})
	if err != nil {
		t.Fatalf("NewCommandFromArgv: %v", err)
	}
	if fromArgv.Executable() != "/bin/sh" || fromArgv.ArgsLen() != 2 {
		t.Fatalf("argv split produced %+v", fromArgv)
	}
}

// TestCommandLaunchValidation requires an absolute executable, so a mutable PATH
// lookup can never change what is launched after validation.
func TestCommandLaunchValidation(t *testing.T) {
	relative := mustCommand(t, "opencode")
	if err := relative.Validate(); err != nil {
		t.Fatalf("a relative command failed basic validation: %v", err)
	}
	if err := relative.ValidateForLaunch(); err == nil {
		t.Fatalf("a relative executable passed launch validation")
	}
	absolute := mustCommand(t, "/usr/local/bin/opencode", "--flag")
	if err := absolute.ValidateForLaunch(); err != nil {
		t.Fatalf("an absolute command failed launch validation: %v", err)
	}
}

// TestCommandRedactionKeepsSecretsOutOfLogs follows the ADR rule that argv may
// carry secrets, so the display path redacts arguments.
func TestCommandRedactionKeepsSecretsOutOfLogs(t *testing.T) {
	command := mustCommand(t, "/usr/local/bin/opencode", "--token", "s3cret")
	if strings.Contains(command.Redacted(), "s3cret") {
		t.Fatalf("redacted form leaks an argument: %q", command.Redacted())
	}
	if !strings.HasPrefix(command.Redacted(), "/usr/local/bin/opencode") {
		t.Fatalf("redacted form dropped the executable: %q", command.Redacted())
	}
	if !strings.Contains(command.String(), "s3cret") {
		t.Fatalf("the literal form must stay verbatim for diagnostics: %q", command.String())
	}
	if got := (Command{}).Redacted(); got != "" {
		t.Fatalf("zero command redacted form = %q, want empty", got)
	}
	if !(Command{}).IsZero() || command.IsZero() {
		t.Fatalf("IsZero is wrong")
	}
}

// TestCapabilitiesSeparateCoreFromNative is the core-versus-provider decision.
func TestCapabilitiesSeparateCoreFromNative(t *testing.T) {
	generic := MustCapabilitiesOf(CapabilityLaunch, CapabilityInteractive)
	if !generic.Has(CapabilityLaunch) || !generic.Has(CapabilityInteractive) {
		t.Fatalf("core capabilities missing: %v", generic)
	}
	if generic.Native().Has(CapabilityLaunch) || generic.Native().Has(CapabilityInteractive) {
		t.Fatalf("native set contains a core capability: %v", generic.Native())
	}
	if generic.Core() != generic {
		t.Fatalf("core filter changed a core-only set: %v", generic.Core())
	}

	native := generic.With(CapabilityResume, CapabilitySessionList, CapabilitySessionLogs, CapabilityKill)
	if native.Core() != generic {
		t.Fatalf("core filter = %v, want %v", native.Core(), generic)
	}
	if got := native.Native(); !got.Has(CapabilityResume) || !got.Has(CapabilityKill) || got.Has(CapabilityLaunch) {
		t.Fatalf("native filter = %v", got)
	}

	if CapabilityLaunch.Class() != ClassCore || CapabilityInteractive.Class() != ClassCore {
		t.Fatalf("launch and interactive must be core capabilities")
	}
	for _, native := range []Capability{CapabilitySessionList, CapabilitySessionLogs, CapabilityResume, CapabilityKill} {
		if native.Class() != ClassNative || !native.IsNative() {
			t.Fatalf("%s must be a native capability", native)
		}
	}
	if unknown := Capability("teleport"); unknown.Class() != "" || unknown.Valid() {
		t.Fatalf("unknown capability classified as %q", unknown.Class())
	}

	if _, err := CapabilitiesOf(Capability("teleport")); err == nil {
		t.Fatalf("an unknown capability was accepted")
	} else {
		var typed *UnsupportedError
		if !errors.As(err, &typed) {
			t.Fatalf("error %v is not typed", err)
		}
	}
}

// TestUnsupportedCapabilityReturnsATypedError is the capability acceptance: an
// unsupported native operation fails with UNSUPPORTED, not a generic error.
func TestUnsupportedCapabilityReturnsATypedError(t *testing.T) {
	generic := MustCapabilitiesOf(CapabilityLaunch, CapabilityInteractive)
	if err := generic.Supports(CapabilityLaunch); err != nil {
		t.Fatalf("a supported capability was refused: %v", err)
	}

	err := generic.Supports(CapabilityResume)
	if err == nil {
		t.Fatalf("resume was reported as supported")
	}
	var typed *UnsupportedError
	if !errors.As(err, &typed) {
		t.Fatalf("error %v is not typed", err)
	}
	if typed.Capability != CapabilityResume {
		t.Fatalf("typed error names capability %q, want resume", typed.Capability)
	}
	if !strings.Contains(err.Error(), "native") {
		t.Fatalf("error %q does not say the capability is a native one", err)
	}

	// The set is immutable: asking about a capability never changes it.
	if generic.Has(CapabilityResume) {
		t.Fatalf("Supports mutated the capability set")
	}
	added := generic.With(CapabilityResume)
	if generic.Has(CapabilityResume) {
		t.Fatalf("With mutated the receiver")
	}
	if !added.Has(CapabilityResume) || !added.Without(CapabilityResume).Has(CapabilityLaunch) {
		t.Fatalf("set algebra is wrong")
	}
	if got := MustCapabilitiesOf(CapabilityKill, CapabilityLaunch).List(); !slices.Equal(got, []Capability{CapabilityLaunch, CapabilityKill}) {
		t.Fatalf("List order = %v", got)
	}
	if MustCapabilitiesOf(CapabilityKill, CapabilityLaunch).String() != "launch, kill" {
		t.Fatalf("String = %q", MustCapabilitiesOf(CapabilityKill, CapabilityLaunch).String())
	}
}

// stubProvider is a resolver-only provider: it proves the port needs no vendor
// behaviour, no PATH probe and no process.
type stubProvider struct {
	id           ID
	capabilities Capabilities
	err          error
}

func (p stubProvider) ID() ID { return p.id }

func (p stubProvider) Capabilities() Capabilities { return p.capabilities }

func (p stubProvider) Resolve(_ context.Context, req ResolveRequest) (Command, error) {
	if p.err != nil {
		return Command{}, p.err
	}
	argv := append([]string{p.id.String()}, req.ExtraArgs()...)
	return NewCommandFromArgv(argv)
}

func TestProviderPortResolvesLiteralArgv(t *testing.T) {
	provider := stubProvider{id: "generic", capabilities: MustCapabilitiesOf(CapabilityLaunch, CapabilityInteractive)}
	request, err := NewResolveRequest("/home/tester/kestrel", "kestrel run", []string{"--flag", "a b"})
	if err != nil {
		t.Fatalf("NewResolveRequest: %v", err)
	}
	if request.WorkspaceDir() != "/home/tester/kestrel" || request.Name() != "kestrel run" {
		t.Fatalf("request lost its inputs: %+v", request)
	}
	extra := request.ExtraArgs()
	extra[0] = "--tampered"
	if request.ExtraArgs()[0] != "--flag" {
		t.Fatalf("request aliased the caller's slice")
	}

	command, err := provider.Resolve(context.Background(), request)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !command.Equal(mustCommand(t, "generic", "--flag", "a b")) {
		t.Fatalf("resolved command = %q", command.String())
	}

	failing := stubProvider{id: "generic", err: errors.New("provider refused to resolve")}
	if _, err := failing.Resolve(context.Background(), request); err == nil {
		t.Fatalf("a failing provider returned a command")
	}
}

func TestResolveRequestValidation(t *testing.T) {
	if _, err := NewResolveRequest("dir\x00", "name", nil); err == nil {
		t.Fatalf("a NUL byte in the workspace directory was accepted")
	}
	if _, err := NewResolveRequest("/dir", "na\x00me", nil); err == nil {
		t.Fatalf("a NUL byte in the session name was accepted")
	}
	if _, err := NewResolveRequest("/dir", "name", []string{"a\x00b"}); err == nil {
		t.Fatalf("a NUL byte in an extra argument was accepted")
	}
}

func TestMapRegistryIndexesProvidersByStableID(t *testing.T) {
	first := stubProvider{id: "claude"}
	second := stubProvider{id: "generic"}
	registry, err := NewMapRegistry(first, second)
	if err != nil {
		t.Fatalf("NewMapRegistry: %v", err)
	}
	if !slices.Equal(registry.IDs(), []ID{"claude", "generic"}) {
		t.Fatalf("ids = %v", registry.IDs())
	}
	if _, ok := registry.Lookup("claude"); !ok {
		t.Fatalf("claude is missing from the registry")
	}
	if _, ok := registry.Lookup("Codex"); ok {
		t.Fatalf("lookup is case insensitive, so a configured id can shadow a built-in one")
	}

	if _, err := NewMapRegistry(first, first); err == nil {
		t.Fatalf("a duplicate provider id was accepted")
	}
	if _, err := NewMapRegistry(stubProvider{id: "Bad ID"}); err == nil {
		t.Fatalf("an invalid provider id was accepted")
	}
	if _, err := NewMapRegistry(nil); err == nil {
		t.Fatalf("a nil provider was accepted")
	}
}

// NewErrorForTest keeps the failing-provider fixture honest without importing the
// session package, which would create an import cycle.
func NewErrorForTest() error { return errors.New("provider refused to resolve") }
func mustCommand(t *testing.T, executable string, args ...string) Command {
	t.Helper()
	command, err := NewCommand(executable, args)
	if err != nil {
		t.Fatalf("NewCommand(%q, %q): %v", executable, args, err)
	}
	return command
}
