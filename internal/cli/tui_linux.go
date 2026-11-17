//go:build linux

package cli

import (
	"context"
	"errors"
	"io"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
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
			// No active owner running: bootstrap foreground owner
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

	for {
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

		finalModel, err := p.Run()
		if err != nil {
			return err
		}

		mFinal, ok := finalModel.(tui.Model)
		if !ok {
			break
		}

		attachID := mFinal.AttachTarget()
		if attachID == "" {
			break
		}

		// Option A: handover to attach loop
		attachErr := attach(ctx, client, attachID, "tui-client", opts)
		if attachErr != nil && !errors.Is(attachErr, io.EOF) && !errors.Is(attachErr, context.Canceled) {
			// Attach error: continue back to TUI loop
		}
	}

	return nil
}
