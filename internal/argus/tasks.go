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
