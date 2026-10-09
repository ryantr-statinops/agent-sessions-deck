//go:build linux

package ipc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

type oneByteReader struct{ r *bytes.Reader }

func (r oneByteReader) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return r.r.Read(p)
}

type oneByteWriter struct{ b bytes.Buffer }

func (w *oneByteWriter) Write(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return w.b.Write(p)
}

func TestFrameRoundTripHandlesPartialReadsAndWrites(t *testing.T) {
	frame := Frame{
		Version: ProtocolVersion, Type: FrameResponse, RequestID: "request-42", Revision: 8,
		Payload: []byte(`{"session":"s-1"}`),
		Error:   &WireError{Code: session.CodeConflict, Subject: "s-1", Attempt: 2, HasAttempt: true, Reason: "session already exists", Hint: "inspect the existing session"},
	}
	// A response cannot contain payload and error simultaneously.
	frame.Payload = nil
	writer := &oneByteWriter{}
	if err := WriteFrame(writer, frame); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	decoded, err := ReadFrame(oneByteReader{r: bytes.NewReader(writer.b.Bytes())})
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if decoded.Version != frame.Version || decoded.Type != frame.Type || decoded.RequestID != frame.RequestID || decoded.Revision != frame.Revision {
		t.Fatalf("decoded frame header = %+v, want %+v", decoded, frame)
	}
	if decoded.Error == nil || decoded.Error.Code != session.CodeConflict || decoded.Error.Subject != "s-1" || !decoded.Error.HasAttempt || decoded.Error.Attempt != 2 {
		t.Fatalf("decoded typed error = %+v", decoded.Error)
	}
	var typed *session.Error
	if err := decoded.Error.AsError(); !errors.As(err, &typed) || typed.Code != session.CodeConflict || !typed.HasAttempt || typed.Attempt != 2 {
		t.Fatalf("wire error conversion = %#v", err)
	}
}

func TestReadFrameRejectsOversizeBeforeReadingBody(t *testing.T) {
	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], MaxFrameBytes+1)
	frame, err := ReadFrame(bytes.NewReader(prefix[:]))
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("ReadFrame error = %v, want ErrFrameTooLarge", err)
	}
	if frame.Version != 0 || frame.Type != "" || frame.RequestID != "" || frame.Operation != "" || len(frame.Payload) != 0 || frame.Error != nil {
		t.Fatalf("oversize frame returned data: %+v", frame)
	}
}

func TestFrameRejectsInvalidVersionShapeAndUnknownFields(t *testing.T) {
	for _, body := range []string{
		`{"version":99,"type":"request","request_id":"r","operation":"list"}`,
		`{"version":1,"type":"request","operation":"list"}`,
		`{"version":1,"type":"request","request_id":"r","operation":"list","unknown":1}`,
		`{"version":1,"type":"request","request_id":"r","operation":"list"} {}`,
	} {
		var wire bytes.Buffer
		var prefix [4]byte
		binary.BigEndian.PutUint32(prefix[:], uint32(len(body)))
		wire.Write(prefix[:])
		wire.WriteString(body)
		if _, err := ReadFrame(&wire); err == nil {
			t.Fatalf("ReadFrame accepted invalid body %s", body)
		}
	}
	if err := (Frame{Version: ProtocolVersion, Type: FrameRequest, RequestID: "\n", Operation: "list"}).Validate(); !errors.Is(err, ErrInvalidFrame) {
		t.Fatalf("control-character request ID error = %v", err)
	}
}

func TestWriteFrameRejectsPayloadAboveCap(t *testing.T) {
	frame := Frame{Version: ProtocolVersion, Type: FrameRequest, RequestID: "large", Operation: "test", Payload: []byte(fmt.Sprintf("%q", strings.Repeat("x", MaxFrameBytes)))}
	if err := WriteFrame(io.Discard, frame); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("WriteFrame error = %v, want ErrFrameTooLarge", err)
	}
}

func TestErrorToWireDoesNotExposeUnstructuredCause(t *testing.T) {
	wire := ErrorToWire(errors.New("permission denied at /home/user/private-file"))
	if wire == nil || wire.Code != session.CodeUnknown || strings.Contains(wire.Reason, "private-file") || strings.Contains(wire.Hint, "private-file") {
		t.Fatalf("unstructured error leaked details into wire response: %+v", wire)
	}
}
