package mcp

// ProtocolVersion is the MCP specification version implemented by this server.
const ProtocolVersion = "2024-11-05"

// ServerInfo identifies this MCP server implementation.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Capabilities advertises supported MCP features.
type Capabilities struct {
	Resources *ResourceCapability `json:"resources,omitempty"`
	Tools     *ToolCapability     `json:"tools,omitempty"`
}

// ResourceCapability describes resource serving capabilities.
type ResourceCapability struct {
	ListChanged bool `json:"listChanged"`
}

// ToolCapability describes tool invocation capabilities.
type ToolCapability struct {
	ListChanged bool `json:"listChanged"`
}

// Request represents a JSON-RPC 2.0 request.
type Request struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      any         `json:"id,omitempty"`
	Method  string      `json:"method"`
	Params  any         `json:"params,omitempty"`
}

// Response represents a JSON-RPC 2.0 response.
type Response struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      any         `json:"id,omitempty"`
	Result  any         `json:"result,omitempty"`
	Error   *Error      `json:"error,omitempty"`
}

// Error represents a JSON-RPC 2.0 error object.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Standard JSON-RPC error codes.
const (
	ErrCodeParse      = -32700
	ErrCodeInvalidReq = -32600
	ErrCodeNoMethod   = -32601
	ErrCodeBadParams  = -32602
	ErrCodeInternal   = -32603
)

// Resource describes an MCP-served resource.
type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

// ResourceContent holds the content for a resource read response.
type ResourceContent struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType"`
	Text     string `json:"text"`
}

// Tool describes an invocable MCP tool.
type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	InputSchema any    `json:"inputSchema"`
}

// ToolContent represents a single content item returned by a tool call.
type ToolContent struct {
	Type string `json:"type"` // always "text" for text responses
	Text string `json:"text"`
}

// ToolResult contains the output from a tool invocation.
type ToolResult struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}
