package mcp

import (
	"context"
	"errors"
	"fmt"

	"github.com/anutron/ludwig/internal/argus"
	"github.com/anutron/ludwig/internal/db"
)

// Resolver helps handlers translate a cwd input into the argus task that
// owns that worktree and the ludwig role bound to it.
type Resolver struct {
	client *argus.Client
	db     *db.DB
}

// NewResolver constructs a Resolver.
func NewResolver(client *argus.Client, database *db.DB) *Resolver {
	return &Resolver{client: client, db: database}
}

// TaskForCwd returns the argus task whose worktree_path matches cwd
// exactly. Returns ErrCwdUnknown if no task matches.
func (r *Resolver) TaskForCwd(ctx context.Context, cwd string) (*argus.Task, error) {
	if cwd == "" {
		return nil, ErrCwdMissing
	}
	tasks, err := r.client.ListTasks(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolver.TaskForCwd: list tasks: %w", err)
	}
	for i := range tasks {
		if tasks[i].WorktreePath == cwd {
			return &tasks[i], nil
		}
	}
	return nil, ErrCwdUnknown
}

// CallerRole resolves cwd → argus task → live binding → role. Returns
// ErrNoBinding if the task is not bound to any ludwig role.
func (r *Resolver) CallerRole(ctx context.Context, cwd string) (*argus.Task, *db.Role, *db.Binding, error) {
	task, err := r.TaskForCwd(ctx, cwd)
	if err != nil {
		return nil, nil, nil, err
	}
	bnd, err := r.db.Bindings.GetLiveByTaskID(ctx, task.ID)
	if errors.Is(err, db.ErrNotFound) {
		return task, nil, nil, ErrNoBinding
	}
	if err != nil {
		return task, nil, nil, err
	}
	role, err := r.db.Roles.GetByID(ctx, bnd.RoleID)
	if err != nil {
		return task, nil, bnd, err
	}
	return task, role, bnd, nil
}

// Errors returned by Resolver. Handlers convert these to MCP error
// responses with explanatory messages.
var (
	ErrCwdMissing = errors.New("cwd input is required")
	ErrCwdUnknown = errors.New("cwd does not map to any tracked argus task")
	ErrNoBinding  = errors.New("argus task at cwd is not bound to any ludwig role")
)
