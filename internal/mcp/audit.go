package mcp

import (
	"encoding/json"
	"io"
	"sync"
	"time"
)

// AuditEvent represents a logged mutation attempt.
type AuditEvent struct {
	Timestamp time.Time `json:"timestamp"`
	Tool      string    `json:"tool"`
	RequestID string    `json:"request_id,omitempty"`
	SessionID string    `json:"session_id,omitempty"`
	Attempt   int       `json:"attempt,omitempty"`
	Status    string    `json:"status"` // "SUCCESS" or "ERROR"
	Error     string    `json:"error,omitempty"`
}

// AuditLogger writes structured audit events to an io.Writer (e.g. stderr).
type AuditLogger struct {
	mu  sync.Mutex
	out io.Writer
}

// NewAuditLogger creates a logger writing to out.
func NewAuditLogger(out io.Writer) *AuditLogger {
	return &AuditLogger{out: out}
}

// Log records an audit event safely without logging any secrets, prompts or raw bytes.
func (l *AuditLogger) Log(event AuditEvent) {
	if l == nil || l.out == nil {
		return
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	data = append(data, '\n')

	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.out.Write(data)
}
