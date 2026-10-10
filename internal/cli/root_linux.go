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
	"sort"
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
	"github.com/ryantr-statinops/agent-sessions-deck/internal/process"
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
	root := &cobra.Command{Use: "asd", Short: "Manage coding-agent sessions from a local terminal.", Long: "Without a subcommand, asd becomes the foreground owner and serves other terminal clients until Ctrl-C. This V1 foreground owner is not a daemon; the dashboard/TUI is Stage 07.", Example: "  asd\n  asd list --json", Args: cobra.NoArgs, SilenceUsage: true, SilenceErrors: true}
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

type scanReport struct {
	ObservedAt time.Time          `json:"observed_at"`
	Agents     []owner.AgentProbe `json:"agents"`
}

func scanCommand(opts Options) *cobra.Command {
	var jsonOutput bool
	var filters []string
	cmd := &cobra.Command{Use: "scan", Short: "Probe configured and built-in agent executables", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		probes, err := owner.ProbeAgents(cmd.Context(), opts.Config)
		if err != nil {
			return err
		}
		if len(filters) > 0 {
			selected := make(map[string]bool, len(filters))
			for _, id := range filters {
				selected[id] = true
			}
			filtered := probes[:0]
			for _, probe := range probes {
				if selected[string(probe.ID)] {
					filtered = append(filtered, probe)
					delete(selected, string(probe.ID))
				}
			}
			if len(selected) > 0 {
				return session.NewError(session.CodeNotFound, strings.Join(sortedKeys(selected), ","), "one or more requested agents were not found", "run asd scan without --agent to see the supported identifiers")
			}
			probes = filtered
		}
		report := scanReport{ObservedAt: time.Now().UTC(), Agents: probes}
		if jsonOutput {
			return writeJSON(opts.Stdout, report)
		}
		for _, probe := range probes {
			fmt.Fprintf(opts.Stdout, "%s\t%s\t%s\t%s\n", probe.ID, probe.Status, probe.Path, probe.Reason)
		}
		return nil
	}}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "write stable JSON to stdout")
	cmd.Flags().StringSliceVar(&filters, "agent", nil, "limit probes to these agent IDs")
	cmd.Example = "  asd scan\n  asd scan --agent codex --json"
	return cmd
}

func sortedKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
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
			return writeJSON(opts.Stdout, listDocumentOf(result))
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
	cmd.Example = "  asd list --status running\n  asd list --agent codex --workspace /work/project --json"
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
		ownerID := ""
		if ownerClient, ok := client.(interface{ OwnerInstanceID() string }); ok {
			ownerID = ownerClient.OwnerInstanceID()
		}
		report := inspectDocumentOf(cmd.Context(), result, ownerID)
		if jsonOutput {
			return writeJSON(opts.Stdout, report)
		}
		row := result.Session
		fmt.Fprintf(opts.Stdout, "id: %s\nname: %s\nagent: %s\nworkspace: %s\nauthority: %s\nobserved_at: %s\nrevision: %d\nlifecycle: %s\nattachment: %s\nio_availability: %s\nactivity: %s\nattempt: %d\nidentity_verified: %t\npersisted: %t\n", row.ID, row.Name, row.AgentID, row.WorkspaceID, row.Authority, row.ObservedAt.Format(time.RFC3339), result.Revision, row.Lifecycle, row.Attachment, report.IOAvailability, row.Activity, row.Generation, row.HasIdentity, result.Persisted)
		if !row.Command.IsZero() {
			fmt.Fprintf(opts.Stdout, "command: %s %s\n", row.Command.Executable(), strings.Join(row.Command.Args(), " "))
		}
		if row.HasIdentity {
			fmt.Fprintf(opts.Stdout, "identity: %s\n", row.Identity.Describe())
		}
		if reason := row.Reason.String(); reason != "" {
			fmt.Fprintf(opts.Stdout, "exit_reason: %s\n", reason)
		}
		if report.Git != nil {
			fmt.Fprintf(opts.Stdout, "git_root: %s\ngit_branch: %s\ngit_dirty: %t\ngit_changed_files: %d\n", report.Git.Root, report.Git.Branch, report.Git.Dirty, report.Git.ChangedFiles)
		}
		if report.WorkspaceError != "" {
			fmt.Fprintf(opts.Stderr, "workspace detail unavailable: %s\n", report.WorkspaceError)
		}
		return nil
	}}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "write stable JSON to stdout")
	cmd.Example = "  asd inspect 01JQASDWSDECK0001\n  asd inspect 01JQASDWSDECK0001 --json"
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
		if !tty && len(args) < 2 {
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
			if err := writeJSON(opts.Stdout, mutationDocumentOf("new", result.Session, result.Revision)); err != nil {
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
	cmd.Example = "  asd new codex /work/project\n  asd new codex /work/project --name review -- --help"
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
		client, err := ownerClient(cmd.Context(), opts)
		if err != nil {
			if errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED) {
				return session.NewError(session.CodeSessionIOFailed, args[0], "no foreground owner can provide this session's PTY", "start asd in a terminal, then run open again")
			}
			return err
		}
		if err := requireTTY(opts); err != nil {
			return err
		}
		return attach(cmd.Context(), client, args[0], holder, opts)
	}}
	cmd.Flags().StringVar(&holder, "name", "terminal", "name this interactive client")
	cmd.Long = "Attach to a live owner session. Press Ctrl+] to detach; the child session continues running."
	cmd.Example = "  asd open 01JQASDWSDECK0001\n  asd open 01JQASDWSDECK0001 --name terminal-b"
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
			return writeJSON(opts.Stdout, mutationDocumentOf("rename", result.Session, result.Revision))
		}
		fmt.Fprintf(opts.Stdout, "renamed %s\n", result.Session.ID)
		return nil
	}}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "write stable JSON to stdout")
	cmd.Example = "  asd rename 01JQASDWSDECK0001 \"design review\""
	return cmd
}

func restartCommand(opts Options) *cobra.Command {
	var jsonOutput, yes, force bool
	cmd := &cobra.Command{Use: "restart <session-id>", Short: "Start a new attempt for a session", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
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
		if force {
			current, err := client.Get(ctx, app.GetRequest{Ref: args[0]})
			if err != nil {
				return err
			}
			if current.Session.Lifecycle.ProcessMayExist() && !yes {
				if err := confirmAction(opts, args[0], "interrupt and restart"); err != nil {
					return err
				}
			}
		}
		result, err := client.Restart(ctx, app.RestartRequest{Ref: args[0], Force: force})
		if err != nil {
			return err
		}
		if jsonOutput {
			if err := writeJSON(opts.Stdout, restartDocumentOf(result)); err != nil {
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
	cmd.Example = "  asd restart 01JQASDWSDECK0001 --yes\n  asd restart 01JQASDWSDECK0001 --force --yes"
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
		if !reading.Session.HasIdentity {
			return nil, session.NewError(session.CodeOwnerUnavailable, ref, "stored active lifecycle has no process identity to reconcile", "start the foreground owner before editing this session")
		}
		observation := process.Observe(reading.Session.Identity)
		if _, err := offline.Report(ctx, app.ReportRequest{Ref: string(reading.Session.ID), Attempt: reading.Session.Generation, Kind: app.ReportReconciled, Liveness: observation}); err != nil {
			return nil, err
		}
	}
	return offline, nil
}

func stopCommand(opts Options) *cobra.Command {
	var jsonOutput bool
	var grace time.Duration
	cmd := &cobra.Command{Use: "stop <session-id>", Short: "Gracefully stop a session", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		client, err := ownerClient(cmd.Context(), opts)
		if err != nil {
			return err
		}
		result, err := client.Stop(cmd.Context(), app.StopRequest{Ref: args[0], Grace: grace})
		if err != nil {
			return err
		}
		if result.TimedOut {
			failure := session.NewError(session.CodeSessionIOFailed, string(result.Session.ID), "graceful stop timed out; the session remains running", "use kill --yes to escalate explicitly")
			if jsonOutput {
				return &JSONResultError{Document: stopDocumentOf(result), Cause: failure}
			}
			return failure
		}
		if jsonOutput {
			return writeJSON(opts.Stdout, stopDocumentOf(result))
		}
		fmt.Fprintf(opts.Stdout, "stopped %s\n", result.Session.ID)
		return nil
	}}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "write stable JSON to stdout")
	cmd.Flags().DurationVar(&grace, "grace", 0, "graceful stop window")
	cmd.Example = "  asd stop 01JQASDWSDECK0001\n  asd stop 01JQASDWSDECK0001 --grace 10s"
	return cmd
}

func killCommand(opts Options) *cobra.Command {
	var jsonOutput, yes bool
	cmd := &cobra.Command{Use: "kill <session-id>", Short: "Force-kill a session", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		client, err := ownerClient(cmd.Context(), opts)
		if err != nil {
			return err
		}
		if !yes {
			if err := confirmAction(opts, args[0], "kill"); err != nil {
				return err
			}
		}
		result, err := client.Kill(cmd.Context(), app.KillRequest{Ref: args[0], Signal: session.SignalKill})
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(opts.Stdout, killDocumentOf(result))
		}
		fmt.Fprintf(opts.Stdout, "killed %s with %s\n", result.Session.ID, result.Signal)
		return nil
	}}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "write stable JSON to stdout")
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm force kill")
	cmd.Example = "  asd kill 01JQASDWSDECK0001\n  asd kill 01JQASDWSDECK0001 --yes"
	return cmd
}

func confirmAction(opts Options, subject, action string) error {
	if !hasTTY(opts) {
		return session.NewError(session.CodeInvalidConfiguration, subject, action+" requires --yes when stdin is not a terminal", "rerun with --yes to confirm the destructive action")
	}
	fmt.Fprintf(opts.Stderr, "Confirm %s for %s? [y/N]: ", action, subject)
	line, err := bufio.NewReader(opts.Stdin).ReadString('\n')
	if err != nil && len(line) == 0 {
		return err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	if answer == "y" || answer == "yes" {
		return nil
	}
	return session.NewError(session.CodeConflict, subject, action+" was not confirmed", "rerun the command when ready")
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
	if width, height, sizeErr := term.GetSize(opts.InputFD); sizeErr == nil {
		_ = opened.Terminal.Resize(width, height)
	}
	resizeSignals := make(chan os.Signal, 1)
	signal.Notify(resizeSignals, syscall.SIGWINCH)
	defer signal.Stop(resizeSignals)
	resizeCtx, stopResize := context.WithCancel(ctx)
	defer stopResize()
	go func() {
		for {
			select {
			case <-resizeCtx.Done():
				return
			case <-resizeSignals:
				width, height, sizeErr := term.GetSize(opts.InputFD)
				if sizeErr == nil {
					_ = opened.Terminal.Resize(width, height)
				}
			}
		}
	}()
	outDone := make(chan error, 1)
	inDone := make(chan error, 1)
	go func() { _, err := io.Copy(opts.Stdout, opened.Terminal); outDone <- err }()
	go func() { inDone <- copyTerminalInput(opts.Stdin, opened.Terminal) }()
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
