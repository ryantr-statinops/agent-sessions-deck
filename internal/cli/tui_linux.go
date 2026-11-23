//go:build linux

package cli

import (
	"context"
	"errors"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/daemon"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/owner"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/tui"
)

// runTUI launches the interactive TUI dashboard.
func runTUI(ctx context.Context, opts Options) error {
	client, err := ownerClient(ctx, opts)
	isOwner := false
	var runtime *owner.Runtime

	if err != nil {
		if errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED) {
			// In V2, try connecting to or launching agentd background daemon first
			if daemonErr := daemon.EnsureRunning(ctx, opts.Paths, opts.Config); daemonErr == nil {
				client, err = ownerClient(ctx, opts)
				if err != nil {
					return err
				}
				isOwner = false
			} else {
				// Fallback to foreground owner if agentd cannot be launched
				runtime, err = owner.Start(ctx, opts.Paths, opts.Config)
				if err != nil {
					return err
				}
				defer func() {
					_ = runtime.Close()
				}()

				go func() {
					_ = runtime.Serve(ctx)
				}()

				client = runtime.Client()
				isOwner = true
			}
		} else {
			return err
		}
	}

	var recent []string
	for _, ws := range opts.Config.Workspaces {
		if ws.Path != "" {
			recent = append(recent, ws.Path)
		}
	}
	if len(recent) == 0 {
		recent = []string{"."}
	}

	m := tui.NewModel(tui.Options{
		Client:           client,
		Paths:            opts.Paths,
		Config:           opts.Config,
		IsOwner:          isOwner,
		RecentWorkspaces: recent,
	})

	p := tea.NewProgram(
		m,
		tea.WithInput(opts.Stdin),
		tea.WithOutput(opts.Stdout),
		tea.WithAltScreen(),
	)

	_, err = p.Run()
	return err
}
