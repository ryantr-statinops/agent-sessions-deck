//go:build linux

package cli

import (
	"context"
	"os"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/git"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
)

type commandDocument struct {
	Executable string   `json:"executable"`
	Args       []string `json:"args"`
}

type gitDocument struct {
	Root         string `json:"root"`
	Branch       string `json:"branch"`
	Detached     bool   `json:"detached"`
	Dirty        bool   `json:"dirty"`
	ChangedFiles int    `json:"changed_files"`
}

type sessionDocument struct {
	ID                string                   `json:"id"`
	Name              string                   `json:"name"`
	AgentID           string                   `json:"agent_id"`
	WorkspaceID       string                   `json:"workspace_id"`
	Authority         session.Authority        `json:"authority"`
	ObservedAt        time.Time                `json:"observed_at"`
	StoreRevision     uint64                   `json:"store_revision"`
	Lifecycle         session.Lifecycle        `json:"lifecycle"`
	Attachment        session.Attachment       `json:"attachment"`
	Activity          session.Activity         `json:"activity"`
	ActivityEvidenced bool                     `json:"activity_evidenced"`
	Generation        session.Generation       `json:"generation"`
	AttemptCount      int                      `json:"attempt_count"`
	Command           *commandDocument         `json:"command,omitempty"`
	Identity          *session.ProcessIdentity `json:"identity,omitempty"`
	Reason            session.Reason           `json:"reason"`
	Notes             []string                 `json:"notes,omitempty"`
	LastSeenAt        time.Time                `json:"last_seen_at,omitempty"`
}

type listDocument struct {
	Command    string            `json:"command"`
	Authority  session.Authority `json:"authority"`
	ObservedAt time.Time         `json:"observed_at"`
	Revision   uint64            `json:"revision"`
	Sessions   []sessionDocument `json:"sessions"`
}

type inspectDocument struct {
	Command        string            `json:"command"`
	Authority      session.Authority `json:"authority"`
	ObservedAt     time.Time         `json:"observed_at"`
	Revision       uint64            `json:"revision"`
	Session        sessionDocument   `json:"session"`
	Stored         sessionDocument   `json:"stored"`
	Persisted      bool              `json:"persisted"`
	IOAvailability string            `json:"io_availability"`
	WorkspacePath  string            `json:"workspace_path"`
	Git            *gitDocument      `json:"git,omitempty"`
	WorkspaceError string            `json:"workspace_error,omitempty"`
}

type mutationDocument struct {
	Command            string              `json:"command"`
	Authority          session.Authority   `json:"authority"`
	ObservedAt         time.Time           `json:"observed_at"`
	Revision           uint64              `json:"revision"`
	Session            sessionDocument     `json:"session"`
	PreviousGeneration *session.Generation `json:"previous_generation,omitempty"`
	Stopped            *bool               `json:"stopped,omitempty"`
	TimedOut           *bool               `json:"timed_out,omitempty"`
	Code               session.Code        `json:"code,omitempty"`
	Reason             string              `json:"reason,omitempty"`
	Hint               string              `json:"hint,omitempty"`
	Signal             session.SignalKind  `json:"signal,omitempty"`
}

func sessionDocumentOf(row session.SessionSnapshot) sessionDocument {
	out := sessionDocument{ID: string(row.ID), Name: row.Name, AgentID: string(row.AgentID), WorkspaceID: string(row.WorkspaceID), Authority: row.Authority, ObservedAt: row.ObservedAt, StoreRevision: row.StoreRevision, Lifecycle: row.Lifecycle, Attachment: row.Attachment, Activity: row.Activity, ActivityEvidenced: row.ActivityEvidenced, Generation: row.Generation, AttemptCount: row.AttemptCount, Reason: row.Reason, Notes: append([]string(nil), row.Notes...), LastSeenAt: row.LastSeenAt}
	if !row.Command.IsZero() {
		out.Command = &commandDocument{Executable: row.Command.Executable(), Args: row.Command.Args()}
	}
	if row.HasIdentity {
		identity := row.Identity
		out.Identity = &identity
	}
	return out
}

func listDocumentOf(result app.ListResult) listDocument {
	rows := make([]sessionDocument, 0, len(result.Snapshot.Sessions))
	for _, row := range result.Snapshot.Sessions {
		rows = append(rows, sessionDocumentOf(row))
	}
	return listDocument{Command: "list", Authority: result.Snapshot.Authority, ObservedAt: result.Snapshot.ObservedAt, Revision: result.Snapshot.Revision, Sessions: rows}
}

func inspectDocumentOf(ctx context.Context, result app.GetResult, ownerID string) inspectDocument {
	row := result.Session
	ioStatus := "unknown"
	if row.Authority == session.AuthorityLive {
		switch {
		case row.Lifecycle.Terminal() || row.Orphaned():
			ioStatus = "unavailable"
		case row.Attachment == session.AttachmentAttached:
			ioStatus = "attached"
		case row.HasIdentity && ownerID != "" && row.Identity.OwnerInstanceID == ownerID:
			ioStatus = "available"
		default:
			ioStatus = "unavailable"
		}
	}
	out := inspectDocument{Command: "inspect", Authority: row.Authority, ObservedAt: result.ObservedAt, Revision: result.Revision, Session: sessionDocumentOf(row), Stored: sessionDocumentOf(result.Stored), Persisted: result.Persisted, IOAvailability: ioStatus, WorkspacePath: string(row.WorkspaceID)}
	home, _ := os.UserHomeDir()
	gitClient := git.NewClient()
	resolver := workspace.NewPathResolver(home, gitClient.Probe)
	candidate, err := workspace.NewResolveRequest(out.WorkspacePath, workspace.SourceExplicit)
	if err == nil {
		resolved, resolveErr := resolver.Resolve(ctx, candidate)
		if resolveErr == nil {
			if metadata, ok := resolved.Git(); ok {
				out.Git = &gitDocument{Root: metadata.Root, Branch: metadata.Branch, Detached: metadata.Detached, Dirty: metadata.Dirty, ChangedFiles: metadata.ChangedFiles}
			}
		} else {
			out.WorkspaceError = resolveErr.Error()
		}
	} else {
		out.WorkspaceError = err.Error()
	}
	return out
}

func restartDocumentOf(result app.RestartResult) mutationDocument {
	doc := mutationDocumentOf("restart", result.Session, result.Revision)
	previous := result.PreviousGeneration
	doc.PreviousGeneration = &previous
	return doc
}

func stopDocumentOf(result app.StopResult) mutationDocument {
	doc := mutationDocumentOf("stop", result.Session, result.Revision)
	stopped, timedOut := result.Stopped, result.TimedOut
	doc.Stopped = &stopped
	doc.TimedOut = &timedOut
	if result.TimedOut {
		doc.Code = session.CodeSessionIOFailed
		doc.Reason = "graceful stop timed out; the session remains running"
		doc.Hint = "use kill --yes to escalate explicitly"
	}
	return doc
}

func killDocumentOf(result app.KillResult) mutationDocument {
	doc := mutationDocumentOf("kill", result.Session, result.Revision)
	doc.Signal = result.Signal
	return doc
}

func mutationDocumentOf(command string, row session.SessionSnapshot, revision uint64) mutationDocument {
	return mutationDocument{Command: command, Authority: row.Authority, ObservedAt: row.ObservedAt, Revision: revision, Session: sessionDocumentOf(row)}
}
