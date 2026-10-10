//go:build linux

package terminal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/pty"
	domain "github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

const (
	ptyReadBufferBytes    = 32 * 1024
	ptyWriteBufferBytes   = 32 * 1024
	queryReplyBufferBytes = 4096
)

var (
	ErrInputQueueFull = errors.New("terminal input queue is full")
	ErrInputTooLarge  = errors.New("terminal input exceeds the configured byte limit")
)

type resizeRequest struct {
	sub  *subscription
	size pty.Size
	done chan error
}

// Session owns one PTY drain, terminal emulator and set of bounded input/output
// queues. Process wait and signaling remain owned by the session runtime.
type Session struct {
	id         domain.ID
	generation domain.Generation
	child      *pty.Child
	screen     *Screen
	options    Options
	size       pty.Size

	streamMu       sync.Mutex
	rawSubscriber  *subscription
	outputFinished bool
	outputErr      error
	disposed       bool

	inputMu      sync.Mutex
	inputCond    *sync.Cond
	userInput    byteRing
	replyInput   byteRing
	writerClosed bool
	writerErr    error

	resizeMu       sync.Mutex
	resizeClosed   bool
	resizeRequests chan resizeRequest
	resizeStop     chan struct{}

	readerDone  chan struct{}
	replyDone   chan struct{}
	writerDone  chan struct{}
	resizeDone  chan struct{}
	outputDone  chan struct{}
	finishOnce  sync.Once
	disposeOnce sync.Once
}

// subscription implements both the raw terminal-byte stream and the ordered screen
// frame stream. The output ring is fixed-size and independent from the VT parser.
type subscription struct {
	session *Session
	screen  *ScreenSubscription
	lease   app.InteractiveLease

	raw       byteRing
	rawGap    bool
	rawFailed bool
	closed    bool
	rawCond   *sync.Cond
}

func newSession(id domain.ID, generation domain.Generation, child *pty.Child, size pty.Size, options Options) (*Session, error) {
	if err := options.Validate(); err != nil {
		return nil, err
	}
	screen, err := NewScreen(size.Columns, size.Rows, options.ScrollbackLines)
	if err != nil {
		return nil, err
	}
	s := &Session{
		id:             id,
		generation:     generation,
		child:          child,
		screen:         screen,
		options:        options,
		size:           size,
		userInput:      newByteRing(options.MaxInputBytes),
		replyInput:     newByteRing(queryReplyBufferBytes),
		resizeRequests: make(chan resizeRequest, MaxResizeBatch),
		resizeStop:     make(chan struct{}),
		readerDone:     make(chan struct{}),
		replyDone:      make(chan struct{}),
		writerDone:     make(chan struct{}),
		resizeDone:     make(chan struct{}),
		outputDone:     make(chan struct{}),
	}
	s.inputCond = sync.NewCond(&s.inputMu)
	return s, nil
}

func (s *Session) start() {
	go s.writePTY()
	go s.resizeLoop()
	go s.readReplies()
	go s.readPTY()
}

// ID identifies the session whose PTY this object drains.
func (s *Session) ID() domain.ID { return s.id }

// Generation identifies the attempt whose PTY this object drains.
func (s *Session) Generation() domain.Generation { return s.generation }

// OutputDone closes after the PTY reader, reply pump and resize worker stop. A
// blocked write is released by child exit or by the runtime closing the PTY master.
func (s *Session) OutputDone() <-chan struct{} { return s.outputDone }

// OutputFinished reports whether PTY output processing and terminal replies ended.
func (s *Session) OutputFinished() bool {
	select {
	case <-s.outputDone:
		return true
	default:
		return false
	}
}

// WaitOutput waits for PTY EOF or the caller's deadline. The context never controls
// the child process lifetime.
func (s *Session) WaitOutput(ctx context.Context) error {
	select {
	case <-s.outputDone:
		s.streamMu.Lock()
		defer s.streamMu.Unlock()
		return s.outputErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Subscribe creates a raw byte reader and an atomic screen snapshot for one lease.
// Detaching the returned subscription never stops this session's PTY drain.
func (s *Session) Subscribe(ctx context.Context, lease app.InteractiveLease) (app.TerminalSubscription, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if lease.SessionID != s.id || lease.Generation != s.generation {
		return nil, domain.NewError(domain.CodeStaleAttempt, string(s.id), "terminal lease names a different attempt", "re-read the session and attach to its current attempt")
	}
	if lease.Holder == "" {
		return nil, domain.NewError(domain.CodeInvalidConfiguration, string(s.id), "terminal lease has no holder", "")
	}

	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	if s.disposed {
		return nil, domain.NewError(domain.CodeSessionIOFailed, string(s.id), "terminal session has been released", "re-read the session and attach to its current attempt")
	}
	if s.rawSubscriber != nil {
		return nil, domain.NewError(domain.CodeConflict, string(s.id), "terminal session already has an interactive subscription", "detach the current holder before attaching another client")
	}
	screen, err := s.screen.Subscribe()
	if err != nil {
		return nil, err
	}
	sub := &subscription{
		session: s,
		screen:  screen,
		lease:   lease,
		raw:     newByteRing(s.options.MaxRawOutputBytes),
	}
	sub.rawCond = sync.NewCond(&s.streamMu)
	s.rawSubscriber = sub
	return sub, nil
}

// Dispose releases terminal screen state after output and after the owner has
// reaped the child and closed the PTY master. It never signals the process.
func (s *Session) Dispose() error {
	if !s.OutputFinished() {
		return ErrSessionActive
	}
	<-s.readerDone
	<-s.replyDone
	<-s.writerDone
	<-s.resizeDone
	s.disposeOnce.Do(func() {
		s.streamMu.Lock()
		s.disposed = true
		sub := s.rawSubscriber
		if sub != nil {
			sub.closed = true
			sub.raw.Clear()
			sub.rawCond.Broadcast()
			s.rawSubscriber = nil
		}
		s.streamMu.Unlock()
		if sub != nil {
			_ = sub.screen.Close()
		}
		_ = s.screen.Close()
	})
	return nil
}

func (s *Session) readPTY() {
	defer close(s.readerDone)
	buffer := make([]byte, ptyReadBufferBytes)
	var readErr error
	for {
		n, err := s.child.Read(buffer)
		if n > 0 {
			s.streamMu.Lock()
			if !s.outputFinished {
				if _, feedErr := s.screen.Feed(buffer[:n]); feedErr != nil && readErr == nil {
					readErr = fmt.Errorf("feed terminal emulator: %w", feedErr)
				}
				if sub := s.rawSubscriber; sub != nil && !sub.closed && !sub.rawGap && !sub.rawFailed {
					if !sub.raw.Write(buffer[:n]) {
						sub.raw.Clear()
						sub.rawGap = true
					}
					sub.rawCond.Broadcast()
				}
			}
			s.streamMu.Unlock()
		}
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrClosed) && readErr == nil {
				readErr = fmt.Errorf("read terminal PTY: %w", err)
			}
			break
		}
	}
	s.finishOutput(readErr)
}

func (s *Session) readReplies() {
	defer close(s.replyDone)
	buffer := make([]byte, 256)
	for {
		n, err := s.screen.emu.Read(buffer)
		if n > 0 {
			if marker := bytes.IndexByte(buffer[:n], 0); marker >= 0 {
				if marker > 0 {
					_ = s.enqueueReply(buffer[:marker])
				}
				return
			}
			_ = s.enqueueReply(buffer[:n])
		}
		if err != nil {
			return
		}
	}
}

func (s *Session) enqueueReply(data []byte) error {
	if len(data) > queryReplyBufferBytes {
		return errors.New("terminal emulator reply exceeds queue capacity")
	}
	s.inputMu.Lock()
	defer s.inputMu.Unlock()
	for len(data) > s.replyInput.Free() && !s.writerClosed {
		s.inputCond.Wait()
	}
	if s.writerClosed {
		return io.ErrClosedPipe
	}
	if !s.replyInput.Write(data) {
		return errors.New("terminal emulator reply queue is full")
	}
	s.inputCond.Signal()
	return nil
}

func (s *Session) writePTY() {
	defer close(s.writerDone)
	buffer := make([]byte, ptyWriteBufferBytes)
	for {
		s.inputMu.Lock()
		for !s.writerClosed && s.replyInput.Len() == 0 && s.userInput.Len() == 0 {
			s.inputCond.Wait()
		}
		if s.writerClosed {
			s.replyInput.Clear()
			s.userInput.Clear()
			s.inputCond.Broadcast()
			s.inputMu.Unlock()
			return
		}
		n := s.replyInput.Read(buffer)
		if n == 0 {
			n = s.userInput.Read(buffer)
		}
		s.inputCond.Broadcast()
		s.inputMu.Unlock()

		for written := 0; written < n; {
			count, err := s.child.Write(buffer[written:n])
			written += count
			if err != nil {
				s.inputMu.Lock()
				s.writerErr = fmt.Errorf("write terminal PTY: %w", err)
				s.writerClosed = true
				s.replyInput.Clear()
				s.userInput.Clear()
				s.inputCond.Broadcast()
				s.inputMu.Unlock()
				return
			}
			if count == 0 {
				s.inputMu.Lock()
				s.writerErr = io.ErrShortWrite
				s.writerClosed = true
				s.replyInput.Clear()
				s.userInput.Clear()
				s.inputCond.Broadcast()
				s.inputMu.Unlock()
				return
			}
		}
	}
}

func (s *Session) finishOutput(readErr error) {
	s.finishOnce.Do(func() {
		s.streamMu.Lock()
		s.outputFinished = true
		s.outputErr = readErr
		if sub := s.rawSubscriber; sub != nil {
			sub.rawCond.Broadcast()
		}
		s.streamMu.Unlock()

		s.screen.Finish()
		s.resizeMu.Lock()
		if !s.resizeClosed {
			s.resizeClosed = true
			close(s.resizeStop)
		}
		s.resizeMu.Unlock()

		s.inputMu.Lock()
		s.writerClosed = true
		s.inputCond.Broadcast()
		s.inputMu.Unlock()

		_ = s.screen.stopReplyReader()
		<-s.replyDone
		<-s.resizeDone
		close(s.outputDone)
	})
}

func (sub *subscription) SessionID() domain.ID { return sub.session.id }

func (sub *subscription) Generation() domain.Generation { return sub.session.generation }

func (sub *subscription) Holder() string { return sub.lease.Holder }

func (sub *subscription) InitialSnapshot() app.TerminalSnapshot { return sub.screen.InitialSnapshot() }

func (sub *subscription) ReadFrame() (app.TerminalFrame, error) { return sub.screen.ReadFrame() }

func (sub *subscription) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	s := sub.session
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	for {
		if sub.closed {
			return 0, io.EOF
		}
		if sub.rawGap {
			sub.rawGap = false
			sub.rawFailed = true
			return 0, app.ErrTerminalOutputGap
		}
		if n := sub.raw.Read(p); n > 0 {
			return n, nil
		}
		if sub.rawFailed || s.outputFinished {
			return 0, io.EOF
		}
		sub.rawCond.Wait()
	}
}

func (sub *subscription) Write(p []byte) (int, error) {
	s := sub.session
	if len(p) == 0 {
		return 0, nil
	}
	if len(p) > s.options.MaxInputBytes {
		return 0, ErrInputTooLarge
	}
	s.streamMu.Lock()
	if sub.closed || s.disposed || s.outputFinished || s.rawSubscriber != sub {
		s.streamMu.Unlock()
		return 0, io.ErrClosedPipe
	}
	s.inputMu.Lock()
	s.streamMu.Unlock()
	defer s.inputMu.Unlock()
	if s.writerClosed {
		if s.writerErr != nil {
			return 0, s.writerErr
		}
		return 0, io.ErrClosedPipe
	}
	if !s.userInput.Write(p) {
		return 0, ErrInputQueueFull
	}
	s.inputCond.Signal()
	return len(p), nil
}

func (sub *subscription) Resize(width, height int) error {
	if err := ValidateSize(width, height); err != nil {
		return err
	}
	return sub.session.resize(sub, pty.Size{Columns: width, Rows: height})
}

func (sub *subscription) Close() error {
	s := sub.session
	s.streamMu.Lock()
	if sub.closed {
		s.streamMu.Unlock()
		return nil
	}
	sub.closed = true
	sub.raw.Clear()
	if s.rawSubscriber == sub {
		s.rawSubscriber = nil
	}
	sub.rawCond.Broadcast()
	s.streamMu.Unlock()
	return sub.screen.Close()
}

func (s *Session) resize(sub *subscription, size pty.Size) error {
	request := resizeRequest{sub: sub, size: size, done: make(chan error, 1)}
	s.resizeMu.Lock()
	if s.resizeClosed {
		s.resizeMu.Unlock()
		return io.ErrClosedPipe
	}
	s.resizeRequests <- request
	s.resizeMu.Unlock()
	select {
	case err := <-request.done:
		return err
	case <-s.outputDone:
		select {
		case err := <-request.done:
			return err
		default:
			return io.ErrClosedPipe
		}
	}
}

func (s *Session) resizeLoop() {
	defer close(s.resizeDone)
	var queued resizeRequest
	hasQueued := false
	for {
		var first resizeRequest
		if hasQueued {
			first = queued
			hasQueued = false
		} else {
			select {
			case <-s.resizeStop:
				s.failPendingResizes()
				return
			case first = <-s.resizeRequests:
			}
		}
		pending := make([]resizeRequest, 1, MaxResizeBatch)
		pending[0] = first
		latest := first.size
		timer := time.NewTimer(s.options.ResizeDebounce)
		timerFired := false
		for len(pending) < MaxResizeBatch {
			select {
			case next := <-s.resizeRequests:
				if next.sub != first.sub {
					queued = next
					hasQueued = true
					timerFired = true
				} else {
					pending = append(pending, next)
					latest = next.size
				}
			case <-timer.C:
				timerFired = true
			case <-s.resizeStop:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				for _, request := range pending {
					request.done <- io.ErrClosedPipe
				}
				if hasQueued {
					queued.done <- io.ErrClosedPipe
				}
				s.failPendingResizes()
				return
			}
			if timerFired {
				break
			}
		}
		if !timerFired {
			select {
			case <-timer.C:
			case <-s.resizeStop:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				for _, request := range pending {
					request.done <- io.ErrClosedPipe
				}
				if hasQueued {
					queued.done <- io.ErrClosedPipe
				}
				s.failPendingResizes()
				return
			}
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		err := s.applyResize(first.sub, latest)
		for _, request := range pending {
			request.done <- err
		}
	}
}

func (s *Session) applyResize(sub *subscription, size pty.Size) error {
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	if sub.closed || s.rawSubscriber != sub || s.outputFinished || s.disposed {
		return io.ErrClosedPipe
	}
	if err := s.child.Resize(size); err != nil {
		return fmt.Errorf("resize PTY: %w", err)
	}
	if err := s.screen.Resize(size.Columns, size.Rows); err != nil {
		return fmt.Errorf("resize terminal emulator: %w", err)
	}
	s.size = size
	return nil
}

func (s *Session) failPendingResizes() {
	for {
		select {
		case request := <-s.resizeRequests:
			request.done <- io.ErrClosedPipe
		default:
			return
		}
	}
}
