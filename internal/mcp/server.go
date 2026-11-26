package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// Server implements an MCP JSON-RPC 2.0 server over stdio.
type Server struct {
	name        string
	version     string
	mu          sync.RWMutex
	initialized bool
	tools       map[string]registeredTool
	toolOrder   []string
}

type registeredTool struct {
	tool    Tool
	handler ToolHandler
}

// ToolHandler handles execution of an MCP tool.
type ToolHandler func(ctx context.Context, args json.RawMessage) (CallToolResult, error)

// ServerOptions configures the MCP server.
type ServerOptions struct {
	Name    string
	Version string
}

// NewServer initializes an MCP server.
func NewServer(opts ServerOptions) *Server {
	if opts.Name == "" {
		opts.Name = "agent-sessions-deck"
	}
	if opts.Version == "" {
		opts.Version = "dev"
	}
	return &Server{
		name:      opts.Name,
		version:   opts.Version,
		tools:     make(map[string]registeredTool),
		toolOrder: make([]string, 0),
	}
}

// RegisterTool registers an available tool on the server.
func (s *Server) RegisterTool(tool Tool, handler ToolHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.tools[tool.Name]; !exists {
		s.toolOrder = append(s.toolOrder, tool.Name)
	}
	s.tools[tool.Name] = registeredTool{tool: tool, handler: handler}
}

// IsInitialized returns whether initialization handshake completed.
func (s *Server) IsInitialized() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.initialized
}

// HandleMessage routes an incoming JSON-RPC message to the proper handler.
func (s *Server) HandleMessage(ctx context.Context, msg Message) (Message, bool, error) {
	if msg.IsNotification() {
		switch msg.Method {
		case "notifications/initialized":
			s.mu.Lock()
			s.initialized = true
			s.mu.Unlock()
			return Message{}, false, nil
		default:
			// Unknown notification: per JSON-RPC, notifications produce no response
			return Message{}, false, nil
		}
	}

	if !msg.IsRequest() {
		return NewErrorResponse(msg.ID, CodeInvalidRequest, "message is neither a valid request nor notification", nil), true, nil
	}

	switch msg.Method {
	case "initialize":
		var params InitializeParams
		if len(msg.Params) > 0 {
			if err := json.Unmarshal(msg.Params, &params); err != nil {
				return NewErrorResponse(msg.ID, CodeInvalidParams, "malformed initialize params", err.Error()), true, nil
			}
		}

		res := InitializeResult{
			ProtocolVersion: ProtocolVersion,
			Capabilities: ServerCapabilities{
				Tools: &ToolsCapability{ListChanged: false},
			},
			ServerInfo: ServerInfo{
				Name:    s.name,
				Version: s.version,
			},
			Instructions: "ASD Control Plane MCP server. Query and manage coding-agent sessions with bounded, scoped tools.",
		}
		resp, err := NewResponse(msg.ID, res)
		if err != nil {
			return NewErrorResponse(msg.ID, CodeInternalError, "failed to marshal initialize result", err.Error()), true, nil
		}
		return resp, true, nil

	case "ping":
		resp, _ := NewResponse(msg.ID, map[string]any{})
		return resp, true, nil

	case "tools/list":
		s.mu.RLock()
		tools := make([]Tool, 0, len(s.toolOrder))
		for _, name := range s.toolOrder {
			reg := s.tools[name]
			tools = append(tools, reg.tool)
		}
		s.mu.RUnlock()

		resp, err := NewResponse(msg.ID, ListToolsResult{Tools: tools})
		if err != nil {
			return NewErrorResponse(msg.ID, CodeInternalError, "failed to marshal tools list", err.Error()), true, nil
		}
		return resp, true, nil

	case "tools/call":
		var params CallToolParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			return NewErrorResponse(msg.ID, CodeInvalidParams, "malformed tools/call params", err.Error()), true, nil
		}
		if params.Name == "" {
			return NewErrorResponse(msg.ID, CodeInvalidParams, "tools/call requires 'name'", nil), true, nil
		}

		s.mu.RLock()
		reg, exists := s.tools[params.Name]
		s.mu.RUnlock()

		if !exists {
			return NewErrorResponse(msg.ID, CodeMethodNotFound, fmt.Sprintf("tool %q not found", params.Name), nil), true, nil
		}

		// Execute tool handler
		res, err := reg.handler(ctx, params.Arguments)
		if err != nil {
			return NewErrorResponse(msg.ID, CodeInternalError, err.Error(), nil), true, nil
		}
		resp, err := NewResponse(msg.ID, res)
		if err != nil {
			return NewErrorResponse(msg.ID, CodeInternalError, "failed to marshal tool result", err.Error()), true, nil
		}
		return resp, true, nil

	default:
		return NewErrorResponse(msg.ID, CodeMethodNotFound, fmt.Sprintf("method %q not found", msg.Method), nil), true, nil
	}
}

// Serve reads messages from in and writes responses to out until EOF or ctx cancellation.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	reader := NewReader(in)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		msg, err := reader.ReadMessage()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			if IsParseError(err) {
				_ = WriteMessage(out, NewErrorResponse(nil, CodeParseError, "parse error: malformed JSON", err.Error()))
				continue
			}
			return err
		}

		resp, hasResp, handleErr := s.HandleMessage(ctx, msg)
		if handleErr != nil {
			_ = WriteMessage(out, NewErrorResponse(msg.ID, CodeInternalError, handleErr.Error(), nil))
			continue
		}
		if hasResp {
			if err := WriteMessage(out, resp); err != nil {
				return err
			}
		}
	}
}
