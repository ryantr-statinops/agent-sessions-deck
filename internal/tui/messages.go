package tui

import (
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/events"
)

// ScanResultMsg delivers asynchronous agent probe results.
type ScanResultMsg struct {
	Seq    AsyncSeq
	Result app.ScanResult
	Err    error
}

// SessionListMsg delivers asynchronous session list updates.
type SessionListMsg struct {
	Seq    AsyncSeq
	Result app.ListResult
	Err    error
}

// ActionCompletedMsg conveys the outcome of a lifecycle operation (restart, stop, kill, rename).
type ActionCompletedMsg struct {
	Seq       AsyncSeq
	Action    string
	SessionID string
	Err       error
}

// LaunchCompletedMsg conveys the outcome of a session creation.
type LaunchCompletedMsg struct {
	Seq       AsyncSeq
	SessionID string
	Err       error
}

// StatusClearMsg clears temporary status notices after a duration.
type StatusClearMsg struct {
	Timestamp time.Time
}

// SuspendForAttachMsg signals the outer CLI runner to temporarily suspend Bubble Tea
// and enter the interactive raw PTY stream (open command), resuming when detached.
type SuspendForAttachMsg struct {
	SessionID string
}

// QuitMsg signals an intentional exit from the application.
type QuitMsg struct {
	StopSessions bool
}

// TerminalFrameMsg delivers an asynchronous terminal frame update from a session subscription.
type TerminalFrameMsg struct {
	SessionID string
	Frame     app.TerminalFrame
	Err       error
}

// SessionOpenResultMsg delivers the result of opening a session stream.
type SessionOpenResultMsg struct {
	SessionID string
	Result    app.OpenResult
	Err       error
}

// ResyncMsg signals that the event stream overflowed or requested a state resynchronization.
type ResyncMsg struct {
	Revision events.Revision
}
