//go:build linux

package ipc

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

type listClient struct{ app.Client }

func (listClient) List(context.Context, app.ListRequest) (app.ListResult, error) {
	return app.ListResult{Snapshot: session.Snapshot{Revision: 12}}, nil
}

func TestServerClientHandshakeDispatchAndOwnerExclusion(t *testing.T) {
	paths := config.Paths{StateDir: t.TempDir()}
	owner, err := BootstrapPaths(paths)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(owner, listClient{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(ctx) }()
	client := NewClient(mustSocketPath(t, paths))
	callCtx, callCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer callCancel()
	if err := client.Ping(callCtx); err != nil {
		t.Fatal(err)
	}
	result, err := client.List(callCtx, app.ListRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Snapshot.Revision != 12 {
		t.Fatalf("list revision = %d, want 12", result.Snapshot.Revision)
	}
	if _, err := BootstrapPaths(paths); !errors.Is(err, ErrOwnerLocked) {
		t.Fatalf("competing owner error = %v, want ErrOwnerLocked", err)
	}
	cancel()
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop after context cancellation")
	}
}

type streamClient struct {
	app.Client
	terminal *testSubscription
	detached chan struct{}
}

func (c *streamClient) Open(_ context.Context, req app.OpenRequest) (app.OpenResult, error) {
	id := session.ID("stream-session")
	c.terminal = newTestSubscription(id, 1, req.Holder)
	return app.OpenResult{Session: session.SessionSnapshot{ID: id}, Lease: session.InteractiveLease{SessionID: id, Generation: 1, Holder: req.Holder}, Terminal: c.terminal, Revision: 4}, nil
}
func (c *streamClient) Detach(context.Context, app.DetachRequest) (app.DetachResult, error) {
	select {
	case c.detached <- struct{}{}:
	default:
	}
	return app.DetachResult{}, nil
}

type testSubscription struct {
	id         session.ID
	generation session.Generation
	holder     string
	initial    app.TerminalSnapshot
	raw        chan []byte
	frames     chan app.TerminalFrame
	writes     chan []byte
	resizes    chan [2]int
	done       chan struct{}
	once       sync.Once
}

func newTestSubscription(id session.ID, generation session.Generation, holder string) *testSubscription {
	return &testSubscription{id: id, generation: generation, holder: holder, raw: make(chan []byte, 4), frames: make(chan app.TerminalFrame, 4), writes: make(chan []byte, 2), resizes: make(chan [2]int, 2), done: make(chan struct{})}
}
func (s *testSubscription) SessionID() session.ID                 { return s.id }
func (s *testSubscription) Generation() session.Generation        { return s.generation }
func (s *testSubscription) Holder() string                        { return s.holder }
func (s *testSubscription) InitialSnapshot() app.TerminalSnapshot { return s.initial }
func (s *testSubscription) ReadFrame() (app.TerminalFrame, error) {
	select {
	case frame := <-s.frames:
		return frame, nil
	case <-s.done:
		return app.TerminalFrame{}, io.EOF
	}
}
func (s *testSubscription) Read(p []byte) (int, error) {
	select {
	case data := <-s.raw:
		return copy(p, data), nil
	case <-s.done:
		return 0, io.EOF
	}
}
func (s *testSubscription) Write(p []byte) (int, error) {
	data := append([]byte(nil), p...)
	s.writes <- data
	return len(p), nil
}
func (s *testSubscription) Resize(width, height int) error {
	s.resizes <- [2]int{width, height}
	return nil
}
func (s *testSubscription) Close() error { s.once.Do(func() { close(s.done) }); return nil }

func TestIPCOpenStreamsBytesInputScreenAndDetachesOnDisconnect(t *testing.T) {
	paths := config.Paths{StateDir: t.TempDir()}
	owner, err := BootstrapPaths(paths)
	if err != nil {
		t.Fatal(err)
	}
	service := &streamClient{detached: make(chan struct{}, 1)}
	server, err := NewServer(owner, service)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(ctx) }()
	client := NewClient(mustSocketPath(t, paths))
	openCtx, openCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer openCancel()
	opened, err := client.Open(openCtx, app.OpenRequest{Ref: "stream-session", Holder: "terminal-a"})
	if err != nil {
		t.Fatal(err)
	}
	service.terminal.raw <- []byte("child output")
	buffer := make([]byte, 32)
	n, err := opened.Terminal.Read(buffer)
	if err != nil || string(buffer[:n]) != "child output" {
		t.Fatalf("terminal output = %q, %v", buffer[:n], err)
	}
	if _, err := opened.Terminal.Write([]byte("input")); err != nil {
		t.Fatal(err)
	}
	select {
	case data := <-service.terminal.writes:
		if string(data) != "input" {
			t.Fatalf("terminal input = %q", data)
		}
	case <-time.After(time.Second):
		t.Fatal("terminal input was not forwarded")
	}
	if err := opened.Terminal.Resize(100, 40); err != nil {
		t.Fatal(err)
	}
	select {
	case size := <-service.terminal.resizes:
		if size != [2]int{100, 40} {
			t.Fatalf("terminal resize = %v", size)
		}
	case <-time.After(time.Second):
		t.Fatal("terminal resize was not forwarded")
	}
	snapshot := app.TerminalSnapshot{Sequence: 1, Screen: app.TerminalScreen{Columns: 80, Rows: 24}}
	service.terminal.frames <- app.TerminalFrame{Sequence: 1, Snapshot: &snapshot}
	frameCtx, frameCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer frameCancel()
	frame, err := opened.Terminal.ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	if frame.Snapshot == nil || frame.Sequence != 1 {
		t.Fatalf("screen frame = %+v", frame)
	}
	if err := opened.Terminal.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-service.detached:
	case <-time.After(2 * time.Second):
		t.Fatal("stream disconnect did not release the lease")
	}
	cancel()
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-frameCtx.Done():
		t.Fatal("owner server did not shut down")
	}
}

func mustSocketPath(t *testing.T, paths config.Paths) string {
	t.Helper()
	path, err := SocketPath(paths)
	if err != nil {
		t.Fatal(err)
	}
	return path
}
