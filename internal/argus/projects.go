package argus

import "context"

// Project describes one configured argus project as returned by
// GET /api/projects/full. Branch and Backend are the project's configured
// defaults; both are empty when the project inherits argus's global default.
// Argus also returns each project's path and sandbox overrides — hera
// intentionally does not consume those (it exposes only the fields that
// compose with hera_spawn_worker, and avoids surfacing absolute paths).
type Project struct {
	Name    string `json:"name"`
	Branch  string `json:"branch,omitempty"`
	Backend string `json:"backend,omitempty"`
}

// ListProjectsFull fetches every configured argus project with its configured
// defaults via GET /api/projects/full. Returns an empty slice (not an error)
// when the daemon has no projects configured.
//
// This is hera's single source of truth for project discovery: the
// hera_projects MCP tool, hera_spawn_worker's up-front project validation, and
// the modal name list (via ListProjects) all derive from it.
func (c *Client) ListProjectsFull(ctx context.Context) ([]Project, error) {
	var resp struct {
		Projects []Project `json:"projects"`
	}
	if _, err := c.doJSON(ctx, "GET", "/api/projects/full", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Projects, nil
}
