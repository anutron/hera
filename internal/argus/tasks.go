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
	// Elapsed is argus's pre-formatted age string ("16m", "4d") — the rail
	// renders it verbatim for freelance rows so their elapsed column matches
	// argus's own ("the same way Argus shows them"). Absent on old daemons.
	Elapsed string `json:"elapsed,omitempty"`
	// Idle / NeedsInput are runtime-derived flags argus serves only while
	// Status == in_progress (omitempty, so absent == false). NeedsInput
	// requires argus >= the plugin-substrate build; older daemons omit it.
	Idle       bool `json:"idle,omitempty"`
	NeedsInput bool `json:"needs_input,omitempty"`
	// Archived mirrors argus's is_archived. Hera uses it to keep archived
	// argus tasks out of the active rail (omitempty, so absent == false).
	Archived bool `json:"archived,omitempty"`
	// PRState is the GitHub PR review state string served by argus's daemon
	// poller (awaiting-review / changes-requested / approved / none / draft /
	// merged-closed / unknown). Absent on daemons that predate the PR-poll
	// feature — hera treats an absent field as non-actionable (no indicator).
	PRState string `json:"pr_state,omitempty"`
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

// ListTasksAll fetches every task INCLUDING archived ones (archived=all).
// The default GET /api/tasks excludes archived tasks, so the rail's state
// cache uses this to learn each task's archived bit (and full state) — without
// it, archived tasks are invisible to the cache and can't be hidden from the
// active rail.
func (c *Client) ListTasksAll(ctx context.Context) ([]Task, error) {
	var resp ListTasksResponse
	if _, err := c.doJSON(ctx, "GET", "/api/tasks?archived=all", nil, &resp); err != nil {
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

// taskSizeResponse mirrors argus's GET /api/tasks/{id}/size payload.
type taskSizeResponse struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

// GetTaskSize fetches the worker PTY's current cols/rows from argus. Argus
// returns 404 with `{"error":"no active session"}` when the task has no
// live session — that case is reported as ErrNoTaskSize so callers can
// pick a default surface size without spamming logs.
func (c *Client) GetTaskSize(ctx context.Context, taskID string) (int, int, error) {
	var out taskSizeResponse
	status, err := c.doJSON(ctx, "GET", "/api/tasks/"+url.PathEscape(taskID)+"/size", nil, &out)
	if err != nil {
		if status == 404 {
			return 0, 0, ErrNoTaskSize
		}
		return 0, 0, err
	}
	return out.Cols, out.Rows, nil
}

// resizeTaskInput is the body of POST /api/tasks/{id}/size.
type resizeTaskInput struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

// minResizeDim / maxResizeDim mirror argus's accepted bounds for /size.
// Callers passing values outside [1, 1000] short-circuit with a local
// error rather than burning a roundtrip on a guaranteed 400.
const (
	minResizeDim = 1
	maxResizeDim = 1000
)

// ResizeTask asks argus to resize the worker PTY for taskID to the given
// cols/rows via POST /api/tasks/{id}/resize?skip_kick=1. Argus applies the
// SIGWINCH directly to the worker process so live terminal UI reflows at the
// new width. The ?skip_kick=1 parameter suppresses argus's KickRerender
// (stop+--session-id restart): hera issues ResizeTask on every pane-geometry
// change driven by the plugin viewport, and each kick would blank the pane and
// restart the session. Scrollback reflow is handled by the TUI-side
// maybeKickRerender path (when the user explicitly enters argus's native task
// view), not by plugin-view pane-size adjustments.
//
// Returns ErrNoTaskSize when argus reports 404 (no active session for
// the task). Other non-2xx statuses surface as *HTTPError. Out-of-bound
// dimensions short-circuit locally with a typed error.
func (c *Client) ResizeTask(ctx context.Context, taskID string, cols, rows int) error {
	if cols < minResizeDim || cols > maxResizeDim || rows < minResizeDim || rows > maxResizeDim {
		return fmt.Errorf("argus.ResizeTask: cols/rows must be in [%d, %d]: got %dx%d", minResizeDim, maxResizeDim, cols, rows)
	}
	body := resizeTaskInput{Cols: cols, Rows: rows}
	// Argus mounts the resize handler at POST /api/tasks/{id}/resize.
	// The /size route is GET-only (for reads) and returns 405 on POST.
	// skip_kick=1 suppresses KickRerender so plugin-view geometry changes
	// only send SIGWINCH, not stop+restart (BUG-058).
	status, err := c.doJSON(ctx, "POST", "/api/tasks/"+url.PathEscape(taskID)+"/resize?skip_kick=1", body, nil)
	if err != nil {
		if status == 404 {
			return ErrNoTaskSize
		}
		return err
	}
	return nil
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
// Branch is optional; an empty value uses the project's default branch.
type CreateTaskInput struct {
	Project string `json:"project"`
	Name    string `json:"name,omitempty"`
	Prompt  string `json:"prompt"`
	Backend string `json:"backend,omitempty"`
	Branch  string `json:"branch,omitempty"`
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

// ListProjects fetches the names of every configured argus project via
// GET /api/projects. Returns an empty slice (not an error) when the
// daemon has no projects configured.
func (c *Client) ListProjects(ctx context.Context) ([]string, error) {
	var resp struct {
		Projects []string `json:"projects"`
	}
	if _, err := c.doJSON(ctx, "GET", "/api/projects", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Projects, nil
}

// BackendEntry describes one argus backend entry as returned by
// GET /api/backends. Command is the process template argus uses to
// spawn the agent. PromptFlag is the flag used to pass the initial
// prompt (e.g. "--" for positional args).
type BackendEntry struct {
	Name       string `json:"name"`
	Command    string `json:"command"`
	PromptFlag string `json:"prompt_flag,omitempty"`
}

// ListBackends fetches every configured argus backend via
// GET /api/backends. Returns an empty slice (not an error) when the
// daemon has no backends configured beyond its built-in default.
func (c *Client) ListBackends(ctx context.Context) ([]BackendEntry, error) {
	var resp struct {
		Backends []BackendEntry `json:"backends"`
	}
	if _, err := c.doJSON(ctx, "GET", "/api/backends", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Backends, nil
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

// RestartTask asks argus to restart the agent session for taskID via
// POST /api/tasks/{id}/restart. Argus is expected to re-spawn the agent
// backend (e.g. claude --resume <last-session-id>) and route its PTY output
// through the same task output stream so hera's proxy subscription picks up
// the resumed session automatically.
//
// Returns ErrNoTaskRestart when argus responds 404 or 405 (the endpoint does
// not exist on this daemon version). Other non-2xx responses surface as
// *HTTPError.
func (c *Client) RestartTask(ctx context.Context, taskID string) error {
	status, err := c.doJSON(ctx, "POST", "/api/tasks/"+url.PathEscape(taskID)+"/restart", nil, nil)
	if err != nil {
		if status == 404 || status == 405 {
			return ErrNoTaskRestart
		}
		return err
	}
	return nil
}

// DeleteTask destroys an argus task via DELETE /api/tasks/{id}. Argus stops
// the session, removes the session log + artifacts, deletes the DB row, AND
// cleans up the task's git worktree + branch (see argus handleDeleteTask).
// This is the destructive verb backing hera-view's `^d`: it removes the argus
// task, worktree, and branch in one call. The route accepts hera's scope
// token (it is NOT master-gated — same tier as stop).
//
// A 404 is treated as success: deletion is idempotent, so a task that argus
// already removed (e.g. deleted out-of-band before a `^d` cascade or `^r`
// prune reaches it) is "already gone" rather than an error. Swallowing the
// 404 keeps cascades from aborting partway and leaving sibling roles
// un-archived. Other non-2xx statuses surface as *HTTPError.
func (c *Client) DeleteTask(ctx context.Context, taskID string) error {
	status, err := c.doJSON(ctx, "DELETE", "/api/tasks/"+url.PathEscape(taskID), nil, nil)
	if err != nil {
		if status == 404 {
			return nil
		}
		return err
	}
	return nil
}

// setTaskStatusInput is the body of POST /api/tasks/{id}/status.
type setTaskStatusInput struct {
	Status string `json:"status"`
}

// setTaskStatusResponse mirrors argus's status response envelope.
type setTaskStatusResponse struct {
	Status string `json:"status"`
}

// SetTaskStatus sets an argus task's workflow status via
// POST /api/tasks/{id}/status. status must be one of argus's status strings:
// "pending", "in_progress", "in_review", "complete". Returns argus's resolved
// status string. Backs hera-view's `s`/`S` advance/revert keys (the caller
// computes the next/prev status before invoking). Accepts hera's scope token
// (not master-gated).
func (c *Client) SetTaskStatus(ctx context.Context, taskID, status string) (string, error) {
	body := setTaskStatusInput{Status: status}
	var out setTaskStatusResponse
	if _, err := c.doJSON(ctx, "POST", "/api/tasks/"+url.PathEscape(taskID)+"/status", body, &out); err != nil {
		return "", err
	}
	return out.Status, nil
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
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == 404 {
		return 0, ErrNoTaskInput
	}
	if resp.StatusCode >= 400 {
		return 0, fmt.Errorf("argus.PostTaskInput: HTTP %d", resp.StatusCode)
	}

	var out postTaskInputResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, fmt.Errorf("argus.PostTaskInput: decode: %w", err)
	}
	return int(out.Bytes), nil
}
