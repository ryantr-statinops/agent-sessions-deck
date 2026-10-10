//go:build linux

package ipc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/events"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

const clientQueueSize = 32

// Client implements the application boundary over the foreground owner's Unix socket.
type Client struct {
	path     string
	mu       sync.Mutex
	instance string
}

// NewClient constructs a client for one state-scoped Unix socket.
func NewClient(socketPath string) *Client { return &Client{path: socketPath} }

// OwnerInstanceID returns the instance ID learned from the latest successful handshake.
func (c *Client) OwnerInstanceID() string { c.mu.Lock(); defer c.mu.Unlock(); return c.instance }

func (c *Client) rememberInstance(id string) error {
	if id == "" {
		return session.NewError(session.CodeOwnerUnavailable, "owner", "owner handshake omitted its instance ID", "restart the owner and retry")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.instance != "" && c.instance != id {
		return session.NewError(session.CodeOwnerUnavailable, "owner", "the foreground owner changed during this client lifetime", "inspect the session state before retrying")
	}
	c.instance = id
	return nil
}

func (c *Client) handshake(ctx context.Context, conn *net.UnixConn) (string, error) {
	id, err := newRequestID()
	if err != nil {
		return "", err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(requestTimeout))
	}
	if err := WriteFrame(conn, Frame{Version: ProtocolVersion, Type: FrameHello, RequestID: id}); err != nil {
		return "", err
	}
	ack, err := ReadFrame(conn)
	if err != nil {
		return "", err
	}
	if ack.Type != FrameHelloAck || ack.RequestID != id {
		return "", fmt.Errorf("invalid IPC owner handshake response")
	}
	if err := c.rememberInstance(ack.Instance); err != nil {
		return "", err
	}
	return ack.Instance, nil
}

func (c *Client) request(ctx context.Context, operation string, request any, stream bool) (Frame, *net.UnixConn, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return Frame{}, nil, err
	}
	requestID, err := newRequestID()
	if err != nil {
		return Frame{}, nil, err
	}
	attempts := 1
	if !stream {
		attempts = 2
	}
	var last error
	for attempt := 0; attempt < attempts; attempt++ {
		conn, dialErr := (&net.Dialer{Timeout: requestTimeout}).DialContext(ctx, "unix", c.path)
		if dialErr != nil {
			last = dialErr
			if !stream {
				continue
			}
			break
		}
		unixConn, ok := conn.(*net.UnixConn)
		if !ok {
			_ = conn.Close()
			return Frame{}, nil, errors.New("IPC transport did not return a Unix socket")
		}
		if _, err := c.handshake(ctx, unixConn); err != nil {
			last = err
			_ = unixConn.Close()
			if !stream {
				continue
			}
			break
		}
		if deadline, ok := ctx.Deadline(); ok {
			_ = unixConn.SetDeadline(deadline)
		} else if !stream {
			_ = unixConn.SetDeadline(time.Now().Add(requestTimeout))
		}
		if err := WriteFrame(unixConn, Frame{Version: ProtocolVersion, Type: FrameRequest, RequestID: requestID, Operation: operation, Payload: payload}); err != nil {
			last = err
			_ = unixConn.Close()
			if !stream {
				continue
			}
			break
		}
		response, err := ReadFrame(unixConn)
		if err != nil {
			last = err
			_ = unixConn.Close()
			if !stream {
				continue
			}
			break
		}
		if response.Type != FrameResponse || response.RequestID != requestID {
			_ = unixConn.Close()
			return Frame{}, nil, session.NewError(session.CodeOwnerUnavailable, operation, "owner returned a mismatched response", "retry the request")
		}
		if response.Error != nil {
			_ = unixConn.Close()
			return response, nil, response.Error.AsError()
		}
		if stream {
			_ = unixConn.SetDeadline(time.Time{})
			return response, unixConn, nil
		}
		_ = unixConn.Close()
		return response, nil, nil
	}
	return Frame{}, nil, session.WrapError(session.CodeOwnerUnavailable, operation, "cannot complete IPC request with the foreground owner", "check that the owner is running, then retry", last)
}

func newRequestID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("mint IPC request ID: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

func decodeResult(frame Frame, target any) error {
	if len(frame.Payload) == 0 {
		return errors.New("IPC response omitted its payload")
	}
	return decodePayload(frame.Payload, target)
}

func (c *Client) call(ctx context.Context, op string, request, result any) error {
	frame, _, err := c.request(ctx, op, request, false)
	if err != nil {
		return err
	}
	return decodeResult(frame, result)
}

func (c *Client) Scan(ctx context.Context, req app.ScanRequest) (out app.ScanResult, err error) {
	err = c.call(ctx, "scan", req, &out)
	return
}
func (c *Client) List(ctx context.Context, req app.ListRequest) (out app.ListResult, err error) {
	err = c.call(ctx, "list", req, &out)
	return
}
func (c *Client) Get(ctx context.Context, req app.GetRequest) (out app.GetResult, err error) {
	err = c.call(ctx, "inspect", req, &out)
	return
}
func (c *Client) Create(ctx context.Context, req app.CreateRequest) (out app.CreateResult, err error) {
	err = c.call(ctx, "create", req, &out)
	return
}
func (c *Client) Detach(ctx context.Context, req app.DetachRequest) (out app.DetachResult, err error) {
	err = c.call(ctx, "detach", req, &out)
	return
}
func (c *Client) Rename(ctx context.Context, req app.RenameRequest) (out app.RenameResult, err error) {
	err = c.call(ctx, "rename", req, &out)
	return
}
func (c *Client) Restart(ctx context.Context, req app.RestartRequest) (out app.RestartResult, err error) {
	err = c.call(ctx, "restart", req, &out)
	return
}
func (c *Client) Stop(ctx context.Context, req app.StopRequest) (out app.StopResult, err error) {
	err = c.call(ctx, "stop", req, &out)
	return
}
func (c *Client) Kill(ctx context.Context, req app.KillRequest) (out app.KillResult, err error) {
	err = c.call(ctx, "kill", req, &out)
	return
}
func (c *Client) Delete(ctx context.Context, req app.DeleteRequest) (out app.DeleteResult, err error) {
	err = c.call(ctx, "delete", req, &out)
	return
}

func (c *Client) Open(ctx context.Context, req app.OpenRequest) (app.OpenResult, error) {
	frame, conn, err := c.request(ctx, "open", req, true)
	if err != nil {
		return app.OpenResult{}, err
	}
	var header struct {
		Session  session.SessionSnapshot `json:"session"`
		Lease    app.InteractiveLease    `json:"lease"`
		Revision uint64                  `json:"revision"`
		Event    events.Event            `json:"event"`
		Snapshot app.TerminalSnapshot    `json:"snapshot"`
	}
	if err := decodeResult(frame, &header); err != nil {
		_ = conn.Close()
		return app.OpenResult{}, err
	}
	terminal := newRemoteSubscription(conn, string(header.Session.ID), header.Lease.Generation, req.Holder, header.Snapshot)
	return app.OpenResult{Session: header.Session, Lease: header.Lease, Terminal: terminal, Revision: header.Revision, Event: header.Event}, nil
}

var _ app.Client = (*Client)(nil)
var _ app.TerminalSubscription = (*remoteSubscription)(nil)

type remoteSubscription struct {
	conn       *net.UnixConn
	sessionID  string
	generation session.Generation
	holder     string
	initial    app.TerminalSnapshot
	writeMu    sync.Mutex
	closeOnce  sync.Once
	mu         sync.Mutex
	readErr    error
	raw        chan []byte
	frames     chan app.TerminalFrame
	done       chan struct{}
	gap        bool
	rawEnded   bool
	rawGap     chan struct{}
	rawGapOnce sync.Once
	current    app.TerminalSnapshot
}

func newRemoteSubscription(conn *net.UnixConn, id string, generation session.Generation, holder string, snapshot app.TerminalSnapshot) *remoteSubscription {
	r := &remoteSubscription{conn: conn, sessionID: id, generation: generation, holder: holder, initial: snapshot, current: snapshot, raw: make(chan []byte, clientQueueSize), frames: make(chan app.TerminalFrame, 1), done: make(chan struct{}), rawGap: make(chan struct{})}
	go r.receive()
	return r
}
func (r *remoteSubscription) SessionID() session.ID                 { return session.ID(r.sessionID) }
func (r *remoteSubscription) Generation() session.Generation        { return r.generation }
func (r *remoteSubscription) Holder() string                        { return r.holder }
func (r *remoteSubscription) InitialSnapshot() app.TerminalSnapshot { return r.initial }
func (r *remoteSubscription) ReadFrame() (app.TerminalFrame, error) {
	select {
	case frame, ok := <-r.frames:
		if ok {
			return frame, nil
		}
		return app.TerminalFrame{}, r.readError()
	case <-r.done:
		return app.TerminalFrame{}, r.readError()
	}
}
func (r *remoteSubscription) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	r.mu.Lock()
	if r.gap {
		r.gap = false
		r.rawEnded = true
		r.mu.Unlock()
		return 0, app.ErrTerminalOutputGap
	}
	if r.rawEnded {
		r.mu.Unlock()
		return 0, io.EOF
	}
	r.mu.Unlock()
	select {
	case data, ok := <-r.raw:
		if !ok {
			return 0, r.readError()
		}
		n := copy(p, data)
		if n < len(data) {
			r.markRawGap()
			return n, app.ErrTerminalOutputGap
		}
		return n, nil
	case <-r.rawGap:
		r.mu.Lock()
		r.gap = false
		r.rawEnded = true
		r.mu.Unlock()
		return 0, app.ErrTerminalOutputGap
	case <-r.done:
		return 0, r.readError()
	}
}
func (r *remoteSubscription) Write(p []byte) (int, error) {
	payload, err := json.Marshal(p)
	if err != nil {
		return 0, err
	}
	if err := r.send(Frame{Version: ProtocolVersion, Type: FrameTerminalInput, Payload: payload}); err != nil {
		return 0, err
	}
	return len(p), nil
}
func (r *remoteSubscription) Resize(width, height int) error {
	return r.send(Frame{Version: ProtocolVersion, Type: FrameTerminalResize, Payload: json.RawMessage(fmt.Sprintf(`{"width":%d,"height":%d}`, width, height))})
}
func (r *remoteSubscription) send(frame Frame) error {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	if err := r.conn.SetWriteDeadline(time.Now().Add(requestTimeout)); err != nil {
		return err
	}
	return WriteFrame(r.conn, frame)
}
func (r *remoteSubscription) Close() error {
	var err error
	r.closeOnce.Do(func() {
		err = r.send(Frame{Version: ProtocolVersion, Type: FrameTerminalClose})
		err = errors.Join(err, r.conn.Close())
	})
	return err
}
func (r *remoteSubscription) receive() {
	defer close(r.raw)
	defer close(r.frames)
	defer close(r.done)
	for {
		frame, err := ReadFrame(r.conn)
		if err != nil {
			if !errors.Is(err, net.ErrClosed) && !errors.Is(err, io.EOF) {
				r.setError(err)
			}
			return
		}
		switch frame.Type {
		case FrameTerminalBytes:
			if frame.Error != nil {
				r.markRawGap()
				continue
			}
			var data []byte
			if json.Unmarshal(frame.Payload, &data) != nil {
				r.setError(ErrInvalidFrame)
				return
			}
			r.mu.Lock()
			if r.rawEnded {
				r.mu.Unlock()
				continue
			}
			select {
			case r.raw <- data:
				r.mu.Unlock()
			default:
				r.mu.Unlock()
				r.markRawGap()
			}
		case FrameTerminalScreen:
			var next app.TerminalFrame
			if decodePayload(frame.Payload, &next) != nil {
				r.setError(ErrInvalidFrame)
				return
			}
			if next.Snapshot != nil {
				r.current = *next.Snapshot
			} else if next.Delta != nil {
				if next.Delta.BaseSequence != r.current.Sequence {
					r.setError(errors.New("IPC terminal screen sequence gap"))
					return
				}
				screen := r.current.Screen
				lines := make([]app.TerminalLine, next.Delta.Rows)
				for _, line := range screen.Lines {
					if line.Index >= 0 && line.Index < len(lines) {
						lines[line.Index] = line
					}
				}
				for _, line := range next.Delta.Lines {
					if line.Index >= 0 && line.Index < len(lines) {
						lines[line.Index] = line
					}
				}
				screen.Columns, screen.Rows = next.Delta.Columns, next.Delta.Rows
				screen.Lines, screen.Cursor = lines, next.Delta.Cursor
				screen.AlternateScreen, screen.ScrollbackLines = next.Delta.AlternateScreen, next.Delta.ScrollbackLines
				r.current = app.TerminalSnapshot{Sequence: next.Sequence, Screen: screen}
			} else {
				r.setError(ErrInvalidFrame)
				return
			}
			next = app.TerminalFrame{Sequence: r.current.Sequence, Snapshot: &r.current}
			select {
			case r.frames <- next:
			default:
				select {
				case <-r.frames:
				default:
				}
				select {
				case r.frames <- next:
				default:
				}
			}
		}
	}
}
func (r *remoteSubscription) setError(err error) {
	r.mu.Lock()
	if r.readErr == nil {
		r.readErr = err
	}
	r.mu.Unlock()
}

func (r *remoteSubscription) markRawGap() {
	r.mu.Lock()
	r.gap = true
	r.rawEnded = true
	r.mu.Unlock()
	r.rawGapOnce.Do(func() { close(r.rawGap) })
	r.discardRaw()
}

func (r *remoteSubscription) discardRaw() {
	for {
		select {
		case <-r.raw:
		default:
			return
		}
	}
}

func (r *remoteSubscription) readError() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.gap {
		r.gap = false
		return app.ErrTerminalOutputGap
	}
	if r.readErr != nil {
		return r.readErr
	}
	return io.EOF
}

// Ping completes the versioned owner handshake without dispatching a service operation.
func (c *Client) Ping(ctx context.Context) error {
	conn, err := (&net.Dialer{Timeout: requestTimeout}).DialContext(ctx, "unix", c.path)
	if err != nil {
		return session.WrapError(session.CodeOwnerUnavailable, "owner", "cannot connect to the foreground owner", "start the owner in another terminal", err)
	}
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		_ = conn.Close()
		return session.NewError(session.CodeOwnerUnavailable, "owner", "owner endpoint is not a Unix socket", "check the configured runtime directory")
	}
	defer unixConn.Close()
	if _, err := c.handshake(ctx, unixConn); err != nil {
		return session.WrapError(session.CodeOwnerUnavailable, "owner", "foreground owner handshake failed", "check the owner version and retry", err)
	}
	return nil
}
