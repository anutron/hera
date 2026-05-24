package mcp

import (
	"context"
	"encoding/json"
)

// Handler implements a ludwig MCP tool's behavior. The runtime decodes the
// callback envelope, then calls Handle with the parsed input. Returning a
// Response with IsError=true surfaces a tool error to the MCP client.
type Handler interface {
	Handle(ctx context.Context, input json.RawMessage) Response
}

// HandlerFunc adapts an anonymous function to the Handler interface.
type HandlerFunc func(ctx context.Context, input json.RawMessage) Response

// Handle implements Handler.
func (f HandlerFunc) Handle(ctx context.Context, input json.RawMessage) Response {
	return f(ctx, input)
}
