package mcp

import "encoding/json"

const (
	// ProtocolVersion is the pinned Model Context Protocol version.
	ProtocolVersion = "2024-11-05"

	// ContentTypeText is the text content type for tool results.
	ContentTypeText = "text"
)

// InitializeParams contains parameters sent by the client during initialization.
type InitializeParams struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ClientCapabilities `json:"capabilities"`
	ClientInfo      ClientInfo         `json:"clientInfo"`
}

// ClientCapabilities describes features the client supports.
type ClientCapabilities struct {
	Experimental map[string]any `json:"experimental,omitempty"`
	Sampling     map[string]any `json:"sampling,omitempty"`
	Roots        map[string]any `json:"roots,omitempty"`
}

// ClientInfo identifies the connecting AI client.
type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// InitializeResult is returned by the server upon initialization.
type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	ServerInfo      ServerInfo         `json:"serverInfo"`
	Instructions    string             `json:"instructions,omitempty"`
}

// ServerCapabilities lists the MCP features supported by ASD.
type ServerCapabilities struct {
	Tools     *ToolsCapability     `json:"tools,omitempty"`
	Resources *ResourcesCapability `json:"resources,omitempty"`
	Prompts   *PromptsCapability   `json:"prompts,omitempty"`
}

// ToolsCapability indicates tool execution support.
type ToolsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// ResourcesCapability indicates resource read support.
type ResourcesCapability struct {
	Subscribe   bool `json:"subscribe,omitempty"`
	ListChanged bool `json:"listChanged,omitempty"`
}

// PromptsCapability indicates prompt template support.
type PromptsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// ServerInfo identifies the ASD MCP server implementation.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Tool describes a capability exposed to AI assistants.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema ToolSchema      `json:"inputSchema"`
	Annotations *ToolAnnotation `json:"annotations,omitempty"`
}

// ToolSchema represents JSON Schema for tool input arguments.
type ToolSchema struct {
	Type       string                 `json:"type"`
	Properties map[string]PropertyDef `json:"properties,omitempty"`
	Required   []string               `json:"required,omitempty"`
}

// PropertyDef describes a single argument field schema.
type PropertyDef struct {
	Type        string   `json:"type"`
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Default     any      `json:"default,omitempty"`
}

// ToolAnnotation indicates behavioral attributes of a tool.
type ToolAnnotation struct {
	ReadOnly    bool `json:"readOnly,omitempty"`
	Destructive bool `json:"destructive,omitempty"`
}

// ListToolsResult is the response payload for tools/list.
type ListToolsResult struct {
	Tools []Tool `json:"tools"`
}

// CallToolParams contains the arguments for tools/call.
type CallToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// CallToolResult is the response payload for tools/call.
type CallToolResult struct {
	Content []ContentItem `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// ContentItem holds a piece of structured or text content in tool results.
type ContentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// NewTextResult creates a successful single-text-content result.
func NewTextResult(text string) CallToolResult {
	return CallToolResult{
		Content: []ContentItem{
			{Type: ContentTypeText, Text: text},
		},
	}
}

// NewJSONResult serializes data as indented JSON text content.
func NewJSONResult(data any) (CallToolResult, error) {
	bytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return CallToolResult{}, err
	}
	return NewTextResult(string(bytes)), nil
}

// NewErrorResult creates an error tool result.
func NewErrorResult(msg string) CallToolResult {
	return CallToolResult{
		Content: []ContentItem{
			{Type: ContentTypeText, Text: msg},
		},
		IsError: true,
	}
}
