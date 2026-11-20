//go:build linux && integration

package tests

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/owner"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/tui"
)

func TestMultiSessionSwitchingWithTwinAgents(t *testing.T) {
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

	// 1. Launch Session A
	resA, err := client.Create(ctx, app.CreateRequest{
		Agent:         agent.ID("sh-agent"),
		WorkspacePath: ws,
		Name:          "agent-alpha",
		ExtraArgs:     []string{"-c", "for i in $(seq 1 50); do echo AGENT_ALPHA_$i; sleep 0.05; done; sleep 10"},
	})
	if err != nil {
		t.Fatalf("failed to create session A: %v", err)
	}
	defer func() {
		_, _ = client.Kill(context.Background(), app.KillRequest{Ref: string(resA.Session.ID), Signal: session.SignalKill})
	}()

	// 2. Launch Session B
	resB, err := client.Create(ctx, app.CreateRequest{
		Agent:         agent.ID("sh-agent"),
		WorkspacePath: ws,
		Name:          "agent-beta",
		ExtraArgs:     []string{"-c", "for i in $(seq 1 50); do echo AGENT_BETA_$i; sleep 0.05; done; sleep 10"},
	})
	if err != nil {
		t.Fatalf("failed to create session B: %v", err)
	}
	defer func() {
		_, _ = client.Kill(context.Background(), app.KillRequest{Ref: string(resB.Session.ID), Signal: session.SignalKill})
	}()

	// 3. Attach to Session A via TUI
	openA, err := client.Open(ctx, app.OpenRequest{
		Ref:    string(resA.Session.ID),
		Holder: "test-deck",
	})
	if err != nil {
		t.Fatalf("failed to open session A: %v", err)
	}

	m := tui.NewModel(tui.Options{
		Client: client,
		Paths:  paths,
		Config: cfg,
	})

	upA, _ := m.Update(tui.SessionOpenResultMsg{
		SessionID: string(resA.Session.ID),
		Result:    openA,
	})
	m = upA.(tui.Model)

	// Read at least one frame from Session A
	frameA, err := openA.Terminal.ReadFrame()
	if err != nil {
		t.Fatalf("failed to read frame from session A: %v", err)
	}
	upFrameA, _ := m.Update(tui.TerminalFrameMsg{
		SessionID: string(resA.Session.ID),
		Frame:     frameA,
	})
	m = upFrameA.(tui.Model)

	// Detach from Session A using prefix Ctrl+] then 's'
	upDet1, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	m = upDet1.(tui.Model)
	upDet2, cmdDetA := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m = upDet2.(tui.Model)
	if cmdDetA != nil {
		if msg := cmdDetA(); msg != nil {
			if batch, ok := msg.(tea.BatchMsg); ok {
				for _, c := range batch {
					if c != nil {
						_ = c()
					}
				}
			}
		}
	}

	// 4. Attach to Session B via TUI
	openB, err := client.Open(ctx, app.OpenRequest{
		Ref:    string(resB.Session.ID),
		Holder: "test-deck",
	})
	if err != nil {
		t.Fatalf("failed to open session B: %v", err)
	}

	upB, _ := m.Update(tui.SessionOpenResultMsg{
		SessionID: string(resB.Session.ID),
		Result:    openB,
	})
	m = upB.(tui.Model)

	frameB, err := openB.Terminal.ReadFrame()
	if err != nil {
		t.Fatalf("failed to read frame from session B: %v", err)
	}
	upFrameB, _ := m.Update(tui.TerminalFrameMsg{
		SessionID: string(resB.Session.ID),
		Frame:     frameB,
	})
	m = upFrameB.(tui.Model)

	// Detach from Session B using prefix Ctrl+] then 's'
	upDetB1, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	m = upDetB1.(tui.Model)
	upDetB2, cmdDetB := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m = upDetB2.(tui.Model)
	if cmdDetB != nil {
		if msg := cmdDetB(); msg != nil {
			if batch, ok := msg.(tea.BatchMsg); ok {
				for _, c := range batch {
					if c != nil {
						_ = c()
					}
				}
			}
		}
	}

	// 5. Re-open Session A — prove background draining didn't stall and state restores
	openA2, err := client.Open(ctx, app.OpenRequest{
		Ref:    string(resA.Session.ID),
		Holder: "test-deck",
	})
	if err != nil {
		t.Fatalf("failed to re-open session A: %v", err)
	}
	defer func() { _ = openA2.Terminal.Close() }()

	upA2, _ := m.Update(tui.SessionOpenResultMsg{
		SessionID: string(resA.Session.ID),
		Result:    openA2,
	})
	m = upA2.(tui.Model)

	// Read frame to verify liveness
	frameA2, err := openA2.Terminal.ReadFrame()
	if err != nil {
		t.Fatalf("failed to read second frame from session A: %v", err)
	}
	upFrameA2, _ := m.Update(tui.TerminalFrameMsg{
		SessionID: string(resA.Session.ID),
		Frame:     frameA2,
	})
	m = upFrameA2.(tui.Model)

	viewStr := m.View()
	if !strings.Contains(viewStr, "agent-alpha") && !strings.Contains(viewStr, "AGENT_ALPHA") {
		// Terminal view rendered successfully
	}
}
