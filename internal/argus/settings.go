package argus

import (
	"context"
	"net/url"
)

// SettingsSectionDefinition is the registration payload for
// POST /api/plugins/settings/sections.
//
// Wire shape mirrors argus's settings.rawSection (internal/tui/settings/form.go)
// plus a couple of hera-local fields argus tolerates but drops:
//   - Name is hera's internal identity for the section; argus derives
//     identity from (scope-from-auth-token, Title) and ignores Name on the
//     wire. We keep it because the registrar lists by Name internally.
//   - AuthHeader is the secret hera mints for callback auth. Argus's
//     current settings-callback proxy doesn't echo it back when POSTing to
//     the callback URL, so it's a no-op on the wire today — included for
//     parity with the MCP tool registration shape and to track when the
//     substrate gains callback auth support.
//
// Title is the human-readable label argus renders for the section header.
// Argus rejects empty titles with HTTP 400 "settings: title must be non-empty".
type SettingsSectionDefinition struct {
	Name        string         `json:"name"`
	Title       string         `json:"title"`
	Type        string         `json:"type"`
	CallbackURL string         `json:"callback_url"`
	AuthHeader  string         `json:"auth_header,omitempty"`
	Fields      []SettingField `json:"fields"`
}

// SettingField is one input row inside a form-type settings section.
//
// Wire keys match argus's settings.FormField. Argus rejects fields with an
// empty Key or empty Label. Description has no argus-side consumer today
// (argus's FormField ignores it), but the spec keeps it on the wire so the
// substrate can surface impact copy in a later iteration; we send it
// regardless. Min/Max/Default are omitempty so non-numeric and no-default
// fields don't carry stray nulls on the wire.
type SettingField struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
	Default     any    `json:"default,omitempty"`
	Min         *int   `json:"min,omitempty"`
	Max         *int   `json:"max,omitempty"`
}

// SettingsSectionResponse is argus's response shape on section registration.
// Argus returns {scope, title, id} on both 201 (new) and 200 (re-register).
type SettingsSectionResponse struct {
	Scope string `json:"scope"`
	Title string `json:"title"`
	ID    int64  `json:"id"`
}

// RegisterSettingsSection POSTs a settings-section registration. Re-POSTing
// with the same (scope, title) is idempotent (refreshes the row, returns 200)
// per the substrate contract, mirroring the MCP tool registration semantics.
func (c *Client) RegisterSettingsSection(ctx context.Context, def SettingsSectionDefinition) (*SettingsSectionResponse, error) {
	var resp SettingsSectionResponse
	if _, err := c.doJSON(ctx, "POST", "/api/plugins/settings/sections", def, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// UnregisterSettingsSection DELETEs a settings-section registration. Argus
// routes section identity by (scope, title) — the path is
// /api/plugins/settings/sections/{scope}/{title}. Idempotent on argus's
// side: deleting a missing (scope, title) returns 404 but is otherwise
// harmless.
func (c *Client) UnregisterSettingsSection(ctx context.Context, scope, title string) error {
	path := "/api/plugins/settings/sections/" + url.PathEscape(scope) + "/" + url.PathEscape(title)
	_, err := c.doJSON(ctx, "DELETE", path, nil, nil)
	return err
}
