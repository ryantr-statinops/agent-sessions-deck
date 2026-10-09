//go:build linux

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
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
	root.AddCommand(scanCommand(opts), listCommand(opts), inspectCommand(opts), newCommand(opts), openCommand(opts), detachCommand(opts), renameCommand(opts), restartCommand(opts), stopCommand(opts), killCommand(opts), deleteCommand(opts))
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
	var lifecycle, agentID, workspaceID string
	cmd := &cobra.Command{Use: "list", Short: "List sessions", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		client, err := readClient(cmd.Context(), opts)
		if err != nil {
			return err
		}
		req := app.ListRequest{AgentID: agent.ID(agentID), Workspace: workspace.ID(workspaceID)}
		if lifecycle != "" {
			req.Lifecycles = []session.Lifecycle{session.Lifecycle(lifecycle)}
		}
		result, err := client.List(cmd.Context(), req)
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(opts.Stdout, result)
		}
		for _, row := range result.Snapshot.Sessions {
			fmt.Fprintf(opts.Stdout, "%s\t%s\t%s\t%s\t%s\n", row.ID, row.Name, row.AgentID, row.Lifecycle, row.WorkspaceID)
		}
		return nil
	}}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "write stable JSON to stdout")
	cmd.Flags().StringVar(&lifecycle, "lifecycle", "", "filter by lifecycle")
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
		return writeJSON(opts.Stdout, result)
	}}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "write stable JSON to stdout")
	return cmd
}

func newCommand(opts Options) *cobra.Command {
	var name string
	cmd := &cobra.Command{Use: "new <agent> <workspace> [-- <argv...>]", Short: "Create a session and attach to its terminal", Args: cobra.MinimumNArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireTTY(opts); err != nil {
			return err
		}
		client, err := ownerClient(cmd.Context(), opts)
		if err != nil {
			return err
		}
		result, err := client.Create(cmd.Context(), app.CreateRequest{Agent: agent.ID(args[0]), WorkspacePath: args[1], WorkspaceSource: workspace.SourceExplicit, Name: name, ExtraArgs: args[2:]})
		if err != nil {
			return err
		}
		fmt.Fprintf(opts.Stderr, "created %s; attaching\n", result.Session.ID)
		return attach(cmd.Context(), client, string(result.Session.ID), "asd", opts)
	}}
	cmd.Flags().StringVar(&name, "name", "", "session display name")
	return cmd
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

func detachCommand(opts Options) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{Use: "detach <session-id>", Short: "Release the interactive attachment", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		client, err := ownerClient(cmd.Context(), opts)
		if err != nil {
			return err
		}
		result, err := client.Detach(cmd.Context(), app.DetachRequest{Ref: args[0]})
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(opts.Stdout, result)
		}
		fmt.Fprintf(opts.Stdout, "detached %s\n", result.Session.ID)
		return nil
	}}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "write stable JSON to stdout")
	return cmd
}

func renameCommand(opts Options) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{Use: "rename <session-id> <name>", Short: "Rename a session", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		client, err := ownerClient(cmd.Context(), opts)
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
		client, err := ownerClient(cmd.Context(), opts)
		if err != nil {
			return err
		}
		result, err := client.Restart(cmd.Context(), app.RestartRequest{Ref: args[0], Force: force})
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(opts.Stdout, result)
		}
		fmt.Fprintf(opts.Stdout, "restarted %s at attempt %d\n", result.Session.ID, result.Session.Generation)
		return nil
	}}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "write stable JSON to stdout")
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm restart")
	cmd.Flags().BoolVar(&force, "force", false, "allow interrupting a live attempt")
	return cmd
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

func deleteCommand(opts Options) *cobra.Command {
	var jsonOutput, yes bool
	cmd := &cobra.Command{Use: "delete <session-id>", Short: "Delete finished session metadata", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if !yes {
			return errors.New("delete requires --yes confirmation")
		}
		client, err := ownerClient(cmd.Context(), opts)
		if err != nil {
			return err
		}
		result, err := client.Delete(cmd.Context(), app.DeleteRequest{Ref: args[0]})
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(opts.Stdout, result)
		}
		fmt.Fprintf(opts.Stdout, "deleted %s\n", result.ID)
		return nil
	}}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "write stable JSON to stdout")
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm metadata deletion")
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
func requireTTY(opts Options) error {
	if !term.IsTerminal(opts.InputFD) || !term.IsTerminal(opts.OutputFD) {
		return errors.New("this command requires an interactive terminal on stdin and stdout")
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
