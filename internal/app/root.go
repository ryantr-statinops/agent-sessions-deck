package app

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

var (
	buildVersion = "dev"
	buildCommit  = "unknown"
	buildDate    = "unknown"
)

// Execute runs the V1 root command and returns a process exit code.
func Execute(args []string, stdout, stderr io.Writer) int {
	root := newRootCommand(stdout, stderr)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	return 0
}

func newRootCommand(stdout, stderr io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use:           "asd",
		Short:         "Manage coding-agent sessions from a local terminal.",
		Args:          cobra.NoArgs,
		Version:       buildVersion,
		SilenceUsage:  true,
		SilenceErrors: true,
		Annotations: map[string]string{
			"commit": buildCommit,
			"date":   buildDate,
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetVersionTemplate("asd version {{.Version}} (commit {{.Annotations.commit}}, built {{.Annotations.date}})\n")
	return root
}
