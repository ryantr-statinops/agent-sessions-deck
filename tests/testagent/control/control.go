//go:build linux

// Package control defines the wire protocol between the Stage 01 fake agent
// fixture and the tests that drive it.
//
// The fixture inherits a single full-duplex control channel on fd 3 (see
// ControlFD). Both directions carry newline-delimited JSON: the fixture writes
// Event values to fd 3 and reads Command values from fd 3. The fixture's
// stdin/stdout/stderr stay bound to the PTY so that terminal bytes and control
// bytes never share a file descriptor.
//
// Neither this package nor the fixture contains session or runtime logic; it
// only describes what a test needs in order to observe a process without
// sleeping on it.
package control

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// ControlFD is the inherited file descriptor number carrying the full-duplex
// control channel.
const ControlFD = 3

// IdentFD is the file descriptor number a spawned helper process uses to
// report its identity back to the fixture that spawned it. Helpers never write
// to the control channel; only the top-level fixture does.
const IdentFD = 3

// Event names emitted by the fixture.
const (
	EventReady   = "ready"
	EventStarted = "started"
	EventIdent   = "ident"
	EventLine    = "line"
	EventSize    = "size"
	EventQuery   = "query"
	EventSignal  = "signal"
	EventFrame   = "frame"
	EventSample  = "sample"
	EventFlood   = "flood"
	EventExit    = "exit"
)

// Command names accepted by the fixture.
const (
	CmdGo      = "go"
	CmdExit    = "exit"
	CmdStop    = "stop"
	CmdGetsize = "getsize"
	CmdWrite   = "write"
	CmdQuery   = "query"
)

// Event is implemented by every value the fixture can emit. EventName returns
// the discriminator used in the JSON "event" field.
type Event interface {
	EventName() string
}

// Ready is the first event of every run. It carries the fixture's own identity
// and the fully resolved configuration the tests need to assert on.
type Ready struct {
	PID   int    `json:"pid"`
	PGID  int    `json:"pgid"`
	SID   int    `json:"sid"`
	Mode  string `json:"mode"`
	Label string `json:"label"`
	Arg   string `json:"arg"`
}

// EventName implements Event.
func (*Ready) EventName() string { return EventReady }

// Started is emitted once a mode has passed any start-up gate and is running.
// Modes that gate on start-up (slow-start) withhold it until released.
type Started struct {
	Mode string `json:"mode"`
}

// EventName implements Event.
func (*Started) EventName() string { return EventStarted }

// Ident reports a process identity. Role is "child" or "grandchild". The
// fixture resolves PPID and PGID from the kernel, not from assumptions.
type Ident struct {
	Role string `json:"role"`
	PID  int    `json:"pid"`
	PPID int    `json:"ppid"`
	PGID int    `json:"pgid"`
}

// EventName implements Event.
func (*Ident) EventName() string { return EventIdent }

// Line is one complete line of terminal input, newline stripped.
type Line struct {
	N    int    `json:"n"`
	Data string `json:"data"`
}

// EventName implements Event.
func (*Line) EventName() string { return EventLine }

// Size reports a terminal size observation. Reason is "start", "winch" or
// "getsize".
type Size struct {
	Rows   int    `json:"rows"`
	Cols   int    `json:"cols"`
	Reason string `json:"reason"`
}

// EventName implements Event.
func (*Size) EventName() string { return EventSize }

// Query reports terminal query/response bytes observed on stdin.
type Query struct {
	Kind string `json:"kind"`
	Raw  string `json:"raw"`
}

// EventName implements Event.
func (*Query) EventName() string { return EventQuery }

// Signal reports a caught signal and how many times it has been seen.
type Signal struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// EventName implements Event.
func (*Signal) EventName() string { return EventSignal }

// Frame reports a full-screen redraw.
type Frame struct {
	N    int `json:"n"`
	Rows int `json:"rows"`
	Cols int `json:"cols"`
}

// EventName implements Event.
func (*Frame) EventName() string { return EventFrame }

// Sample reports a fixed Unicode payload written to the terminal.
type Sample struct {
	Name string `json:"name"`
	Hex  string `json:"hex"`
	Size int    `json:"size"`
}

// EventName implements Event.
func (*Sample) EventName() string { return EventSample }

// Flood reports the total of a bounded output burst.
type Flood struct {
	Bytes     int `json:"bytes"`
	Lines     int `json:"lines"`
	RecordLen int `json:"recordlen"`
}

// EventName implements Event.
func (*Flood) EventName() string { return EventFlood }

// Exit is always the last event. Code is meaningful when Signal is empty;
// Signal is set when the fixture was terminated by a signal.
type Exit struct {
	Code   int    `json:"code"`
	Signal string `json:"signal"`
	Reason string `json:"reason"`
}

// EventName implements Event.
func (*Exit) EventName() string { return EventExit }

// IdentReport is the single JSON line a helper process writes to IdentFD on
// start-up. Helpers have no control channel, so this is a plain line rather
// than a control.Event.
type IdentReport struct {
	Role string `json:"role"`
	PID  int    `json:"pid"`
	PPID int    `json:"ppid"`
	PGID int    `json:"pgid"`
}

// Command is a request sent to the fixture over the control channel.
type Command struct {
	Cmd  string `json:"cmd"`
	Code int    `json:"code,omitempty"`
	Kind string `json:"kind,omitempty"`
	Data string `json:"data,omitempty"`
}

// encoder serialises a value as one JSON line. It is safe for concurrent use
// so the fixture can emit from its signal handler's goroutine and its main
// loop concurrently.
type encoder struct {
	mu sync.Mutex
	w  io.Writer
}

// Emit writes v as a single newline-delimited JSON record. The "event"
// discriminator is injected from the value's EventName, so a struct can never
// be emitted with a missing or stale discriminator.
func (e *encoder) Emit(v any) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	name := eventName(v)
	body, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("control: marshal %s: %w", name, err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return fmt.Errorf("control: reshape %s: %w", name, err)
	}
	disc, err := json.Marshal(name)
	if err != nil {
		return fmt.Errorf("control: marshal %s: %w", name, err)
	}
	fields["event"] = disc
	line, err := json.Marshal(fields)
	if err != nil {
		return fmt.Errorf("control: marshal %s: %w", name, err)
	}
	line = append(line, '\n')
	if _, err := e.w.Write(line); err != nil {
		return fmt.Errorf("control: write %s: %w", name, err)
	}
	return nil
}

func eventName(v any) string {
	if e, ok := v.(Event); ok {
		return e.EventName()
	}
	return fmt.Sprintf("%T", v)
}

// Emitter is the write half of the control channel.
type Emitter struct {
	enc encoder
}

// NewEmitter returns an Emitter that writes events to w.
func NewEmitter(w io.Writer) *Emitter {
	return &Emitter{enc: encoder{w: w}}
}

// Emit writes v as one newline-delimited JSON record.
func (e *Emitter) Emit(v Event) error { return e.enc.Emit(v) }

// EmitLine writes an arbitrary value as one newline-delimited JSON record.
func (e *Emitter) EmitLine(v any) error { return e.enc.Emit(v) }

// Reader decodes bounded newline-delimited JSON records. Partial records survive
// transport timeouts so a later read resumes from the same stream position.
type Reader struct {
	br      *bufio.Reader
	pending []byte
}

// NewReader returns a Reader that decodes from r. Records are bounded so a
// misbehaving peer cannot exhaust memory.
func NewReader(r io.Reader) *Reader { return &Reader{br: bufio.NewReader(r)} }

// ErrLineTooLong is returned when a record exceeds the size bound.
var ErrLineTooLong = errors.New("control: record exceeds size bound")

const maxRecordSize = 4 * 1024 * 1024

// Read decodes the next record. It returns io.EOF at end of stream.
func (r *Reader) Read() (json.RawMessage, error) {
	for {
		part, err := r.br.ReadSlice('\n')
		if len(r.pending)+len(part) > maxRecordSize {
			r.pending = nil
			return nil, ErrLineTooLong
		}
		r.pending = append(r.pending, part...)
		if err == nil {
			raw := r.pending[:len(r.pending)-1]
			result := append(json.RawMessage(nil), raw...)
			r.pending = nil
			return result, nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(err, io.EOF) && len(r.pending) > 0 {
			result := append(json.RawMessage(nil), r.pending...)
			r.pending = nil
			return result, nil
		}
		return nil, err
	}
}

// Decode reads the next record and decodes it into a concrete event type based
// on the "event" discriminator.
func (r *Reader) Decode() (Event, error) {
	raw, err := r.Read()
	if err != nil {
		return nil, err
	}
	var probe struct {
		Event string `json:"event"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("control: probe discriminator: %w", err)
	}
	ev := newEvent(probe.Event)
	if ev == nil {
		return nil, fmt.Errorf("control: unknown event %q", probe.Event)
	}
	if err := json.Unmarshal(raw, ev); err != nil {
		return nil, fmt.Errorf("control: decode %s: %w", probe.Event, err)
	}
	return ev, nil
}

// DecodeCommand reads the next record and decodes it as a Command.
func (r *Reader) DecodeCommand() (Command, error) {
	raw, err := r.Read()
	if err != nil {
		return Command{}, err
	}
	var c Command
	if err := json.Unmarshal(raw, &c); err != nil {
		return Command{}, fmt.Errorf("control: decode command: %w", err)
	}
	if c.Cmd == "" {
		return Command{}, errors.New("control: command missing cmd field")
	}
	return c, nil
}

func newEvent(name string) Event {
	switch name {
	case EventReady:
		return new(Ready)
	case EventStarted:
		return new(Started)
	case EventIdent:
		return new(Ident)
	case EventLine:
		return new(Line)
	case EventSize:
		return new(Size)
	case EventQuery:
		return new(Query)
	case EventSignal:
		return new(Signal)
	case EventFrame:
		return new(Frame)
	case EventSample:
		return new(Sample)
	case EventFlood:
		return new(Flood)
	case EventExit:
		return new(Exit)
	default:
		return nil
	}
}

// Conn is the fixture side of the control channel.
type Conn struct {
	Reader *Reader
	*Emitter
}

// Dial binds a Conn to w for events and r for commands.
func Dial(r io.Reader, w io.Writer) *Conn {
	return &Conn{Reader: NewReader(r), Emitter: NewEmitter(w)}
}
