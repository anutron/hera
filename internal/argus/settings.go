package argus

import (
	"context"
	"net/url"
)

// SettingsSectionDefinition is the registration payload for
// POST /api/plugins/settings/sections.
type SettingsSectionDefinition struct {
	Name        string         `json:"name"`
	Type        string         `json:"type"`
	CallbackURL string         `json:"callback_url"`
	AuthHeader  string         `json:"auth_header"`
	Fields      []SettingField `json:"fields"`
}

// SettingField is one input row inside a form-type settings section.
// Min and Max are pointers so they can be omitted for non-numeric fields
// while still serialising as explicit zeros when set.
type SettingField struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
	Default     any    `json:"default"`
	Min         *int   `json:"min"`
	Max         *int   `json:"max"`
}

// SettingsSectionResponse is argus's response shape on section registration.
type SettingsSectionResponse struct {
	Name  string `json:"name"`
	Scope string `json:"scope"`
}

// RegisterSettingsSection POSTs a settings-section registration. Re-POSTing
// with the same name is idempotent (refreshes the heartbeat) per the
// substrate contract, mirroring the MCP tool registration semantics.
func (c *Client) RegisterSettingsSection(ctx context.Context, def SettingsSectionDefinition) (*SettingsSectionResponse, error) {
	var resp SettingsSectionResponse
	if _, err := c.doJSON(ctx, "POST", "/api/plugins/settings/sections", def, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// UnregisterSettingsSection DELETEs a settings-section registration by name.
// Idempotent – deleting a missing name returns 200 OK per the substrate
// contract.
func (c *Client) UnregisterSettingsSection(ctx context.Context, name string) error {
	_, err := c.doJSON(ctx, "DELETE", "/api/plugins/settings/sections/"+url.PathEscape(name), nil, nil)
	return err
}
