//go:build linux

package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
)

type countingClient struct {
	app.Client
	creates atomic.Int32
}

func (c *countingClient) Create(context.Context, app.CreateRequest) (app.CreateResult, error) {
	c.creates.Add(1)
	time.Sleep(20 * time.Millisecond)
	return app.CreateResult{Revision: 7}, nil
}

func TestDispatcherReplaysDuplicateMutationWithoutCallingServiceTwice(t *testing.T) {
	client := &countingClient{}
	dispatcher, err := NewDispatcher(client)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(app.CreateRequest{Agent: "codex", WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	request := Frame{Version: ProtocolVersion, Type: FrameRequest, RequestID: "create-once", Operation: "create", Payload: payload}
	var wg sync.WaitGroup
	responses := make(chan Frame, 8)
	for range cap(responses) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			responses <- dispatcher.Handle(context.Background(), request)
		}()
	}
	wg.Wait()
	close(responses)
	for response := range responses {
		if response.Error != nil || response.Revision != 7 || response.RequestID != request.RequestID {
			t.Fatalf("duplicate response = %+v", response)
		}
	}
	if got := client.creates.Load(); got != 1 {
		t.Fatalf("Create calls = %d, want exactly one", got)
	}
}

func TestDispatcherRejectsRequestIDReuseWithDifferentPayload(t *testing.T) {
	client := &countingClient{}
	dispatcher, err := NewDispatcher(client)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := json.Marshal(app.CreateRequest{Agent: "codex", WorkspacePath: t.TempDir()})
	second, _ := json.Marshal(app.CreateRequest{Agent: "codex", WorkspacePath: t.TempDir()})
	request := Frame{Version: ProtocolVersion, Type: FrameRequest, RequestID: "same-id", Operation: "create", Payload: first}
	if response := dispatcher.Handle(context.Background(), request); response.Error != nil {
		t.Fatalf("first request error = %+v", response.Error)
	}
	request.Payload = second
	response := dispatcher.Handle(context.Background(), request)
	if response.Error == nil || response.Error.Code.String() != "CONFLICT" {
		t.Fatalf("request ID payload conflict response = %+v", response)
	}
	if got := client.creates.Load(); got != 1 {
		t.Fatalf("Create calls after payload conflict = %d, want one", got)
	}
}

func TestDispatcherRejectsExpiredOperationAsUnknownNotRawPath(t *testing.T) {
	client := &countingClient{Client: failingClient{}}
	dispatcher, err := NewDispatcher(client)
	if err != nil {
		t.Fatal(err)
	}
	request := Frame{Version: ProtocolVersion, Type: FrameRequest, RequestID: "raw-error", Operation: "list", Payload: json.RawMessage(`{}`)}
	response := dispatcher.Handle(context.Background(), request)
	if response.Error == nil || response.Error.Code.String() != "UNKNOWN" || response.Error.Reason == "" {
		t.Fatalf("typed generic error response = %+v", response)
	}
	if response.Error.Reason == "/sensitive/path" {
		t.Fatalf("wire leaked raw cause: %+v", response.Error)
	}
}

type failingClient struct{ app.Client }

func (failingClient) List(context.Context, app.ListRequest) (app.ListResult, error) {
	return app.ListResult{}, errors.New("/sensitive/path")
}

type workspacesTestClient struct {
	app.Client
	workspacesCalled bool
}

func (c *workspacesTestClient) Workspaces(ctx context.Context, req app.WorkspacesRequest) (app.WorkspacesResult, error) {
	c.workspacesCalled = true
	return app.WorkspacesResult{
		Authority: session.AuthorityLive,
		Workspaces: []workspace.WorkspaceSummary{
			{TotalCount: 2, ActiveCount: 1},
		},
	}, nil
}

func TestDispatcherHandlesWorkspaces(t *testing.T) {
	client := &workspacesTestClient{}
	dispatcher, err := NewDispatcher(client)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(app.WorkspacesRequest{})
	request := Frame{
		Version:   ProtocolVersion,
		Type:      FrameRequest,
		RequestID: "ws-req",
		Operation: OpWorkspaces,
		Payload:   payload,
	}
	response := dispatcher.Handle(context.Background(), request)
	if response.Error != nil {
		t.Fatalf("unexpected error response: %+v", response.Error)
	}
	if !client.workspacesCalled {
		t.Fatalf("expected client.Workspaces to be called")
	}
	var res app.WorkspacesResult
	if err := json.Unmarshal(response.Payload, &res); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if len(res.Workspaces) != 1 || res.Workspaces[0].TotalCount != 2 {
		t.Fatalf("unexpected result: %+v", res)
	}
}
