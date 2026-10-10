//go:build linux

package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

const requestTimeout = 30 * time.Second

// Server serves one foreground owner over its already-bound Unix socket.
type Server struct {
	owner      *Owner
	client     app.Client
	dispatcher *Dispatcher
	mu         sync.Mutex
	conns      map[*net.UnixConn]struct{}
}

// NewServer binds the protocol dispatcher to the foreground application client.
func NewServer(owner *Owner, client app.Client) (*Server, error) {
	if owner == nil || owner.Listener() == nil {
		return nil, errors.New("IPC server requires a bootstrapped owner")
	}
	dispatcher, err := NewDispatcher(client)
	if err != nil {
		return nil, err
	}
	return &Server{owner: owner, client: client, dispatcher: dispatcher, conns: make(map[*net.UnixConn]struct{})}, nil
}

// Serve accepts same-UID clients until ctx ends or the listener fails. The
// state-home lock remains held until all accepted requests have stopped.
func (s *Server) Serve(ctx context.Context) error {
	if s == nil || s.owner == nil {
		return errors.New("IPC server is not initialized")
	}
	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = s.owner.Listener().Close()
			s.mu.Lock()
			for conn := range s.conns {
				_ = conn.Close()
			}
			s.mu.Unlock()
		case <-stop:
		}
	}()
	var workers sync.WaitGroup
	var serveErr error
	for {
		conn, err := s.owner.Listener().AcceptUnix()
		if err != nil {
			if ctx.Err() == nil && !errors.Is(err, net.ErrClosed) {
				serveErr = fmt.Errorf("accept IPC client: %w", err)
			}
			break
		}
		if err := VerifyPeerUID(conn); err != nil {
			_ = conn.Close()
			continue
		}
		s.mu.Lock()
		s.conns[conn] = struct{}{}
		s.mu.Unlock()
		workers.Add(1)
		go func() {
			defer workers.Done()
			defer func() {
				s.mu.Lock()
				delete(s.conns, conn)
				s.mu.Unlock()
				_ = conn.Close()
			}()
			s.serveConn(ctx, conn)
		}()
	}
	close(stop)
	workers.Wait()
	closeErr := s.owner.Close()
	if serveErr != nil {
		return errors.Join(serveErr, closeErr)
	}
	return closeErr
}

func (s *Server) serveConn(ownerCtx context.Context, conn *net.UnixConn) {
	_ = conn.SetDeadline(time.Now().Add(requestTimeout))
	hello, err := ReadFrame(conn)
	if err != nil || hello.Type != FrameHello {
		return
	}
	if err := WriteFrame(conn, Frame{Version: ProtocolVersion, Type: FrameHelloAck, RequestID: hello.RequestID, Instance: s.owner.InstanceID()}); err != nil {
		return
	}
	request, err := ReadFrame(conn)
	if err != nil || request.Type != FrameRequest {
		return
	}
	if request.Operation == "open" {
		s.serveOpen(ownerCtx, conn, request)
		return
	}
	ctx, cancel := context.WithTimeout(ownerCtx, requestTimeout)
	defer cancel()
	response := s.dispatcher.Handle(ctx, request)
	_ = conn.SetWriteDeadline(time.Now().Add(requestTimeout))
	_ = WriteFrame(conn, response)
}

func (s *Server) serveOpen(ownerCtx context.Context, conn *net.UnixConn, request Frame) {
	var open app.OpenRequest
	if err := decodePayload(request.Payload, &open); err != nil {
		s.writeError(conn, request.RequestID, session.WrapError(session.CodeInvalidConfiguration, open.Ref, "open request payload is invalid", "send a valid open request", err))
		return
	}
	ctx, cancel := context.WithTimeout(ownerCtx, requestTimeout)
	result, err := s.client.Open(ctx, open)
	cancel()
	if err != nil {
		s.writeError(conn, request.RequestID, err)
		return
	}
	header := struct {
		Session  any                  `json:"session"`
		Lease    app.InteractiveLease `json:"lease"`
		Revision uint64               `json:"revision"`
		Event    any                  `json:"event,omitempty"`
		Snapshot app.TerminalSnapshot `json:"snapshot"`
	}{result.Session, result.Lease, result.Revision, result.Event, result.Terminal.InitialSnapshot()}
	payload, err := json.Marshal(header)
	if err != nil {
		_ = result.Terminal.Close()
		_, _ = s.client.Detach(ownerCtx, app.DetachRequest{Ref: string(result.Terminal.SessionID())})
		s.writeError(conn, request.RequestID, session.NewError(session.CodeUnknown, open.Ref, "cannot encode stream attachment", "retry the attach request"))
		return
	}
	_ = conn.SetWriteDeadline(time.Now().Add(requestTimeout))
	if err := WriteFrame(conn, Frame{Version: ProtocolVersion, Type: FrameResponse, RequestID: request.RequestID, Payload: payload, Revision: result.Revision}); err != nil {
		_ = result.Terminal.Close()
		_, _ = s.client.Detach(ownerCtx, app.DetachRequest{Ref: string(result.Terminal.SessionID())})
		return
	}
	s.stream(ownerCtx, conn, result.Terminal)
}

func (s *Server) stream(ownerCtx context.Context, conn *net.UnixConn, terminal app.TerminalSubscription) {
	defer func() {
		_ = terminal.Close()
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		_, _ = s.client.Detach(ctx, app.DetachRequest{Ref: string(terminal.SessionID())})
	}()
	_ = conn.SetDeadline(time.Time{})
	var writeMu sync.Mutex
	write := func(frame Frame) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(requestTimeout))
		return WriteFrame(conn, frame)
	}
	streamCtx, cancel := context.WithCancel(ownerCtx)
	defer cancel()
	var readers sync.WaitGroup
	readers.Add(2)
	go func() {
		defer readers.Done()
		buf := make([]byte, 32<<10)
		for {
			n, err := terminal.Read(buf)
			if n > 0 {
				payload, _ := json.Marshal(buf[:n])
				if write(Frame{Version: ProtocolVersion, Type: FrameTerminalBytes, Payload: payload}) != nil {
					cancel()
					_ = conn.Close()
					return
				}
			}
			if err != nil {
				if errors.Is(err, app.ErrTerminalOutputGap) {
					gap := session.NewError(session.CodeSessionIOFailed, string(terminal.SessionID()), "terminal byte subscriber fell behind", "detach and reattach to start a fresh stream")
					_ = write(Frame{Version: ProtocolVersion, Type: FrameTerminalBytes, Error: ErrorToWire(gap)})
					return
				}
				if !errors.Is(err, io.EOF) {
					cancel()
					_ = conn.Close()
				}
				return
			}
		}
	}()
	go func() {
		defer readers.Done()
		for {
			frame, err := terminal.ReadFrame()
			if err != nil {
				cancel()
				_ = conn.Close()
				return
			}
			payload, marshalErr := json.Marshal(frame)
			if marshalErr != nil || write(Frame{Version: ProtocolVersion, Type: FrameTerminalScreen, Payload: payload}) != nil {
				cancel()
				_ = conn.Close()
				return
			}
		}
	}()
	for streamCtx.Err() == nil {
		frame, err := ReadFrame(conn)
		if err != nil {
			break
		}
		switch frame.Type {
		case FrameTerminalInput:
			var data []byte
			if json.Unmarshal(frame.Payload, &data) != nil || len(data) > MaxFrameBytes/2 {
				cancel()
				break
			}
			if _, err := terminal.Write(data); err != nil {
				cancel()
				break
			}
		case FrameTerminalResize:
			var size struct{ Width, Height int }
			if json.Unmarshal(frame.Payload, &size) != nil || size.Width <= 0 || size.Height <= 0 {
				cancel()
				break
			}
			if err := terminal.Resize(size.Width, size.Height); err != nil {
				cancel()
				break
			}
		case FrameTerminalClose:
			cancel()
			_ = terminal.Close()
			_ = conn.Close()
			readers.Wait()
			return
		default:
			cancel()
		}
	}
	cancel()
	_ = terminal.Close()
	_ = conn.Close()
	readers.Wait()
}

func (s *Server) writeError(conn *net.UnixConn, requestID string, err error) {
	_ = WriteFrame(conn, Frame{Version: ProtocolVersion, Type: FrameResponse, RequestID: requestID, Error: ErrorToWire(err)})
}
