//go:build linux

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/events"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/ipc"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

type cliHistoryClient struct {
	app.Client
	historyReq app.HistoryRequest
	eventsReq  app.EventsRequest
}

func (c *cliHistoryClient) History(ctx context.Context, req app.HistoryRequest) (app.HistoryResult, error) {
	c.historyReq = req
	return app.HistoryResult{
		Authority:  session.AuthorityLive,
		ObservedAt: time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC),
		TotalCount: 1,
		Sessions: []app.SessionHistory{
			{
				ID:           "s-000001",
				Name:         "kestrel-worker",
				AgentID:      "echo-agent",
				WorkspaceID:  "/workspaces/default",
				CreatedAt:    time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC),
				UpdatedAt:    time.Date(2026, 11, 26, 11, 0, 0, 0, time.UTC),
				Lifecycle:    "exited",
				Generation:   2,
				AttemptCount: 2,
				Attempts: []app.AttemptHistory{
					{
						Generation:  1,
						StartedAt:   time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC),
						EndedAt:     time.Date(2026, 11, 26, 10, 30, 0, 0, time.UTC),
						Duration:    "30m0s",
						Lifecycle:   "exited",
						Reason:      "natural_exit(0)",
						ExitCode:    0,
						HasExitCode: true,
					},
					{
						Generation:  2,
						StartedAt:   time.Date(2026, 11, 26, 10, 31, 0, 0, time.UTC),
						EndedAt:     time.Date(2026, 11, 26, 11, 0, 0, 0, time.UTC),
						Duration:    "29m0s",
						Lifecycle:   "exited",
						Reason:      "natural_exit(0)",
						ExitCode:    0,
						HasExitCode: true,
					},
				},
			},
		},
	}, nil
}

func (c *cliHistoryClient) Events(ctx context.Context, req app.EventsRequest) (app.EventsResult, error) {
	c.eventsReq = req
	ev := events.NewEvent("evt-1", events.TypeSessionStarted, "s-000001", 1, 42,
		events.NewTimestamp(time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)), events.Metadata{})
	return app.EventsResult{
		Authority:    session.AuthorityLive,
		ObservedAt:   time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC),
		Events:       []events.Event{ev},
		NextRevision: 42,
	}, nil
}

func TestCLIHistoryCommand(t *testing.T) {
	paths := config.Paths{StateDir: t.TempDir()}
	claim, err := ipc.BootstrapPaths(paths)
	if err != nil {
		t.Fatal(err)
	}
	client := &cliHistoryClient{}
	server, err := ipc.NewServer(claim, client)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Serve(ctx) }()

	var stdout, stderr bytes.Buffer
	opts := Options{
		Paths:  paths,
		Config: config.Default(),
		Stdout: &stdout,
		Stderr: &stderr,
	}

	cmd := historyCommand(opts)
	cmd.SetArgs([]string{"--attempts", "--limit", "10"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("history command failed: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "s-000001") || !strings.Contains(out, "kestrel-worker") {
		t.Fatalf("missing session info: %s", out)
	}
	if !strings.Contains(out, "gen 1") || !strings.Contains(out, "gen 2") {
		t.Fatalf("missing attempt history: %s", out)
	}
	if client.historyReq.Limit != 10 || !client.historyReq.IncludeAttempts {
		t.Fatalf("req mismatch: %+v", client.historyReq)
	}
}

func TestCLIHistoryJSON(t *testing.T) {
	paths := config.Paths{StateDir: t.TempDir()}
	claim, err := ipc.BootstrapPaths(paths)
	if err != nil {
		t.Fatal(err)
	}
	client := &cliHistoryClient{}
	server, err := ipc.NewServer(claim, client)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Serve(ctx) }()

	var stdout, stderr bytes.Buffer
	opts := Options{
		Paths:  paths,
		Config: config.Default(),
		Stdout: &stdout,
		Stderr: &stderr,
	}

	cmd := historyCommand(opts)
	cmd.SetArgs([]string{"--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("history --json failed: %v", err)
	}

	var res app.HistoryResult
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal json: %v", err)
	}
	if res.TotalCount != 1 || res.Sessions[0].ID != "s-000001" {
		t.Fatalf("json result mismatch: %+v", res)
	}
}

func TestCLIEventsCommand(t *testing.T) {
	paths := config.Paths{StateDir: t.TempDir()}
	claim, err := ipc.BootstrapPaths(paths)
	if err != nil {
		t.Fatal(err)
	}
	client := &cliHistoryClient{}
	server, err := ipc.NewServer(claim, client)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Serve(ctx) }()

	var stdout, stderr bytes.Buffer
	opts := Options{
		Paths:  paths,
		Config: config.Default(),
		Stdout: &stdout,
		Stderr: &stderr,
	}

	cmd := eventsCommand(opts)
	cmd.SetArgs([]string{"--session", "s-000001", "--limit", "25"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("events command failed: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "session.started") || !strings.Contains(out, "s-000001") {
		t.Fatalf("missing event info: %s", out)
	}
	if client.eventsReq.SessionID != "s-000001" || client.eventsReq.Limit != 25 {
		t.Fatalf("eventsReq mismatch: %+v", client.eventsReq)
	}
}

func TestCLIHistoryPruneDryRun(t *testing.T) {
	var stdout, stderr bytes.Buffer
	opts := Options{
		Paths:  config.Paths{StateDir: t.TempDir()},
		Config: config.Default(),
		Stdout: &stdout,
		Stderr: &stderr,
	}

	cmd := historyPruneCommand(opts)
	cmd.SetArgs([]string{"--dry-run", "--max-attempts", "10"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("prune --dry-run failed: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "[DRY RUN]") || !strings.Contains(out, "10") {
		t.Fatalf("dry run output mismatch: %s", out)
	}
}
