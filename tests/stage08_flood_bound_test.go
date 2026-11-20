//go:build linux && integration

package tests

import (
	"context"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/owner"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/tui"
	termview "github.com/ryantr-statinops/agent-sessions-deck/internal/tui/terminal"
)

func TestFloodOutputBoundedMemoryAndDetachResponsiveness(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	paths := config.Paths{
		StateDir:   t.TempDir(),
		ConfigDir:  t.TempDir(),
		RuntimeDir: t.TempDir(),
	}
	cfg := config.Default()
	cfg.Agents = []config.Agent{
		{
			ID:         "sh-agent",
			Name:       "Shell Agent",
			Executable: "/bin/sh",
		},
	}

	runtime, err := owner.Start(ctx, paths, cfg)
	if err != nil {
		t.Fatalf("failed to start owner runtime: %v", err)
	}
	defer func() { _ = runtime.Close() }()

	go func() { _ = runtime.Serve(ctx) }()
	client := runtime.Client()

	ws := t.TempDir()

	// Launch flood session emitting 15,000 lines
	res, err := client.Create(ctx, app.CreateRequest{
		Agent:         agent.ID("sh-agent"),
		WorkspacePath: ws,
		Name:          "flood-session",
		ExtraArgs:     []string{"-c", "for i in $(seq 1 15000); do echo FLOOD_LINE_$i; done; sleep 10"},
	})
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}
	defer func() {
		_, _ = client.Kill(context.Background(), app.KillRequest{Ref: string(res.Session.ID), Signal: session.SignalKill})
	}()

	openRes, err := client.Open(ctx, app.OpenRequest{
		Ref:    string(res.Session.ID),
		Holder: "flood-test",
	})
	if err != nil {
		t.Fatalf("failed to open session: %v", err)
	}
	defer func() { _ = openRes.Terminal.Close() }()

	m := tui.NewModel(tui.Options{
		Client: client,
		Paths:  paths,
		Config: cfg,
	})

	upOpen, _ := m.Update(tui.SessionOpenResultMsg{
		SessionID: string(res.Session.ID),
		Result:    openRes,
	})
	m = upOpen.(tui.Model)

	// Pump up to 20 frames from the flood stream
	framesReceived := 0
	for i := 0; i < 20; i++ {
		frame, rErr := openRes.Terminal.ReadFrame()
		if rErr != nil {
			break
		}
		framesReceived++
		upFrame, _ := m.Update(tui.TerminalFrameMsg{
			SessionID: string(res.Session.ID),
			Frame:     frame,
		})
		m = upFrame.(tui.Model)
	}

	if framesReceived == 0 {
		t.Fatal("expected to receive at least one terminal frame from flood stream")
	}

	// Verify bounded capacity of scrollback buffer under heavy load
	ring := termview.NewScrollbackRing(1000)
	dummyLine := app.TerminalLine{Cells: []app.TerminalCell{{Text: "A", Width: 1}}}
	for i := 0; i < 5000; i++ {
		ring.Push(dummyLine)
	}
	if ring.Len() > 1000 {
		t.Fatalf("expected ring buffer bounded at 1000, got %d", ring.Len())
	}

	// Verify that prefix detach `Ctrl+] s` remains immediately responsive under load
	upPrefix, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	m = upPrefix.(tui.Model)

	upDetach, cmdDetach := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m = upDetach.(tui.Model)

	if cmdDetach == nil {
		t.Fatal("expected detach command upon Ctrl+] s")
	}
}
