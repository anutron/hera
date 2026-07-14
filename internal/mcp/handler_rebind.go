package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/anutron/hera/internal/argus"
	"github.com/anutron/hera/internal/db"
	"github.com/anutron/hera/internal/events"
)

// RebindHandler implements the hera_rebind MCP tool — the supported repair
// path for a binding stuck in the claim-says-none / attach-says-exists state
// (BUG-059).
//
// The stuck state arises when a worktree_path is reused across two argus
// tasks' lifecycles: the live binding is rooted at the worktree, but a
// cwd→task lookup can resolve a colliding (stale) task id, so the task-keyed
// claim misses the binding while the worktree-keyed uniqueness rejects any
// fresh attach. The lookup fix (task-then-worktree fallback) makes a plain
// claim succeed in the common case where the binding row itself is correct.
// hera_rebind handles the harder case where the live binding's OWN
// argus_task_id has drifted away from the caller's actual live session — so
// PTY delivery and status routing (which key on the binding's argus_task_id)
// go nowhere. It reconciles the binding to the caller's real task WITHOUT
// tearing down the argus session: only the binding row is refreshed; the role
// (and thus its prompt, messages, and status, all keyed on role_id) survives.
//
// It refuses rather than guess whenever the state is genuinely ambiguous:
// two live in_progress tasks share the worktree, multiple roles are bound
// here and no role_name disambiguates, or another role's live binding holds
// the target slot.
type RebindHandler struct {
	resolver *Resolver
	db       *db.DB
	client   *argus.Client
}

// NewRebindHandler constructs a RebindHandler.
func NewRebindHandler(r *Resolver, database *db.DB, client *argus.Client) *RebindHandler {
	return &RebindHandler{resolver: r, db: database, client: client}
}

// RebindInput is the hera_rebind tool's input schema. RoleName is optional and
// only needed when more than one role holds a live binding at this worktree.
type RebindInput struct {
	Cwd          string `json:"cwd"`
	Orchestrator string `json:"orchestrator"`
	RoleName     string `json:"role_name,omitempty"`
}

// RebindOutput is the success payload.
type RebindOutput struct {
	Orchestrator    string  `json:"orchestrator"`
	RoleName        string  `json:"role_name"`
	Kind            string  `json:"kind"`
	ArgusTaskID     string  `json:"argus_task_id"`
	WorktreePath    string  `json:"worktree_path"`
	BindingID       int64   `json:"binding_id"`
	Reconciled      bool    `json:"reconciled"` // false when the binding was already consistent (no-op)
	EndedBindingIDs []int64 `json:"ended_binding_ids,omitempty"`
	Detail          string  `json:"detail"`
}

// Handle implements Handler.
func (h *RebindHandler) Handle(ctx context.Context, raw json.RawMessage) Response {
	if resp, gated := LinkGate(); gated {
		return resp
	}
	var in RebindInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return ErrorResponse("hera_rebind: invalid input JSON: " + err.Error())
	}
	if in.Cwd == "" {
		return ErrorResponse("hera_rebind: cwd is required")
	}
	if in.Orchestrator == "" {
		return ErrorResponse("hera_rebind: orchestrator is required")
	}

	// The caller's real live task. TaskForCwd disambiguates a shared
	// worktree to the single in_progress task; a genuinely ambiguous cwd
	// (2+ in_progress tasks) surfaces here so we refuse rather than repair
	// against the wrong identity.
	task, err := h.resolver.TaskForCwd(ctx, in.Cwd)
	if err != nil {
		return ErrorResponse("hera_rebind: " + err.Error())
	}

	orch, err := h.db.Orchestrators.GetByName(ctx, in.Orchestrator)
	if errors.Is(err, db.ErrNotFound) {
		return ErrorResponse(fmt.Sprintf("hera_rebind: orchestrator %q does not exist", in.Orchestrator))
	}
	if err != nil {
		return ErrorResponse("hera_rebind: " + err.Error())
	}

	// Gather the caller's live bindings under this orchestrator, keyed both
	// by the resolved task id and by the worktree path. Under BUG-059 these
	// can disagree; the union is the full set of rows in play.
	candidates, err := h.collectCandidates(ctx, task, orch.ID)
	if err != nil {
		return ErrorResponse("hera_rebind: " + err.Error())
	}
	if len(candidates) == 0 {
		return ErrorResponse(fmt.Sprintf(
			"hera_rebind: no live binding to orchestrator %q at this worktree or task; nothing to reconcile. To create a binding, use hera_join with role_name and kind.",
			in.Orchestrator,
		))
	}

	keeperRole, resp, ok := h.pickKeeperRole(ctx, orch.ID, candidates, in.RoleName)
	if !ok {
		return resp
	}

	// The keeper role's own live binding (role-unique, so at most one).
	var keeperBnd *db.Binding
	if b, err := h.db.Bindings.GetLiveByRole(ctx, keeperRole.ID); err == nil {
		keeperBnd = b
	} else if !errors.Is(err, db.ErrNotFound) {
		return ErrorResponse("hera_rebind: load keeper binding: " + err.Error())
	}

	// Who currently occupies the TARGET slots the reconciled binding must own.
	taskOcc, err := h.liveOrNil(ctx, func() (*db.Binding, error) {
		return h.db.Bindings.GetLiveByTaskAndOrchestrator(ctx, task.ID, orch.ID)
	})
	if err != nil {
		return ErrorResponse("hera_rebind: " + err.Error())
	}
	wtOcc, err := h.liveOrNil(ctx, func() (*db.Binding, error) {
		return h.db.Bindings.GetLiveByWorktreeAndOrchestrator(ctx, task.WorktreePath, orch.ID)
	})
	if err != nil {
		return ErrorResponse("hera_rebind: " + err.Error())
	}

	// Already consistent: the keeper binding is the sole occupant of both
	// target slots and already points at the caller's task + worktree.
	if keeperBnd != nil &&
		keeperBnd.ArgusTaskID == task.ID &&
		keeperBnd.WorktreePath == task.WorktreePath &&
		keeperBnd.OrchestratorID == orch.ID &&
		taskOcc != nil && taskOcc.ID == keeperBnd.ID &&
		wtOcc != nil && wtOcc.ID == keeperBnd.ID {
		return jsonText(RebindOutput{
			Orchestrator: orch.Name,
			RoleName:     keeperRole.Name,
			Kind:         string(keeperRole.Kind),
			ArgusTaskID:  keeperBnd.ArgusTaskID,
			WorktreePath: keeperBnd.WorktreePath,
			BindingID:    keeperBnd.ID,
			Reconciled:   false,
			Detail:       "binding already consistent; no change needed",
		})
	}

	// Refuse if a DIFFERENT role's live binding holds a target slot — that is
	// a genuine two-role conflict this verb must not silently resolve.
	if taskOcc != nil && taskOcc.RoleID != keeperRole.ID {
		return ErrorResponse(fmt.Sprintf(
			"hera_rebind: argus task %s under orchestrator %q is already live-bound to a different role (binding %d); refusing to steal it",
			task.ID, in.Orchestrator, taskOcc.ID,
		))
	}
	if wtOcc != nil && wtOcc.RoleID != keeperRole.ID {
		return ErrorResponse(fmt.Sprintf(
			"hera_rebind: worktree %q under orchestrator %q is already live-bound to a different role (binding %d); refusing to steal it",
			task.WorktreePath, in.Orchestrator, wtOcc.ID,
		))
	}

	// Reconcile: end the keeper's stale binding, then insert one clean row
	// pointing at the caller's real task + worktree. Ending + recreating
	// (rather than UPDATE) reuses the DAO's uniqueness enforcement and event
	// emission, and preserves the role — so messages/status/prompt survive.
	var ended []int64
	if keeperBnd != nil {
		if err := h.db.Bindings.End(ctx, keeperBnd.ID, "hera_rebind"); err != nil && !errors.Is(err, db.ErrNotFound) {
			return ErrorResponse("hera_rebind: end stale binding: " + err.Error())
		}
		ended = append(ended, keeperBnd.ID)
	}

	fresh, err := h.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID:         keeperRole.ID,
		OrchestratorID: orch.ID,
		ArgusTaskID:    task.ID,
		WorktreePath:   task.WorktreePath,
	})
	if err != nil {
		return ErrorResponse("hera_rebind: create reconciled binding: " + err.Error())
	}

	// Mirror role kind to argus task_meta so the rail + auto-adopt see it.
	// Best-effort: a transient argus failure must not undo the reconcile.
	_ = h.client.PutTaskMeta(ctx, task.ID, events.MetaKeyRole, string(keeperRole.Kind))

	return jsonText(RebindOutput{
		Orchestrator:    orch.Name,
		RoleName:        keeperRole.Name,
		Kind:            string(keeperRole.Kind),
		ArgusTaskID:     fresh.ArgusTaskID,
		WorktreePath:    fresh.WorktreePath,
		BindingID:       fresh.ID,
		Reconciled:      true,
		EndedBindingIDs: ended,
		Detail:          "binding reconciled to the caller's live argus task",
	})
}

// collectCandidates returns the caller's live bindings under orchID, keyed by
// the resolved task id and by the worktree path, de-duplicated by binding id.
func (h *RebindHandler) collectCandidates(ctx context.Context, task *argus.Task, orchID int64) ([]*db.Binding, error) {
	seen := map[int64]bool{}
	var out []*db.Binding
	add := func(b *db.Binding) {
		if b != nil && !seen[b.ID] {
			seen[b.ID] = true
			out = append(out, b)
		}
	}
	if b, err := h.db.Bindings.GetLiveByTaskAndOrchestrator(ctx, task.ID, orchID); err == nil {
		add(b)
	} else if !errors.Is(err, db.ErrNotFound) {
		return nil, err
	}
	if task.WorktreePath != "" {
		if b, err := h.db.Bindings.GetLiveByWorktreeAndOrchestrator(ctx, task.WorktreePath, orchID); err == nil {
			add(b)
		} else if !errors.Is(err, db.ErrNotFound) {
			return nil, err
		}
	}
	return out, nil
}

// pickKeeperRole resolves which role's binding to reconcile. With an explicit
// role_name it must name a role that actually holds one of the candidate
// bindings; without one, exactly one role must be represented among the
// candidates. Any other shape is genuinely ambiguous and refused. The boolean
// is false when the returned Response is a refusal to surface.
func (h *RebindHandler) pickKeeperRole(ctx context.Context, orchID int64, candidates []*db.Binding, roleName string) (*db.Role, Response, bool) {
	roleIDs := map[int64]bool{}
	for _, b := range candidates {
		roleIDs[b.RoleID] = true
	}

	if roleName != "" {
		role, err := h.db.Roles.GetByOrchestratorAndName(ctx, orchID, roleName)
		if errors.Is(err, db.ErrNotFound) {
			return nil, ErrorResponse(fmt.Sprintf("hera_rebind: role %q does not exist under this orchestrator", roleName)), false
		}
		if err != nil {
			return nil, ErrorResponse("hera_rebind: load role: " + err.Error()), false
		}
		if !roleIDs[role.ID] {
			return nil, ErrorResponse(fmt.Sprintf(
				"hera_rebind: role %q has no live binding at this worktree or task; candidates: %s",
				roleName, h.roleNames(ctx, candidates),
			)), false
		}
		return role, Response{}, true
	}

	if len(roleIDs) > 1 {
		return nil, ErrorResponse(fmt.Sprintf(
			"hera_rebind: multiple roles hold live bindings here (%s); pass role_name to pick which to reconcile",
			h.roleNames(ctx, candidates),
		)), false
	}

	// Exactly one role among the candidates.
	role, err := h.db.Roles.GetByID(ctx, candidates[0].RoleID)
	if err != nil {
		return nil, ErrorResponse("hera_rebind: load role: " + err.Error()), false
	}
	return role, Response{}, true
}

// roleNames renders a comma-joined list of the candidate bindings' role names
// for ambiguity messages. Unresolvable ids fall back to a "role <id>" token.
func (h *RebindHandler) roleNames(ctx context.Context, candidates []*db.Binding) string {
	out := ""
	for i, b := range candidates {
		name := fmt.Sprintf("role %d", b.RoleID)
		if role, err := h.db.Roles.GetByID(ctx, b.RoleID); err == nil {
			name = role.Name
		}
		if i > 0 {
			out += ", "
		}
		out += name
	}
	return out
}

// liveOrNil runs a single-binding lookup and folds ErrNotFound into (nil, nil).
func (h *RebindHandler) liveOrNil(ctx context.Context, fn func() (*db.Binding, error)) (*db.Binding, error) {
	b, err := fn()
	if errors.Is(err, db.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return b, nil
}
