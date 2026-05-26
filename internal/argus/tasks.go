package argus

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
)

// Task is the subset of argus's task representation that hera consumes.
// Argus may return additional fields; they are ignored.
type Task struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Project      string `json:"project"`
	Status       string `json:"status"`
	WorktreePath string `json:"worktree_path"`
}

// ListTasksResponse is argus's GET /api/tasks payload envelope.
type ListTasksResponse struct {
	Tasks []Task `json:"tasks"`
}

// ListTasks fetches the current task list.
func (c *Client) ListTasks(ctx context.Context) ([]Task, error) {
	var resp ListTasksResponse
	if _, err := c.doJSON(ctx, "GET", "/api/tasks", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Tasks, nil
}

// GetTask fetches one task.
func (c *Client) GetTask(ctx context.Context, taskID string) (*Task, error) {
	var t Task
	if _, err := c.doJSON(ctx, "GET", "/api/tasks/"+url.PathEscape(taskID), nil, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// MetaEntry is one row from a task's metadata sidecar.
type MetaEntry struct {
	Namespace string `json:"namespace"`
	Key       string `json:"key"`
	Value     string `json:"value"`
	UpdatedAt string `json:"updated_at"`
}

// GetTaskMetaResponse is argus's GET /api/tasks/:id/meta payload envelope.
type GetTaskMetaResponse struct {
	Entries []MetaEntry `json:"entries"`
}

// GetTaskMeta fetches a task's metadata, optionally filtered by namespace.
// Passing an empty namespace fetches every namespace.
func (c *Client) GetTaskMeta(ctx context.Context, taskID, namespace string) ([]MetaEntry, error) {
	path := "/api/tasks/" + url.PathEscape(taskID) + "/meta"
	if namespace != "" {
		path += "?namespace=" + url.QueryEscape(namespace)
	}
	var resp GetTaskMetaResponse
	if _, err := c.doJSON(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Entries, nil
}

// PutTaskMetaInput is the body of a single-key write to task metadata. The
// namespace is auto-derived from the scope token; clients writing under
// their own scope MUST omit it.
type PutTaskMetaInput struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// PutTaskMeta writes a single key/value to a task's metadata sidecar. The
// namespace is derived from the scope token by argus.
func (c *Client) PutTaskMeta(ctx context.Context, taskID, key, value string) error {
	body := PutTaskMetaInput{Key: key, Value: value}
	_, err := c.doJSON(ctx, "PUT", "/api/tasks/"+url.PathEscape(taskID)+"/meta", body, nil)
	return err
}

// PostTaskInput writes raw bytes to a task's PTY. Returns the byte count
// argus accepted, or an error.
//
// The `bytes` field on the wire is tolerated as either a JSON int or a
// JSON string (argus has historically emitted it as a string; hera v1
// declared it as int, which surfaced as a decode error on every call
// during the hera-settings dogfood loop even though the inject side
// effect succeeded). flexInt accepts either shape.
type postTaskInputResponse struct {
	Status string  `json:"status"`
	Bytes  flexInt `json:"bytes"`
}

// flexInt decodes a numeric value from JSON whether it arrives as a
// number (e.g. 12) or a quoted string (e.g. "12"). Use only for fields
// where the wire format is observed-tolerant; do NOT use as a default
// for new fields — prefer a typed int and tighten the contract.
type flexInt int

func (f *flexInt) UnmarshalJSON(data []byte) error {
	s := string(data)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return fmt.Errorf("flexInt: cannot parse %q: %w", string(data), err)
	}
	*f = flexInt(n)
	return nil
}

// CreateTaskInput is the body posted to argus's POST /api/tasks. Project
// is required; if absent on the argus side the call returns HTTP 400 and
// callers must seed the project first (in v1 hera does not have a
// master-scope token, so project creation is an operator setup step).
//
// Name is optional; argus auto-derives a slug from Prompt when empty.
// Backend is optional; an empty value uses argus's per-project default.
type CreateTaskInput struct {
	Project string `json:"project"`
	Name    string `json:"name,omitempty"`
	Prompt  string `json:"prompt"`
	Backend string `json:"backend,omitempty"`
}

// CreatedTask is argus's POST /api/tasks response envelope.
type CreatedTask struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

// CreateTask creates a new argus task and starts its agent session. The
// argus task name is auto-generated from the prompt when CreateTaskInput.Name
// is empty.
//
// Meta is a convenience: after the task is created, every entry in meta
// is PUT to the task's /meta endpoint via PutTaskMeta. A meta PUT failure
// is reported as an error but does NOT roll back the created task —
// callers can resolve the partial state via the returned task id.
func (c *Client) CreateTask(ctx context.Context, in CreateTaskInput, meta map[string]string) (*CreatedTask, error) {
	if in.Project == "" {
		return nil, fmt.Errorf("argus.CreateTask: project is required")
	}
	if in.Prompt == "" && in.Name == "" {
		return nil, fmt.Errorf("argus.CreateTask: prompt or name is required")
	}
	var out CreatedTask
	if _, err := c.doJSON(ctx, "POST", "/api/tasks", in, &out); err != nil {
		return nil, err
	}
	for k, v := range meta {
		if err := c.PutTaskMeta(ctx, out.ID, k, v); err != nil {
			return &out, fmt.Errorf("argus.CreateTask: meta %q on task %s: %w", k, out.ID, err)
		}
	}
	return &out, nil
}

// CreateProjectInput is the body for POST /api/projects. Argus requires
// Name and Path; the rest are optional. Note: argus enforces master-scope
// auth on this route, so hera's scope token will be rejected with HTTP 403.
// The method is included for completeness and for tests; the production
// flow expects projects to be pre-configured by the operator.
type CreateProjectInput struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Branch  string `json:"branch,omitempty"`
	Backend string `json:"backend,omitempty"`
}

// CreateProject saves a new argus project. Returns an error if argus
// rejects the call (e.g., HTTP 403 for scoped tokens, HTTP 400 for
// invalid input).
func (c *Client) CreateProject(ctx context.Context, in CreateProjectInput) error {
	if in.Name == "" || in.Path == "" {
		return fmt.Errorf("argus.CreateProject: name and path are required")
	}
	_, err := c.doJSON(ctx, "POST", "/api/projects", in, nil)
	return err
}

// ArchiveTask flips the argus task's archived flag to true via
// POST /api/tasks/{id}/archive. Idempotent on argus's side.
func (c *Client) ArchiveTask(ctx context.Context, taskID string) error {
	_, err := c.doJSON(ctx, "POST", "/api/tasks/"+url.PathEscape(taskID)+"/archive", nil, nil)
	return err
}

// UnarchiveTask flips the argus task's archived flag back to false.
func (c *Client) UnarchiveTask(ctx context.Context, taskID string) error {
	_, err := c.doJSON(ctx, "POST", "/api/tasks/"+url.PathEscape(taskID)+"/unarchive", nil, nil)
	return err
}

func (c *Client) PostTaskInput(ctx context.Context, taskID string, payload []byte) (int, error) {
	req, err := newRequest(ctx, "POST", c.BaseURL()+"/api/tasks/"+url.PathEscape(taskID)+"/input", payload)
	if err != nil {
		return 0, err
	}
	c.applyAuth(req)
	req.Header.Set("Content-Type", "text/plain")

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("argus.PostTaskInput: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return 0, fmt.Errorf("argus.PostTaskInput: HTTP %d", resp.StatusCode)
	}

	var out postTaskInputResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, fmt.Errorf("argus.PostTaskInput: decode: %w", err)
	}
	return int(out.Bytes), nil
}
