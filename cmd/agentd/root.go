package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/cli"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/daemon"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/ipc"
	"github.com/spf13/cobra"
)

var (
	buildVersion = "dev"
	buildCommit  = "unknown"
	buildDate    = "unknown"
)

func execute(args []string, stdout, stderr io.Writer) int {
	paths, err := config.ResolvePaths(os.Getenv)
	if err != nil {
		return reportError(args, "agentd", err, stdout, stderr, 2)
	}
	cfg, err := config.Load(paths.ConfigFile())
	if err != nil {
		return reportError(args, "agentd", err, stdout, stderr, 2)
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
	return "agentd"
}

func newRootCommand(paths config.Paths, cfg config.Config, stdout, stderr io.Writer) *cobra.Command {
	var foreground bool

	root := &cobra.Command{
		Use:   "agentd",
		Short: "Background session supervisor and PTY daemon for Agent Session Deck.",
		Long:  "agentd is the background daemon that owns agent sessions, PTY handles, and terminal emulation for ASD V2.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if foreground {
				return runForeground(cmd.Context(), paths, cfg, stderr)
			}
			return statusCommand(paths, stdout, stderr).RunE(cmd, args)
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.Flags().BoolVarP(&foreground, "foreground", "f", false, "run daemon in foreground")
	root.Version = buildVersion
	root.Annotations = map[string]string{"commit": buildCommit, "date": buildDate}
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetVersionTemplate("agentd version {{.Version}} (commit {{.Annotations.commit}}, built {{.Annotations.date}})\n")

	root.AddCommand(
		runCommand(paths, cfg, stderr),
		startCommand(paths, cfg, stdout, stderr),
		stopCommand(paths, stdout, stderr),
		statusCommand(paths, stdout, stderr),
	)

	return root
}

func runCommand(paths config.Paths, cfg config.Config, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "Run agentd in the foreground (recommended for systemd services)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runForeground(cmd.Context(), paths, cfg, stderr)
		},
	}
}

func runForeground(ctx context.Context, paths config.Paths, cfg config.Config, stderr io.Writer) error {
	sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()

	d, err := daemon.New(sigCtx, paths, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()

	socketPath, _ := ipc.SocketPath(paths)
	fmt.Fprintf(stderr, "agentd %s (PID %d) listening on %s\n", d.InstanceID(), os.Getpid(), socketPath)

	go func() {
		<-sigCtx.Done()
		fmt.Fprintln(stderr, "agentd received termination signal, shutting down active sessions...")
		report, shutdownErr := d.Shutdown(context.Background(), 5*time.Second)
		if shutdownErr != nil {
			fmt.Fprintf(stderr, "agentd shutdown error: %v\n", shutdownErr)
		} else {
			fmt.Fprintf(stderr, "agentd shutdown complete: %d/%d sessions stopped\n", report.StoppedSessions, report.TotalSessions)
		}
	}()

	return d.Serve(sigCtx)
}

func startCommand(paths config.Paths, cfg config.Config, stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start agentd in the background",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := daemon.EnsureRunning(cmd.Context(), paths, cfg); err != nil {
				return err
			}
			fmt.Fprintln(stdout, "agentd started successfully.")
			return nil
		},
	}
}

func stopCommand(paths config.Paths, stdout, stderr io.Writer) *cobra.Command {
	var force bool
	var timeout time.Duration

	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the running agentd daemon",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := daemon.Stop(cmd.Context(), paths, force, timeout); err != nil {
				return err
			}
			fmt.Fprintln(stdout, "agentd stopped successfully.")
			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "force stop even if active sessions exist")
	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Second, "graceful shutdown timeout")
	return cmd
}

func statusCommand(paths config.Paths, stdout, stderr io.Writer) *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show status and health of agentd",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			report, err := daemon.Status(cmd.Context(), paths)
			if err != nil {
				return err
			}

			if jsonOutput {
				enc := json.NewEncoder(stdout)
				enc.SetEscapeHTML(false)
				return enc.Encode(report)
			}

			fmt.Fprintf(stdout, "Daemon Status:   %s\n", report.Status)
			if report.PID > 0 {
				fmt.Fprintf(stdout, "PID:             %d\n", report.PID)
			}
			if report.InstanceID != "" {
				fmt.Fprintf(stdout, "Instance ID:     %s\n", report.InstanceID)
			}
			if report.SocketPath != "" {
				fmt.Fprintf(stdout, "Socket:          %s\n", report.SocketPath)
			}
			if report.Status == "RUNNING" {
				fmt.Fprintf(stdout, "Sessions Active: %d\n", report.ActiveSessions)
				fmt.Fprintf(stdout, "Sessions Total:  %d\n", report.TotalSessions)
			}
			if report.Message != "" {
				fmt.Fprintf(stdout, "Message:         %s\n", report.Message)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "format output as JSON")
	return cmd
}
