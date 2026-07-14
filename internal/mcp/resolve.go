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
//
// A worktree_path is NOT a stable unique key across a task's full lifecycle:
// argus reuses a worktree directory when a task name / branch is reused after
// the prior task moved to in_review / complete / archived without its worktree
// being cleared. When two tasks share a cwd, returning the first match (the
// pre-BUG-059 behavior) can silently resolve to the STALE task, which is the
// root of the claim-vs-attach binding paradox: identity then keys off the
// wrong argus_task_id. To keep resolution stable we disambiguate:
//
//   - one match                          → return it (the overwhelmingly
//     common case; behavior unchanged).
//   - drop archived matches, one left    → return it.
//   - multiple live matches, exactly one
//     is in_progress                      → return the running session (the
//     agent making the call is in_progress).
//   - otherwise (all archived, or 2+
//     equally-plausible live matches)     → ErrCwdUnknown / CwdAmbiguousError
//     so the caller surfaces the collision instead of guessing.
func (r *Resolver) TaskForCwd(ctx context.Context, cwd string) (*argus.Task, error) {
	if cwd == "" {
		return nil, ErrCwdMissing
	}
	normalized := filepath.Clean(cwd)
	tasks, err := r.client.ListTasks(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolver.TaskForCwd: list tasks: %w", err)
	}
	var matches []*argus.Task
	for i := range tasks {
		if filepath.Clean(tasks[i].WorktreePath) == normalized {
			matches = append(matches, &tasks[i])
		}
	}
	return disambiguateTaskMatches(matches)
}

// disambiguateTaskMatches picks the single task a cwd should resolve to among
// tasks that all share the worktree_path. See TaskForCwd for the rules.
func disambiguateTaskMatches(matches []*argus.Task) (*argus.Task, error) {
	switch len(matches) {
	case 0:
		return nil, ErrCwdUnknown
	case 1:
		return matches[0], nil
	}

	var active []*argus.Task
	for _, t := range matches {
		if !t.Archived {
			active = append(active, t)
		}
	}
	switch len(active) {
	case 0:
		// Every task at this cwd is archived — there is no live task here.
		return nil, ErrCwdUnknown
	case 1:
		return active[0], nil
	}

	var running []*argus.Task
	for _, t := range active {
		if t.Status == "in_progress" {
			running = append(running, t)
		}
	}
	if len(running) == 1 {
		return running[0], nil
	}
	return nil, &CwdAmbiguousError{Candidates: active}
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
		bnd, err := r.LiveBindingForOrch(ctx, task, orch.ID)
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

	bindings, err := r.LiveBindingsForTask(ctx, task)
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

// LiveBindingForOrch resolves the caller's live binding under orchID. It keys
// first on the resolved argus_task_id (the exact, historical path) and, on a
// miss, falls back to the caller's worktree_path. The fallback closes the
// BUG-059 gap: when cwd resolved to a colliding task id the task-keyed lookup
// misses the live binding, but the (worktree_path, orchestrator_id) uniqueness
// guarantees the worktree-keyed lookup finds the one correct binding — the same
// row an attach INSERT would collide with. Orchestrator scoping makes the
// fallback safe: a stale binding for a different orchestrator sharing the
// worktree cannot be returned here.
func (r *Resolver) LiveBindingForOrch(ctx context.Context, task *argus.Task, orchID int64) (*db.Binding, error) {
	bnd, err := r.db.Bindings.GetLiveByTaskAndOrchestrator(ctx, task.ID, orchID)
	if err == nil {
		return bnd, nil
	}
	if !errors.Is(err, db.ErrNotFound) {
		return nil, err
	}
	if task.WorktreePath == "" {
		return nil, db.ErrNotFound
	}
	return r.db.Bindings.GetLiveByWorktreeAndOrchestrator(ctx, task.WorktreePath, orchID)
}

// LiveBindingsForTask lists the caller's live bindings. It keys first on the
// resolved argus_task_id and, when that yields none, falls back to the
// worktree_path so a cwd that resolved to a colliding task still finds the
// bindings physically rooted at this worktree (BUG-059). The fallback fires
// only on a task-keyed miss, so it never double-counts.
func (r *Resolver) LiveBindingsForTask(ctx context.Context, task *argus.Task) ([]*db.Binding, error) {
	bindings, err := r.db.Bindings.ListLiveByTaskID(ctx, task.ID)
	if err != nil {
		return nil, err
	}
	if len(bindings) > 0 || task.WorktreePath == "" {
		return bindings, nil
	}
	return r.db.Bindings.ListLiveByWorktree(ctx, task.WorktreePath)
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

// CwdAmbiguousError is returned when a cwd maps to two or more equally
// plausible live argus tasks (same worktree_path, none clearly the running
// session) so the resolver refuses to guess which one the caller is. The
// message lists the candidates so the operator can disambiguate or clean up
// the stale task.
type CwdAmbiguousError struct {
	Candidates []*argus.Task
}

func (e *CwdAmbiguousError) Error() string {
	var parts []string
	for _, t := range e.Candidates {
		parts = append(parts, fmt.Sprintf("%s (%s)", t.ID, t.Status))
	}
	return "cwd maps to multiple live argus tasks sharing this worktree: " +
		strings.Join(parts, ", ") +
		". A stale task is likely reusing this worktree path — archive or delete it, or re-run once only one task here is in_progress."
}

// Errors returned by Resolver. Handlers convert these to MCP error
// responses with explanatory messages.
var (
	ErrCwdMissing = errors.New("cwd input is required")
	ErrCwdUnknown = errors.New("cwd does not map to any tracked argus task")
	ErrNoBinding  = errors.New("argus task at cwd is not bound to any hera role")
)
