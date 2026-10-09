//go:build linux

package ipc

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

const (
	ProtocolVersion   = 1
	MaxFrameBytes     = 1 << 20
	MaxRequestIDBytes = 128
)

var (
	ErrProtocolVersion = errors.New("unsupported IPC protocol version")
	ErrInvalidFrame    = errors.New("invalid IPC frame")
	ErrFrameTooLarge   = errors.New("IPC frame exceeds size limit")
)

// FrameType selects one control or terminal-stream message.
type FrameType string

const (
	FrameHello          FrameType = "hello"
	FrameHelloAck       FrameType = "hello_ack"
	FrameRequest        FrameType = "request"
	FrameResponse       FrameType = "response"
	FrameTerminalBytes  FrameType = "terminal_bytes"
	FrameTerminalScreen FrameType = "terminal_screen"
	FrameTerminalInput  FrameType = "terminal_input"
	FrameTerminalResize FrameType = "terminal_resize"
	FrameTerminalClose  FrameType = "terminal_close"
)

// Frame is the versioned wire envelope. Payload is a single JSON value; terminal
// byte payloads are base64-encoded by encoding/json and remain subject to the
// same hard frame cap.
type Frame struct {
	Version   int             `json:"version"`
	Type      FrameType       `json:"type"`
	RequestID string          `json:"request_id,omitempty"`
	Operation string          `json:"operation,omitempty"`
	Instance  string          `json:"instance_id,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	Revision  uint64          `json:"revision,omitempty"`
	Error     *WireError      `json:"error,omitempty"`
}

// WireError preserves the CLI-contract fields without exposing wrapped causes
// that may contain paths or process details unsuitable for clients.
type WireError struct {
	Code       session.Code       `json:"code"`
	Subject    string             `json:"subject,omitempty"`
	Attempt    session.Generation `json:"attempt,omitempty"`
	HasAttempt bool               `json:"has_attempt,omitempty"`
	Reason     string             `json:"reason"`
	Hint       string             `json:"hint"`
}

func ErrorToWire(err error) *WireError {
	if err == nil {
		return nil
	}
	var typed *session.Error
	if errors.As(err, &typed) {
		return &WireError{Code: typed.Code, Subject: typed.Subject, Attempt: typed.Attempt, HasAttempt: typed.HasAttempt, Reason: typed.Reason, Hint: typed.Hint}
	}
	return &WireError{Code: session.CodeOf(err), Reason: "the request could not be completed", Hint: "inspect owner diagnostics and retry when the cause is resolved"}
}

func (e *WireError) AsError() error {
	if e == nil {
		return nil
	}
	if !e.Code.Valid() {
		return session.NewError(session.CodeUnknown, e.Subject, e.Reason, e.Hint)
	}
	out := session.NewError(e.Code, e.Subject, e.Reason, e.Hint)
	if e.HasAttempt {
		out.ForAttempt(e.Attempt)
	}
	return out
}

// Validate checks the framing envelope before dispatch or client rendering.
func (f Frame) Validate() error {
	if f.Version != ProtocolVersion {
		return fmt.Errorf("%w: got %d, want %d", ErrProtocolVersion, f.Version, ProtocolVersion)
	}
	switch f.Type {
	case FrameHello, FrameHelloAck, FrameRequest, FrameResponse, FrameTerminalBytes, FrameTerminalScreen, FrameTerminalInput, FrameTerminalResize, FrameTerminalClose:
	default:
		return fmt.Errorf("%w: unknown type %q", ErrInvalidFrame, f.Type)
	}
	if f.RequestID != "" && !validRequestID(f.RequestID) {
		return fmt.Errorf("%w: request ID must be 1–%d printable UTF-8 bytes", ErrInvalidFrame, MaxRequestIDBytes)
	}
	if (f.Type == FrameRequest || f.Type == FrameResponse || f.Type == FrameHello || f.Type == FrameHelloAck) && f.RequestID == "" {
		return fmt.Errorf("%w: %s frame requires request_id", ErrInvalidFrame, f.Type)
	}
	if f.Type == FrameRequest && f.Operation == "" {
		return fmt.Errorf("%w: request frame requires operation", ErrInvalidFrame)
	}
	if f.Type == FrameResponse && f.Error != nil && len(f.Payload) != 0 {
		return fmt.Errorf("%w: response cannot contain both payload and error", ErrInvalidFrame)
	}
	if len(f.Payload) != 0 && !json.Valid(f.Payload) {
		return fmt.Errorf("%w: payload is not valid JSON", ErrInvalidFrame)
	}
	if f.Error != nil {
		if !f.Error.Code.Valid() || strings.TrimSpace(f.Error.Reason) == "" || strings.TrimSpace(f.Error.Hint) == "" {
			return fmt.Errorf("%w: typed error needs a known code, reason, and hint", ErrInvalidFrame)
		}
	}
	return nil
}

func validRequestID(id string) bool {
	if len(id) == 0 || len(id) > MaxRequestIDBytes || !utf8.ValidString(id) || strings.TrimSpace(id) == "" {
		return false
	}
	for _, r := range id {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// WriteFrame writes one four-byte big-endian length-prefixed JSON frame.
func WriteFrame(w io.Writer, frame Frame) error {
	if err := frame.Validate(); err != nil {
		return err
	}
	body, err := json.Marshal(frame)
	if err != nil {
		return fmt.Errorf("encode IPC frame: %w", err)
	}
	if len(body) == 0 || len(body) > MaxFrameBytes {
		return fmt.Errorf("%w: encoded length %d, maximum %d", ErrFrameTooLarge, len(body), MaxFrameBytes)
	}
	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(len(body)))
	if err := writeAll(w, prefix[:]); err != nil {
		return fmt.Errorf("write IPC frame length: %w", err)
	}
	if err := writeAll(w, body); err != nil {
		return fmt.Errorf("write IPC frame body: %w", err)
	}
	return nil
}

// ReadFrame reads one bounded frame without allocating from an untrusted length
// until that length has been checked.
func ReadFrame(r io.Reader) (Frame, error) {
	var prefix [4]byte
	if _, err := io.ReadFull(r, prefix[:]); err != nil {
		return Frame{}, err
	}
	n := binary.BigEndian.Uint32(prefix[:])
	if n == 0 || n > MaxFrameBytes {
		return Frame{}, fmt.Errorf("%w: declared length %d, maximum %d", ErrFrameTooLarge, n, MaxFrameBytes)
	}
	body := make([]byte, int(n))
	if _, err := io.ReadFull(r, body); err != nil {
		return Frame{}, fmt.Errorf("read IPC frame body: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var frame Frame
	if err := decoder.Decode(&frame); err != nil {
		return Frame{}, fmt.Errorf("%w: decode JSON: %v", ErrInvalidFrame, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return Frame{}, fmt.Errorf("%w: trailing JSON data", ErrInvalidFrame)
	}
	if err := frame.Validate(); err != nil {
		return Frame{}, err
	}
	return frame, nil
}

func writeAll(w io.Writer, data []byte) error {
	for len(data) != 0 {
		n, err := w.Write(data)
		if n > 0 {
			data = data[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}
