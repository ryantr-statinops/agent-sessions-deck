package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/cli"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
	"github.com/spf13/cobra"
)

var (
	buildVersion = "dev"
	buildCommit  = "unknown"
	buildDate    = "unknown"
)

// execute runs the V1 root command and returns a process exit code.
func execute(args []string, stdout, stderr io.Writer) int {
	paths, err := config.ResolvePaths(os.Getenv)
	if err != nil {
		return reportError(args, "asd", err, stdout, stderr, 2)
	}
	cfg, err := config.Load(paths.ConfigFile())
	if err != nil {
		return reportError(args, "asd", err, stdout, stderr, 2)
	}
	root := newRootCommand(paths, cfg, stdout, stderr)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		return reportError(args, commandName(args), err, stdout, stderr, cli.ExitCode(err))
	}
	return 0
}

func reportError(args []string, command string, err error, stdout, stderr io.Writer, code int) int {
	if wantsJSON(args) {
		encoder := json.NewEncoder(stdout)
		encoder.SetEscapeHTML(false)
		if result, ok := cli.JSONResultOf(err); ok {
			_ = encoder.Encode(result)
		} else {
			_ = encoder.Encode(cli.ErrorDocumentOf(command, err))
		}
	} else {
		fmt.Fprintf(stderr, "Error: %v\n", err)
	}
	return code
}

func wantsJSON(args []string) bool {
	for _, arg := range args {
		if arg == "--json" || strings.HasPrefix(arg, "--json=") {
			return true
		}
	}
	return false
}

func commandName(args []string) string {
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			return arg
		}
	}
	return "asd"
}

func newRootCommand(paths config.Paths, cfg config.Config, stdout, stderr io.Writer) *cobra.Command {
	root := cli.NewRoot(cli.Options{Paths: paths, Config: cfg, Stdin: os.Stdin, Stdout: stdout, Stderr: stderr, InputFD: os.Stdin.Fd(), OutputFD: os.Stdout.Fd()})
	root.Version = buildVersion
	root.Annotations = map[string]string{"commit": buildCommit, "date": buildDate}
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetVersionTemplate("asd version {{.Version}} (commit {{.Annotations.commit}}, built {{.Annotations.date}})\n")
	return root
}
