package main

import (
	"fmt"
	"io"
	"os"

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
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 2
	}
	cfg, err := config.Load(paths.ConfigFile())
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 2
	}
	root := newRootCommand(paths, cfg, stdout, stderr)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	return 0
}

func newRootCommand(paths config.Paths, cfg config.Config, stdout, stderr io.Writer) *cobra.Command {
	root := cli.NewRoot(cli.Options{Paths: paths, Config: cfg, Stdin: os.Stdin, Stdout: stdout, Stderr: stderr, InputFD: os.Stdin.Fd(), OutputFD: os.Stdout.Fd()})
	root.Version = buildVersion
	root.Annotations = map[string]string{"commit": buildCommit, "date": buildDate}
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetVersionTemplate("asd version {{.Version}} (commit {{.Annotations.commit}}, built {{.Annotations.date}})\n")
	return root
}
