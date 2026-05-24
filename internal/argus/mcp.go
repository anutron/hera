package argus

import (
	"context"
	"net/url"
)

// MCPTool is the registration payload for POST /api/mcp/tools.
type MCPTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
	CallbackURL string         `json:"callback_url"`
	AuthHeader  string         `json:"auth_header"`
}

// MCPToolResponse is argus's response shape on registration.
type MCPToolResponse struct {
	Name  string `json:"name"`
	Scope string `json:"scope"`
}

// RegisterTool POSTs an MCP tool registration. Re-POSTing with the same
// name is idempotent (refreshes the heartbeat) per the substrate contract.
func (c *Client) RegisterTool(ctx context.Context, tool MCPTool) (*MCPToolResponse, error) {
	var resp MCPToolResponse
	if _, err := c.doJSON(ctx, "POST", "/api/mcp/tools", tool, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// UnregisterTool DELETEs an MCP tool registration. Idempotent – deleting a
// missing name returns 200 OK per the substrate contract.
func (c *Client) UnregisterTool(ctx context.Context, name string) error {
	_, err := c.doJSON(ctx, "DELETE", "/api/mcp/tools/"+url.PathEscape(name), nil, nil)
	return err
}
