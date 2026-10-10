//go:build linux

package ipc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

const (
	DedupeEntryLimit = 2048
	DedupeRetention  = 10 * time.Minute
)

// Dispatcher translates typed IPC requests to the application boundary. Mutating
// request IDs are idempotent for the retention window and bind to the exact
// operation plus payload.
type Dispatcher struct {
	client app.Client
	now    func() time.Time

	mu      sync.Mutex
	entries map[string]*dedupeEntry
}

type dedupeEntry struct {
	fingerprint [32]byte
	done        chan struct{}
	response    Frame
	completedAt time.Time
}

// NewDispatcher constructs the online service adapter.
func NewDispatcher(client app.Client) (*Dispatcher, error) {
	if client == nil {
		return nil, errors.New("IPC dispatcher requires an application client")
	}
	return &Dispatcher{client: client, now: func() time.Time { return time.Now().UTC() }, entries: make(map[string]*dedupeEntry)}, nil
}

// Handle executes one request and returns a response carrying the same request
// ID and the authoritative revision, if the application result has one.
func (d *Dispatcher) Handle(ctx context.Context, request Frame) Frame {
	response := Frame{Version: ProtocolVersion, Type: FrameResponse, RequestID: request.RequestID}
	if err := request.Validate(); err != nil {
		response.Error = ErrorToWire(session.WrapError(session.CodeInvalidConfiguration, request.RequestID, "IPC request is invalid: "+err.Error(), "send a valid versioned request", err))
		return response
	}
	if request.Type != FrameRequest {
		response.Error = ErrorToWire(session.NewError(session.CodeInvalidConfiguration, request.RequestID, "expected an IPC request frame", "send a request frame"))
		return response
	}
	if request.Operation == "open" {
		response.Error = ErrorToWire(session.NewError(session.CodeUnsupported, request.RequestID, "open requires a streaming connection", "use the owner/client terminal stream operation"))
		return response
	}
	if isMutation(request.Operation) {
		cached, err := d.once(ctx, request, func() Frame { return d.execute(ctx, request) })
		if err != nil {
			response.Error = ErrorToWire(session.WrapError(session.CodeOwnerUnavailable, request.RequestID, "the request is still being processed or the owner is shutting down", "reconnect and retry the same request ID", err))
			return response
		}
		return cached
	}
	return d.execute(ctx, request)
}

func (d *Dispatcher) once(ctx context.Context, request Frame, execute func() Frame) (Frame, error) {
	fingerprint := requestFingerprint(request)
	d.mu.Lock()
	d.pruneLocked()
	if entry := d.entries[request.RequestID]; entry != nil {
		if entry.fingerprint != fingerprint {
			d.mu.Unlock()
			conflict := Frame{Version: ProtocolVersion, Type: FrameResponse, RequestID: request.RequestID,
				Error: ErrorToWire(session.NewError(session.CodeConflict, request.RequestID, "request ID was reused for a different operation or payload", "generate a new request ID for a different operation"))}
			return conflict, nil
		}
		done := entry.done
		d.mu.Unlock()
		select {
		case <-ctx.Done():
			return Frame{}, ctx.Err()
		case <-done:
			d.mu.Lock()
			response := cloneFrame(entry.response)
			d.mu.Unlock()
			return response, nil
		}
	}
	if len(d.entries) >= DedupeEntryLimit {
		d.mu.Unlock()
		return Frame{}, errors.New("idempotency cache is full of unexpired requests")
	}
	entry := &dedupeEntry{fingerprint: fingerprint, done: make(chan struct{})}
	d.entries[request.RequestID] = entry
	d.mu.Unlock()

	response := execute()
	d.mu.Lock()
	entry.response = cloneFrame(response)
	entry.completedAt = d.now()
	close(entry.done)
	d.mu.Unlock()
	return response, nil
}

func (d *Dispatcher) pruneLocked() {
	cutoff := d.now().Add(-DedupeRetention)
	for id, entry := range d.entries {
		select {
		case <-entry.done:
			if entry.completedAt.Before(cutoff) {
				delete(d.entries, id)
			}
		default:
		}
	}
}

func requestFingerprint(request Frame) [32]byte {
	h := sha256.New()
	_, _ = io.WriteString(h, request.Operation)
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(request.Payload)
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

func cloneFrame(frame Frame) Frame {
	frame.Payload = bytes.Clone(frame.Payload)
	if frame.Error != nil {
		copy := *frame.Error
		frame.Error = &copy
	}
	return frame
}

func isMutation(operation string) bool {
	switch operation {
	case "create", "detach", "rename", "restart", "stop", "kill", "delete":
		return true
	default:
		return false
	}
}

func decodePayload(raw json.RawMessage, target any) error {
	if len(raw) == 0 {
		return errors.New("request payload is required")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode request payload: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("request payload contains trailing JSON")
	}
	return nil
}

func (d *Dispatcher) execute(ctx context.Context, request Frame) Frame {
	response := Frame{Version: ProtocolVersion, Type: FrameResponse, RequestID: request.RequestID}
	var result any
	var err error
	switch request.Operation {
	case "scan":
		var req app.ScanRequest
		err = decodePayload(request.Payload, &req)
		if err == nil {
			result, err = d.client.Scan(ctx, req)
		}
	case "list":
		var req app.ListRequest
		err = decodePayload(request.Payload, &req)
		if err == nil {
			result, err = d.client.List(ctx, req)
		}
	case "inspect":
		var req app.GetRequest
		err = decodePayload(request.Payload, &req)
		if err == nil {
			result, err = d.client.Get(ctx, req)
		}
	case "create":
		var req app.CreateRequest
		err = decodePayload(request.Payload, &req)
		if err == nil {
			result, err = d.client.Create(ctx, req)
		}
	case "detach":
		var req app.DetachRequest
		err = decodePayload(request.Payload, &req)
		if err == nil {
			result, err = d.client.Detach(ctx, req)
		}
	case "rename":
		var req app.RenameRequest
		err = decodePayload(request.Payload, &req)
		if err == nil {
			result, err = d.client.Rename(ctx, req)
		}
	case "restart":
		var req app.RestartRequest
		err = decodePayload(request.Payload, &req)
		if err == nil {
			result, err = d.client.Restart(ctx, req)
		}
	case "stop":
		var req app.StopRequest
		err = decodePayload(request.Payload, &req)
		if err == nil {
			result, err = d.client.Stop(ctx, req)
		}
	case "kill":
		var req app.KillRequest
		err = decodePayload(request.Payload, &req)
		if err == nil {
			result, err = d.client.Kill(ctx, req)
		}
	case "delete":
		var req app.DeleteRequest
		err = decodePayload(request.Payload, &req)
		if err == nil {
			result, err = d.client.Delete(ctx, req)
		}
	default:
		err = session.NewError(session.CodeUnsupported, request.Operation, "IPC operation is not supported", "use a documented Stage 06 operation")
	}
	if err != nil {
		response.Error = ErrorToWire(err)
		return response
	}
	payload, err := json.Marshal(result)
	if err != nil {
		response.Error = ErrorToWire(session.WrapError(session.CodeUnknown, request.Operation, "the application result could not be encoded", "inspect the owner build and retry", err))
		return response
	}
	response.Payload = payload
	response.Revision = resultRevision(result)
	return response
}

func resultRevision(result any) uint64 {
	switch value := result.(type) {
	case app.ScanResult:
		return value.Revision
	case app.ListResult:
		return value.Snapshot.Revision
	case app.GetResult:
		return value.Revision
	case app.CreateResult:
		return value.Revision
	case app.DetachResult:
		return value.Revision
	case app.RenameResult:
		return value.Revision
	case app.RestartResult:
		return value.Revision
	case app.StopResult:
		return value.Revision
	case app.KillResult:
		return value.Revision
	case app.DeleteResult:
		return value.Revision
	default:
		return 0
	}
}
