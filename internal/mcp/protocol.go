package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const (
	// Version is the JSON-RPC version string.
	JSONRPCVersion = "2.0"

	// MaxMessageSize caps a single JSON-RPC message line (4 MiB).
	MaxMessageSize = 4 * 1024 * 1024

	// Standard JSON-RPC 2.0 error codes.
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603

	// Application-specific / MCP error codes.
	CodeApprovalRequired = -32000
	CodeLeaseConflict    = -32001
	CodeNotFound         = -32002
	CodeForbidden        = -32003
	CodeRateLimited      = -32004
)

// Message is the top-level JSON-RPC 2.0 envelope.
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError is the JSON-RPC error payload.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("rpc error (code %d): %s", e.Code, e.Message)
}

// IsRequest reports whether the message is a request expecting a response.
func (m Message) IsRequest() bool {
	return m.Method != "" && len(bytes.TrimSpace(m.ID)) > 0
}

// IsNotification reports whether the message is a notification (no id).
func (m Message) IsNotification() bool {
	return m.Method != "" && len(bytes.TrimSpace(m.ID)) == 0
}

// IsResponse reports whether the message is a success or error response.
func (m Message) IsResponse() bool {
	return m.Method == "" && len(bytes.TrimSpace(m.ID)) > 0
}

// NewResponse creates a successful JSON-RPC response.
func NewResponse(id json.RawMessage, result any) (Message, error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return Message{}, err
	}
	return Message{
		JSONRPC: JSONRPCVersion,
		ID:      id,
		Result:  raw,
	}, nil
}

// NewErrorResponse creates an error JSON-RPC response.
func NewErrorResponse(id json.RawMessage, code int, message string, data any) Message {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	return Message{
		JSONRPC: JSONRPCVersion,
		ID:      id,
		Error: &RPCError{
			Code:    code,
			Message: message,
			Data:    data,
		},
	}
}

// NewNotification creates a notification message.
func NewNotification(method string, params any) (Message, error) {
	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return Message{}, err
		}
		raw = b
	}
	return Message{
		JSONRPC: JSONRPCVersion,
		Method:  method,
		Params:  raw,
	}, nil
}

// NewRequest creates a client request message.
func NewRequest(id json.RawMessage, method string, params any) (Message, error) {
	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return Message{}, err
		}
		raw = b
	}
	return Message{
		JSONRPC: JSONRPCVersion,
		ID:      id,
		Method:  method,
		Params:  raw,
	}, nil
}

// Reader reads newline-delimited JSON-RPC messages from an underlying io.Reader.
type Reader struct {
	scanner *bufio.Scanner
}

// NewReader wraps an io.Reader with bounded buffering.
func NewReader(r io.Reader) *Reader {
	scanner := bufio.NewScanner(r)
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, MaxMessageSize)
	return &Reader{scanner: scanner}
}

// ReadMessage returns the next parsed Message or an error (such as io.EOF).
func (r *Reader) ReadMessage() (Message, error) {
	for r.scanner.Scan() {
		line := bytes.TrimSpace(r.scanner.Bytes())
		if len(line) == 0 {
			continue // skip empty lines
		}
		var msg Message
		if err := json.Unmarshal(line, &msg); err != nil {
			return Message{}, fmt.Errorf("%w: %v", errParseFailed, err)
		}
		if msg.JSONRPC != JSONRPCVersion {
			return Message{}, fmt.Errorf("invalid jsonrpc version %q", msg.JSONRPC)
		}
		return msg, nil
	}
	if err := r.scanner.Err(); err != nil {
		return Message{}, err
	}
	return Message{}, io.EOF
}

var errParseFailed = errors.New("parse error")

// IsParseError checks if an error returned by ReadMessage was caused by invalid JSON syntax.
func IsParseError(err error) bool {
	return errors.Is(err, errParseFailed)
}

// WriteMessage encodes msg and writes it with a trailing newline.
func WriteMessage(w io.Writer, msg Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}
