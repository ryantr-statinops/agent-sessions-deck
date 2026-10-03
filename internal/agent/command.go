package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
)

// Command is an immutable resolved command: one executable plus a literal argv
// list. It is never a shell string, so a workspace path, an agent flag or a
// user-supplied `-- <argv...>` entry reaches the process exactly as written and
// is never re-split, globbed or word-expanded (docs/architecture/adr/0003 and
// the glossary's "immutable resolved command").
//
// The fields are unexported so the only way to build a Command is through a
// validating constructor, and every accessor hands back a copy. A stored
// Attempt therefore cannot be mutated by a caller holding the slice it passed
// in.
type Command struct {
	executable string
	args       []string
}

// ErrEmptyArgv is returned when a resolved command is built from an empty argv.
var ErrEmptyArgv = errors.New("resolved command argv is empty")

// NewCommand validates and copies an executable and its arguments.
func NewCommand(executable string, args []string) (Command, error) {
	cmd := Command{executable: executable, args: slices.Clone(args)}
	if err := cmd.Validate(); err != nil {
		return Command{}, err
	}
	return cmd, nil
}

// NewCommandFromArgv treats argv[0] as the executable and the remainder as
// arguments.
func NewCommandFromArgv(argv []string) (Command, error) {
	if len(argv) == 0 {
		return Command{}, ErrEmptyArgv
	}
	return NewCommand(argv[0], argv[1:])
}

// IsZero reports the unset command, which is what an Attempt carries when
// command resolution never succeeded (transition T1).
func (c Command) IsZero() bool { return c.executable == "" && len(c.args) == 0 }

// Executable returns the absolute or relative executable path as resolved.
func (c Command) Executable() string { return c.executable }

// Args returns a copy of the argument list.
func (c Command) Args() []string { return slices.Clone(c.args) }

// ArgsLen returns the argument count without copying the slice.
func (c Command) ArgsLen() int { return len(c.args) }

// Arg returns the argument at i, if present.
func (c Command) Arg(i int) (string, bool) {
	if i < 0 || i >= len(c.args) {
		return "", false
	}
	return c.args[i], true
}

// Argv returns a copy of the full argv, executable first.
func (c Command) Argv() []string {
	argv := make([]string, 0, len(c.args)+1)
	if c.executable != "" {
		argv = append(argv, c.executable)
	}
	return append(argv, c.args...)
}

// Validate checks that the command can be resolved and later persisted: a
// non-empty executable, no NUL byte anywhere and no empty argument.
func (c Command) Validate() error {
	if c.executable == "" {
		return &InvalidCommandError{Problem: "executable is empty"}
	}
	if strings.ContainsRune(c.executable, 0) {
		return &InvalidCommandError{Executable: c.executable, Problem: "executable contains a NUL byte"}
	}
	for i, arg := range c.args {
		if arg == "" {
			return &InvalidCommandError{Executable: c.executable, Problem: fmt.Sprintf("argument %d is empty", i)}
		}
		if strings.ContainsRune(arg, 0) {
			return &InvalidCommandError{Executable: c.executable, Problem: fmt.Sprintf("argument %d contains a NUL byte", i)}
		}
	}
	return nil
}

// ValidateForLaunch adds the launch-time requirement that the executable is an
// absolute path, so the runtime never depends on a mutable PATH lookup after
// validation (Stage 05 launch transaction).
func (c Command) ValidateForLaunch() error {
	if err := c.Validate(); err != nil {
		return err
	}
	if !filepath.IsAbs(c.executable) {
		return &InvalidCommandError{Executable: c.executable, Problem: "executable must be an absolute path at launch"}
	}
	return nil
}

// Equal reports whether two resolved commands are identical, argument order
// included.
func (c Command) Equal(other Command) bool {
	return c.executable == other.executable && slices.Equal(c.args, other.args)
}

// String renders the literal argv for diagnostics. argv may carry secrets
// (ADR 0003), so command-line logging stays off by default and display paths
// prefer Redacted.
func (c Command) String() string { return strings.Join(c.Argv(), " ") }

// Redacted renders the executable without its arguments.
func (c Command) Redacted() string {
	if c.executable == "" {
		return ""
	}
	if len(c.args) == 0 {
		return c.executable
	}
	return c.executable + " [redacted]"
}

// commandJSON is the durable wire form of a Command.
//
// The resolved command is the only record of what ASD actually launched, so it
// has to survive a store round trip byte for byte: the executable and the literal
// argv, never a shell string that would be re-split on the way back in. An empty
// executable with no arguments is the zero command (transition T1), which is
// why the zero value round-trips too.
type commandJSON struct {
	Executable string   `json:"executable"`
	Args       []string `json:"args"`
}

// MarshalJSON renders the literal argv as the durable wire form.
func (c Command) MarshalJSON() ([]byte, error) {
	args := c.args
	if args == nil {
		args = []string{}
	}
	return json.Marshal(commandJSON{Executable: c.executable, Args: args})
}

// UnmarshalJSON restores a Command and validates it, so a stored argv that no
// longer satisfies the launch rules is refused instead of being handed to a
// runtime. Unknown fields are refused too: a schema change must not silently drop
// part of a command.
func (c *Command) UnmarshalJSON(data []byte) error {
	var wire commandJSON
	if err := decodeStrictJSON(data, &wire); err != nil {
		return err
	}
	if wire.Executable == "" && len(wire.Args) == 0 {
		*c = Command{}
		return nil
	}
	decoded, err := NewCommand(wire.Executable, wire.Args)
	if err != nil {
		return err
	}
	*c = decoded
	return nil
}

// DecodeCommand reads one resolved command from r and validates it. It is the
// store's read path: a malformed or unknown-shaped record fails loudly here
// instead of becoming a session with no command.
func DecodeCommand(r io.Reader) (Command, error) {
	var command Command
	if err := decodeStrictJSONFrom(r, &command); err != nil {
		return Command{}, err
	}
	return command, nil
}

// decodeStrictJSON decodes one JSON value, refusing unknown fields and trailing
// data so a schema drift can never be read as a valid record.
func decodeStrictJSON(data []byte, target any) error {
	return decodeStrictJSONFrom(bytes.NewReader(data), target)
}

func decodeStrictJSONFrom(r io.Reader, target any) error {
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.More() {
		return errors.New("trailing data after the JSON record")
	}
	return nil
}

// InvalidCommandError reports a command that failed validation.
// The session layer maps it to the LAUNCH_FAILED or INVALID_CONFIGURATION code
// depending on where it was raised.
type InvalidCommandError struct {
	Executable string
	Problem    string
}

func (e *InvalidCommandError) Error() string {
	if e.Executable == "" {
		return "invalid resolved command: " + e.Problem
	}
	return fmt.Sprintf("invalid resolved command %q: %s", e.Executable, e.Problem)
}
