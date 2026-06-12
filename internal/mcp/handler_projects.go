package mcp

import (
	"context"
	"encoding/json"

	"github.com/anutron/hera/internal/argus"
)

// ProjectsHandler implements the hera_projects MCP tool: a read-only listing
// of every configured argus project with its configured default branch and
// backend. It performs no role resolution and no side effects — any caller may
// list projects to discover valid hera_spawn_worker `project` values, even
// from a cwd that maps to no tracked argus task.
type ProjectsHandler struct {
	client *argus.Client
}

// NewProjectsHandler constructs a ProjectsHandler.
func NewProjectsHandler(client *argus.Client) *ProjectsHandler {
	return &ProjectsHandler{client: client}
}

// ProjectsInput is the tool's input schema. cwd is accepted for harness
// uniformity (every hera tool is invoked with $PWD) but is unused: project
// discovery is global, not caller-scoped.
type ProjectsInput struct {
	Cwd string `json:"cwd,omitempty"`
}

// ProjectEntry is one configured argus project. Branch/Backend are the
// project's configured defaults; empty (and omitted) means the project
// inherits argus's global default.
type ProjectEntry struct {
	Name    string `json:"name"`
	Branch  string `json:"branch,omitempty"`
	Backend string `json:"backend,omitempty"`
}

// ProjectsOutput is the success payload.
type ProjectsOutput struct {
	Projects []ProjectEntry `json:"projects"`
	Count    int            `json:"count"`
}

// Handle implements Handler.
func (h *ProjectsHandler) Handle(ctx context.Context, raw json.RawMessage) Response {
	if resp, gated := LinkGate(); gated {
		return resp
	}
	// Input is optional/ignored, but reject malformed JSON for consistency.
	if len(raw) > 0 {
		var in ProjectsInput
		if err := json.Unmarshal(raw, &in); err != nil {
			return ErrorResponse("hera_projects: invalid input JSON: " + err.Error())
		}
	}

	full, err := h.client.ListProjectsFull(ctx)
	if err != nil {
		return ErrorResponse("hera_projects: list projects: " + err.Error())
	}

	out := ProjectsOutput{
		Projects: make([]ProjectEntry, 0, len(full)),
		Count:    len(full),
	}
	for _, p := range full {
		out.Projects = append(out.Projects, ProjectEntry{
			Name:    p.Name,
			Branch:  p.Branch,
			Backend: p.Backend,
		})
	}
	return jsonText(out)
}
