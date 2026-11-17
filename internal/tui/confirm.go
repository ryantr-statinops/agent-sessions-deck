package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

func (m Model) renderConfirm() string {
	theme := NewTheme()
	width := m.width
	if width < 80 {
		width = 80
	}

	var b strings.Builder

	title := "CONFIRM ACTION"
	switch m.confirmState.Action {
	case ConfirmRestartRunning:
		title = "CONFIRM RESTART RUNNING SESSION"
	case ConfirmGracefulStop:
		title = "CONFIRM STOPPING SESSION"
	case ConfirmForceKill:
		title = "CONFIRM FORCE KILL SESSION"
	case ConfirmOwnerQuit:
		title = "CONFIRM OWNER SHUTDOWN"
	}

	b.WriteString(theme.ModalTitle.Render("── "+title+" ──") + "\n\n")
	b.WriteString(theme.ModalPrompt.Render(m.confirmState.Prompt) + "\n\n")

	if m.confirmState.Impact != "" {
		b.WriteString(theme.ModalImpact.Render("Impact: "+m.confirmState.Impact) + "\n\n")
	}

	// Action buttons
	confirmKey := "[y] Yes"
	cancelKey := "[n/Esc] Cancel"
	if m.confirmState.Action == ConfirmOwnerQuit {
		confirmKey = "[s] Stop all & Quit"
		cancelKey = "[c/Esc] Cancel"
	}

	b.WriteString(fmt.Sprintf("%s    %s\n",
		theme.ModalConfirm.Render(confirmKey),
		theme.ModalCancel.Render(cancelKey),
	))

	boxStyle := theme.ModalBox.Width(min(width-4, 65))
	return lipgloss.Place(width, m.height, lipgloss.Center, lipgloss.Center, boxStyle.Render(b.String()))
}

// executeActionCmd runs the requested lifecycle action through the client.
func (m Model) executeActionCmd(action ConfirmAction, sessionID string, seq AsyncSeq) tea.Cmd {
	client := m.client
	actionName := "action"
	switch action {
	case ConfirmRestartRunning:
		actionName = "restart"
	case ConfirmGracefulStop:
		actionName = "stop"
	case ConfirmForceKill:
		actionName = "kill"
	}

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		var err error
		switch action {
		case ConfirmRestartRunning:
			_, err = client.Restart(ctx, app.RestartRequest{
				Ref:   sessionID,
				Force: true,
			})
		case ConfirmGracefulStop:
			_, err = client.Stop(ctx, app.StopRequest{
				Ref:   sessionID,
				Grace: 5 * time.Second,
			})
		case ConfirmForceKill:
			_, err = client.Kill(ctx, app.KillRequest{
				Ref:    sessionID,
				Signal: session.SignalKill,
			})
		}

		return ActionCompletedMsg{
			Seq:       seq,
			Action:    actionName,
			SessionID: sessionID,
			Err:       err,
		}
	}
}
