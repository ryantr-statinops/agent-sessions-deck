//go:build linux

package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/ipc"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/owner"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
	"github.com/spf13/cobra"
)

// Options injects process streams and XDG paths into the command tree.
type Options struct {
	Paths    config.Paths
	Config   config.Config
	Stdin    io.Reader
	Stdout   io.Writer
	Stderr   io.Writer
	InputFD  uintptr
	OutputFD uintptr
}

// NewRoot builds the complete Stage 06 CLI command tree.
func NewRoot(opts Options) *cobra.Command {
	root := &cobra.Command{Use: "asd", Short: "Manage coding-agent sessions from a local terminal.", Args: cobra.NoArgs, SilenceUsage: true, SilenceErrors: true}
	root.SetIn(opts.Stdin)
	root.SetOut(opts.Stdout)
	root.SetErr(opts.Stderr)
	root.RunE = func(cmd *cobra.Command, _ []string) error {
		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		runtime, err := owner.Start(ctx, opts.Paths, opts.Config)
		if err != nil {
			return err
		}
		fmt.Fprintf(opts.Stderr, "foreground owner %s is running; use Ctrl-C to stop\n", runtimeID(runtime))
		return runtime.Serve(ctx)
	}
	root.AddCommand(scanCommand(opts), listCommand(opts), inspectCommand(opts), newCommand(opts), openCommand(opts), renameCommand(opts), restartCommand(opts), stopCommand(opts), killCommand(opts))
	return root
}

func runtimeID(r *owner.Runtime) string { return r.InstanceID() }

func scanCommand(opts Options) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{Use: "scan", Short: "List configured agent definitions and capabilities", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		client, err := readClient(cmd.Context(), opts)
		if err != nil {
			return err
		}
		result, err := client.Scan(cmd.Context(), app.ScanRequest{})
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(opts.Stdout, result)
		}
		for _, item := range result.Agents {
			fmt.Fprintf(opts.Stdout, "%s\tlaunch=%t\tinteractive=%t\n", item.ID, item.Launchable, item.Interactive)
		}
		return nil
	}}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "write stable JSON to stdout")
	return cmd
}

func listCommand(opts Options) *cobra.Command {
	var jsonOutput bool
	var status, agentID, workspaceID string
	cmd := &cobra.Command{Use: "list", Short: "List sessions", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		client, err := readClient(cmd.Context(), opts)
		if err != nil {
			return err
		}
		req := app.ListRequest{AgentID: agent.ID(agentID), Workspace: workspace.ID(workspaceID)}
		if status != "" {
			req.Lifecycles = []session.Lifecycle{session.Lifecycle(status)}
		}
		result, err := client.List(cmd.Context(), req)
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(opts.Stdout, result)
		}
		fmt.Fprintf(opts.Stdout, "authority=%s observed_at=%s revision=%d\n", result.Snapshot.Authority, result.Snapshot.ObservedAt.Format(time.RFC3339), result.Snapshot.Revision)
		if len(result.Snapshot.Sessions) == 0 {
			fmt.Fprintln(opts.Stdout, "No sessions found. Create one with asd new.")
			return nil
		}
		fmt.Fprintln(opts.Stdout, "ID\tNAME\tAGENT\tSTATUS\tWORKSPACE")
		for _, row := range result.Snapshot.Sessions {
			fmt.Fprintf(opts.Stdout, "%s\t%s\t%s\t%s\t%s\n", row.ID, row.Name, row.AgentID, row.Lifecycle, row.WorkspaceID)
		}
		return nil
	}}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "write stable JSON to stdout")
	cmd.Flags().StringVar(&status, "status", "", "filter by lifecycle")
	cmd.Flags().StringVar(&agentID, "agent", "", "filter by agent id")
	cmd.Flags().StringVar(&workspaceID, "workspace", "", "filter by workspace id")
	return cmd
}

func inspectCommand(opts Options) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{Use: "inspect <session-id>", Short: "Inspect one session", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		client, err := readClient(cmd.Context(), opts)
		if err != nil {
			return err
		}
		result, err := client.Get(cmd.Context(), app.GetRequest{Ref: args[0]})
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(opts.Stdout, result)
		}
		row := result.Session
		fmt.Fprintf(opts.Stdout, "id: %s\nname: %s\nagent: %s\nworkspace: %s\nauthority: %s\nobserved_at: %s\nrevision: %d\nlifecycle: %s\nattachment: %s\nactivity: %s\nattempt: %d\nidentity_verified: %t\npersisted: %t\n", row.ID, row.Name, row.AgentID, row.WorkspaceID, row.Authority, row.ObservedAt.Format(time.RFC3339), result.Revision, row.Lifecycle, row.Attachment, row.Activity, row.Generation, row.HasIdentity, result.Persisted)
		return nil
	}}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "write stable JSON to stdout")
	return cmd
}

func newCommand(opts Options) *cobra.Command {
	var name string
	var jsonOutput bool
	cmd := &cobra.Command{Use: "new [agent] [workspace] [-- <argv...>]", Short: "Create a session and attach to its terminal", Args: cobra.ArbitraryArgs, RunE: func(cmd *cobra.Command, args []string) error {
		tty := hasTTY(opts)
		client, err := ownerClient(cmd.Context(), opts)
		var foreground *owner.Runtime
		ownerCtx := cmd.Context()
		var stopOwner context.CancelFunc
		if err != nil {
			if !isOwnerUnavailable(err) {
				return err
			}
			if !tty {
				return session.NewError(session.CodeNotInteractive, "new", "creating without a foreground owner requires an interactive terminal", "start asd in a terminal, or attach to an existing owner")
			}
			ownerCtx, stopOwner = signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stopOwner()
			foreground, err = owner.Start(ownerCtx, opts.Paths, opts.Config)
			if err != nil {
				return err
			}
			defer foreground.Close()
			client = foreground.Client()
		}
		if !tty && !jsonOutput && len(args) < 2 {
			return session.NewError(session.CodeInvalidConfiguration, "new", "non-interactive creation requires both agent and workspace", "pass explicit agent and workspace arguments")
		}
		agentID, workspacePath, extraArgs, err := resolveNewInputs(ownerCtx, client, args, opts)
		if err != nil {
			return err
		}
		result, err := client.Create(ownerCtx, app.CreateRequest{Agent: agentID, WorkspacePath: workspacePath, WorkspaceSource: workspace.SourceExplicit, Name: name, ExtraArgs: extraArgs})
		if err != nil {
			return err
		}
		if jsonOutput {
			if err := writeJSON(opts.Stdout, result); err != nil {
				return err
			}
			if foreground != nil {
				return foreground.Serve(ownerCtx)
			}
			return nil
		}
		if !tty {
			fmt.Fprintln(opts.Stdout, result.Session.ID)
			return nil
		}
		fmt.Fprintf(opts.Stderr, "created %s; attaching\n", result.Session.ID)
		if err := attach(ownerCtx, client, string(result.Session.ID), "asd", opts); err != nil {
			return err
		}
		if foreground != nil {
			return foreground.Serve(ownerCtx)
		}
		return nil
	}}
	cmd.Flags().StringVar(&name, "name", "", "session display name")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "write the created session as JSON without attaching")
	return cmd
}

func resolveNewInputs(ctx context.Context, client app.Client, args []string, opts Options) (agent.ID, string, []string, error) {
	reader := bufio.NewReader(opts.Stdin)
	var selected agent.ID
	var workspacePath string
	var extra []string
	if len(args) > 0 {
		selected = agent.ID(args[0])
	}
	if len(args) > 1 {
		workspacePath = args[1]
	}
	if len(args) > 2 {
		extra = append([]string(nil), args[2:]...)
	}
	if selected == "" {
		scan, err := client.Scan(ctx, app.ScanRequest{})
		if err != nil {
			return "", "", nil, err
		}
		if len(scan.Agents) == 0 {
			return "", "", nil, session.NewError(session.CodeNotFound, "agent", "no configured agent is available", "add an agent under agents in config.yaml, then run asd scan")
		}
		if len(scan.Agents) == 1 {
			selected = scan.Agents[0].ID
		} else {
			var choices strings.Builder
			for i, a := range scan.Agents {
				fmt.Fprintf(&choices, "\n  %d) %s", i+1, a.ID)
			}
			fmt.Fprintf(opts.Stderr, "Choose an agent:%s\n> ", choices.String())
			line, err := reader.ReadString('\n')
			if err != nil && len(line) == 0 {
				return "", "", nil, err
			}
			n, parseErr := strconv.Atoi(strings.TrimSpace(line))
			if parseErr != nil || n < 1 || n > len(scan.Agents) {
				return "", "", nil, session.NewError(session.CodeInvalidConfiguration, "agent", "agent selection is not a listed choice", "rerun new and choose one of the listed agent numbers")
			}
			selected = scan.Agents[n-1].ID
		}
	}
	if workspacePath == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", "", nil, err
		}
		fmt.Fprintf(opts.Stderr, "Workspace path [%s]: ", cwd)
		line, err := reader.ReadString('\n')
		if err != nil && len(line) == 0 {
			return "", "", nil, err
		}
		workspacePath = strings.TrimSpace(line)
		if workspacePath == "" {
			workspacePath = cwd
		}
	}
	return selected, workspacePath, extra, nil
}

func isOwnerUnavailable(err error) bool {
	var typed *session.Error
	return errors.As(err, &typed) && typed.Code == session.CodeOwnerUnavailable
}

func openCommand(opts Options) *cobra.Command {
	var holder string
	cmd := &cobra.Command{Use: "open <session-id>", Short: "Attach to a running session", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireTTY(opts); err != nil {
			return err
		}
		client, err := ownerClient(cmd.Context(), opts)
		if err != nil {
			return err
		}
		return attach(cmd.Context(), client, args[0], holder, opts)
	}}
	cmd.Flags().StringVar(&holder, "name", "terminal", "name this interactive client")
	return cmd
}

func renameCommand(opts Options) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{Use: "rename <session-id> <name>", Short: "Rename a session", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		client, err := historicalRenameClient(cmd.Context(), opts, args[0])
		if err != nil {
			return err
		}
		result, err := client.Rename(cmd.Context(), app.RenameRequest{Ref: args[0], Name: args[1]})
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(opts.Stdout, result)
		}
		fmt.Fprintf(opts.Stdout, "renamed %s\n", result.Session.ID)
		return nil
	}}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "write stable JSON to stdout")
	return cmd
}

func restartCommand(opts Options) *cobra.Command {
	var jsonOutput, yes, force bool
	cmd := &cobra.Command{Use: "restart <session-id>", Short: "Start a new attempt for a session", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if !yes {
			return errors.New("restart requires --yes confirmation")
		}
		ctx := cmd.Context()
		client, err := ownerClient(ctx, opts)
		var foreground *owner.Runtime
		var stopOwner context.CancelFunc
		if err != nil {
			if !isOwnerUnavailable(err) {
				return err
			}
			if !hasTTY(opts) {
				return session.NewError(session.CodeNotInteractive, args[0], "restarting without a foreground owner requires a terminal", "run restart from a terminal so ASD can own the new attempt")
			}
			ctx, stopOwner = signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
			defer stopOwner()
			foreground, err = owner.Start(ctx, opts.Paths, opts.Config)
			if err != nil {
				return err
			}
			defer foreground.Close()
			client = foreground.Client()
		}
		result, err := client.Restart(ctx, app.RestartRequest{Ref: args[0], Force: force})
		if err != nil {
			return err
		}
		if jsonOutput {
			if err := writeJSON(opts.Stdout, result); err != nil {
				return err
			}
		} else {
			fmt.Fprintf(opts.Stdout, "restarted %s at attempt %d\n", result.Session.ID, result.Session.Generation)
		}
		if foreground != nil {
			return foreground.Serve(ctx)
		}
		return nil
	}}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "write stable JSON to stdout")
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm restart")
	cmd.Flags().BoolVar(&force, "force", false, "allow interrupting a live attempt")
	return cmd
}

func historicalRenameClient(ctx context.Context, opts Options, ref string) (app.Client, error) {
	client, err := ownerClient(ctx, opts)
	if err == nil {
		return client, nil
	}
	if !isOwnerUnavailable(err) {
		return nil, err
	}
	offline, err := owner.Offline(ctx, opts.Paths, opts.Config)
	if err != nil {
		return nil, err
	}
	reading, err := offline.Get(ctx, app.GetRequest{Ref: ref})
	if err != nil {
		return nil, err
	}
	if reading.Session.Lifecycle.ProcessMayExist() {
		return nil, session.NewError(session.CodeOwnerUnavailable, ref, "stored lifecycle may still have a process and cannot be safely reconciled offline", "start the foreground owner before editing this session")
	}
	return offline, nil
}

func stopCommand(opts Options) *cobra.Command {
	var jsonOutput, yes bool
	var grace time.Duration
	cmd := &cobra.Command{Use: "stop <session-id>", Short: "Gracefully stop a session", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if !yes {
			return errors.New("stop requires --yes confirmation")
		}
		client, err := ownerClient(cmd.Context(), opts)
		if err != nil {
			return err
		}
		result, err := client.Stop(cmd.Context(), app.StopRequest{Ref: args[0], Grace: grace})
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(opts.Stdout, result)
		}
		if result.TimedOut {
			return fmt.Errorf("stop timed out; session %s remains running; use kill --yes to escalate", result.Session.ID)
		}
		fmt.Fprintf(opts.Stdout, "stopped %s\n", result.Session.ID)
		return nil
	}}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "write stable JSON to stdout")
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm stop")
	cmd.Flags().DurationVar(&grace, "grace", 0, "graceful stop window")
	return cmd
}

func killCommand(opts Options) *cobra.Command {
	var jsonOutput, yes bool
	cmd := &cobra.Command{Use: "kill <session-id>", Short: "Force-kill a session", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if !yes {
			return errors.New("kill requires --yes confirmation")
		}
		client, err := ownerClient(cmd.Context(), opts)
		if err != nil {
			return err
		}
		result, err := client.Kill(cmd.Context(), app.KillRequest{Ref: args[0], Signal: session.SignalKill})
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(opts.Stdout, result)
		}
		fmt.Fprintf(opts.Stdout, "killed %s with %s\n", result.Session.ID, result.Signal)
		return nil
	}}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "write stable JSON to stdout")
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm force kill")
	return cmd
}

func readClient(ctx context.Context, opts Options) (app.Client, error) {
	client, err := ownerClient(ctx, opts)
	if err == nil {
		return client, nil
	}
	var domainErr *session.Error
	if !errors.As(err, &domainErr) || domainErr.Code != session.CodeOwnerUnavailable {
		return nil, err
	}
	return owner.Offline(ctx, opts.Paths, opts.Config)
}
func ownerClient(ctx context.Context, opts Options) (app.Client, error) {
	path, err := ipc.SocketPath(opts.Paths)
	if err != nil {
		return nil, err
	}
	client := ipc.NewClient(path)
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := client.Ping(probeCtx); err != nil {
		return nil, err
	}
	return client, nil
}
func writeJSON(w io.Writer, value any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(value)
}
func hasTTY(opts Options) bool {
	return term.IsTerminal(opts.InputFD) && term.IsTerminal(opts.OutputFD)
}
func requireTTY(opts Options) error {
	if !hasTTY(opts) {
		return session.NewError(session.CodeNotInteractive, "terminal", "this command requires an interactive terminal on stdin and stdout", "run it from a terminal")
	}
	return nil
}
func attach(ctx context.Context, client app.Client, ref, holder string, opts Options) error {
	opened, err := client.Open(ctx, app.OpenRequest{Ref: ref, Holder: holder})
	if err != nil {
		return err
	}
	terminalState, err := term.MakeRaw(opts.InputFD)
	if err != nil {
		_ = opened.Terminal.Close()
		return err
	}
	defer term.Restore(opts.InputFD, terminalState)
	defer opened.Terminal.Close()
	outDone := make(chan error, 1)
	inDone := make(chan error, 1)
	go func() { _, err := io.Copy(opts.Stdout, opened.Terminal); outDone <- err }()
	go func() { _, err := io.Copy(opened.Terminal, opts.Stdin); inDone <- err }()
	select {
	case err := <-outDone:
		_ = opened.Terminal.Close()
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
	case err := <-inDone:
		_ = opened.Terminal.Close()
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
	case <-ctx.Done():
		_ = opened.Terminal.Close()
		return ctx.Err()
	}
	return nil
}
