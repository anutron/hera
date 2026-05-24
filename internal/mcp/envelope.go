package mcp

import "encoding/json"

// CallbackEnvelope is the JSON body argus POSTs into ludwig's MCP callback
// endpoint when a registered tool is invoked.
type CallbackEnvelope struct {
	Tool    string          `json:"tool"`
	Input   json.RawMessage `json:"input"`
	Context struct {
		TaskID    string `json:"task_id"`
		SessionID string `json:"session_id"`
	} `json:"context"`
}

// ContentBlock is one MCP-native content block in a tool response.
type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// Response is the MCP-native tool response shape.
type Response struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

// TextResponse builds a successful single-block text response.
func TextResponse(text string) Response {
	return Response{Content: []ContentBlock{{Type: "text", Text: text}}}
}

// ErrorResponse builds an error response carrying a one-line message.
func ErrorResponse(message string) Response {
	return Response{
		Content: []ContentBlock{{Type: "text", Text: message}},
		IsError: true,
	}
}
