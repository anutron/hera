package mcp

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/anutron/hera/internal/argus"
	"github.com/anutron/hera/internal/db"
)

// Resolver helps handlers translate a cwd input into the argus task that
// owns that worktree and the hera role bound to it.
type Resolver struct {
	client *argus.Client
	db     *db.DB
}

// NewResolver constructs a Resolver.
func NewResolver(client *argus.Client, database *db.DB) *Resolver {
	return &Resolver{client: client, db: database}
}

// TaskForCwd returns the argus task whose worktree_path matches cwd,
// after normalizing both sides via filepath.Clean (so trailing slashes,
// redundant separators, and "." segments don't cause false misses).
// Returns ErrCwdUnknown if no task matches.
func (r *Resolver) TaskForCwd(ctx context.Context, cwd string) (*argus.Task, error) {
	if cwd == "" {
		return nil, ErrCwdMissing
	}
	normalized := filepath.Clean(cwd)
	tasks, err := r.client.ListTasks(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolver.TaskForCwd: list tasks: %w", err)
	}
	for i := range tasks {
		if filepath.Clean(tasks[i].WorktreePath) == normalized {
			return &tasks[i], nil
		}
	}
	return nil, ErrCwdUnknown
}

// CallerRole resolves cwd → argus task → live binding → role. The
// orchestrator parameter is optional and disambiguates the calling
// task's binding when the task holds multiple live bindings:
//
//   - orchestrator == "" and exactly one live binding → return it
//     (the back-compat single-binding path).
//   - orchestrator == "" and zero live bindings → ErrNoBinding.
//   - orchestrator == "" and 2+ live bindings → AmbiguousBindingError
//     listing each binding's orchestrator so the caller can re-invoke
//     with an explicit orchestrator.
//   - orchestrator != "" → look up the binding for that orchestrator;
//     ErrNoBindingForOrchestrator if no such binding exists; the
//     orchestrator name itself being unknown surfaces the same error
//     (the caller can't be bound to something that doesn't exist).
func (r *Resolver) CallerRole(ctx context.Context, cwd, orchestrator string) (*argus.Task, *db.Role, *db.Binding, error) {
	task, err := r.TaskForCwd(ctx, cwd)
	if err != nil {
		return nil, nil, nil, err
	}

	if orchestrator != "" {
		orch, err := r.db.Orchestrators.GetByName(ctx, orchestrator)
		if errors.Is(err, db.ErrNotFound) {
			return task, nil, nil, &NoBindingForOrchestratorError{Orchestrator: orchestrator}
		}
		if err != nil {
			return task, nil, nil, err
		}
		bnd, err := r.db.Bindings.GetLiveByTaskAndOrchestrator(ctx, task.ID, orch.ID)
		if errors.Is(err, db.ErrNotFound) {
			return task, nil, nil, &NoBindingForOrchestratorError{Orchestrator: orchestrator}
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

	bindings, err := r.db.Bindings.ListLiveByTaskID(ctx, task.ID)
	if err != nil {
		return task, nil, nil, err
	}
	if len(bindings) == 0 {
		return task, nil, nil, ErrNoBinding
	}
	if len(bindings) > 1 {
		return task, nil, nil, r.buildAmbiguousError(ctx, bindings)
	}
	bnd := bindings[0]
	role, err := r.db.Roles.GetByID(ctx, bnd.RoleID)
	if err != nil {
		return task, nil, bnd, err
	}
	return task, role, bnd, nil
}

// buildAmbiguousError loads the orchestrator + role names for each of
// the calling task's live bindings and returns an AmbiguousBindingError
// whose message lists them so the operator can pick the right
// orchestrator string for a follow-up call.
func (r *Resolver) buildAmbiguousError(ctx context.Context, bindings []*db.Binding) *AmbiguousBindingError {
	out := &AmbiguousBindingError{}
	for _, b := range bindings {
		entry := AmbiguousBindingEntry{}
		if orch, err := r.db.Orchestrators.GetByID(ctx, b.OrchestratorID); err == nil {
			entry.Orchestrator = orch.Name
		} else {
			entry.Orchestrator = fmt.Sprintf("(orchestrator id %d)", b.OrchestratorID)
		}
		if role, err := r.db.Roles.GetByID(ctx, b.RoleID); err == nil {
			entry.RoleName = role.Name
			entry.Kind = string(role.Kind)
		}
		out.Bindings = append(out.Bindings, entry)
	}
	return out
}

// AmbiguousBindingError is returned when the calling task has 2+ live
// bindings and no orchestrator was specified to disambiguate. The
// error message lists each binding so the operator can copy the
// orchestrator name into a follow-up call.
type AmbiguousBindingError struct {
	Bindings []AmbiguousBindingEntry
}

// AmbiguousBindingEntry is one row of the ambiguity report.
type AmbiguousBindingEntry struct {
	Orchestrator string
	RoleName     string
	Kind         string
}

func (e *AmbiguousBindingError) Error() string {
	var parts []string
	for _, b := range e.Bindings {
		parts = append(parts, fmt.Sprintf("%s/%s (%s)", b.Orchestrator, b.RoleName, b.Kind))
	}
	return "this argus task holds multiple live hera bindings: " +
		strings.Join(parts, ", ") +
		". Re-invoke with an explicit orchestrator parameter to pick one."
}

// NoBindingForOrchestratorError is returned when the caller asked for
// a specific orchestrator but no binding exists for that orchestrator
// on the calling task. The error name lets the handler suggest the
// attach signature.
type NoBindingForOrchestratorError struct {
	Orchestrator string
}

func (e *NoBindingForOrchestratorError) Error() string {
	return fmt.Sprintf("this argus task is not bound to orchestrator %q. To attach, call hera_join with role_name and kind.", e.Orchestrator)
}

// Errors returned by Resolver. Handlers convert these to MCP error
// responses with explanatory messages.
var (
	ErrCwdMissing = errors.New("cwd input is required")
	ErrCwdUnknown = errors.New("cwd does not map to any tracked argus task")
	ErrNoBinding  = errors.New("argus task at cwd is not bound to any hera role")
)
