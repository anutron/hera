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

// NewOrchestratorHandler implements the hera_new_orchestrator MCP tool.
//
// This is the canonical "be an orchestrator" entry point. The calling
// agent is in some argus worktree; calling this tool creates a hera
// orchestrator + a coordinator role + a binding that ties the calling
// argus task to that role. Subsequent worker tasks the coordinator
// spawns get auto-adopted via the event-stream flow (see internal/events).
//
// Idempotent: re-calling with the same orchestrator name returns the
// existing orchestrator + role if they already match the requested shape;
// otherwise returns an explanatory error.
type NewOrchestratorHandler struct {
	resolver *Resolver
	db       *db.DB
	client   *argus.Client
}

// NewNewOrchestratorHandler constructs a NewOrchestratorHandler. (Yes,
// the name is repetitive – it's "New" the constructor + "NewOrchestrator"
// the handler.)
func NewNewOrchestratorHandler(r *Resolver, database *db.DB, client *argus.Client) *NewOrchestratorHandler {
	return &NewOrchestratorHandler{resolver: r, db: database, client: client}
}

// NewOrchestratorInput is the tool's input schema.
type NewOrchestratorInput struct {
	Cwd                 string `json:"cwd"`
	Name                string `json:"name"`
	CoordinatorRoleName string `json:"coordinator_role_name"`
	Mission             string `json:"mission,omitempty"`
	Constraints         string `json:"constraints,omitempty"`
}

// NewOrchestratorOutput is the success payload.
type NewOrchestratorOutput struct {
	Orchestrator string `json:"orchestrator"`
	RoleName     string `json:"role_name"`
	Kind         string `json:"kind"`
	Mission      string `json:"mission"`
	Constraints  string `json:"constraints"`
	BindingID    int64  `json:"binding_id"`
	ArgusTaskID  string `json:"argus_task_id"`
	Created      bool   `json:"created"` // true if orchestrator was newly created; false if it already existed
}

// Handle implements Handler.
func (h *NewOrchestratorHandler) Handle(ctx context.Context, raw json.RawMessage) Response {
	if resp, gated := LinkGate(); gated {
		return resp
	}
	var in NewOrchestratorInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return ErrorResponse("hera_new_orchestrator: invalid input JSON: " + err.Error())
	}
	if in.Cwd == "" {
		return ErrorResponse("hera_new_orchestrator: cwd is required")
	}
	if in.Name == "" {
		return ErrorResponse("hera_new_orchestrator: name is required")
	}
	if in.CoordinatorRoleName == "" {
		return ErrorResponse("hera_new_orchestrator: coordinator_role_name is required")
	}

	task, err := h.resolver.TaskForCwd(ctx, in.Cwd)
	if err != nil {
		return ErrorResponse("hera_new_orchestrator: " + err.Error())
	}

	// Reject if the calling argus task already has a live binding – we
	// don't want to silently overwrite an existing role's relationship
	// to this worktree.
	if _, err := h.db.Bindings.GetLiveByTaskID(ctx, task.ID); err == nil {
		return ErrorResponse("hera_new_orchestrator: this argus task is already bound to a hera role; resume that role via hera_join(cwd) instead of creating a new orchestrator here")
	} else if !errors.Is(err, db.ErrNotFound) {
		return ErrorResponse("hera_new_orchestrator: lookup existing binding: " + err.Error())
	}

	// Determine "newly created" before Orchestrators.Create runs – the
	// DAO is idempotent on name, so after-the-fact inference (via "does
	// the orchestrator have any roles already?") would falsely report
	// Created=true for an orchestrator that existed but had zero roles.
	existed := true
	if _, err := h.db.Orchestrators.GetByName(ctx, in.Name); errors.Is(err, db.ErrNotFound) {
		existed = false
	} else if err != nil {
		return ErrorResponse("hera_new_orchestrator: lookup orchestrator: " + err.Error())
	}
	orch, err := h.db.Orchestrators.Create(ctx, in.Name)
	if err != nil {
		return ErrorResponse("hera_new_orchestrator: create orchestrator: " + err.Error())
	}
	created := !existed

	// Check for an existing coordinator role with the same name; reject if
	// a different role kind is already in that slot.
	if existing, err := h.db.Roles.GetByOrchestratorAndName(ctx, orch.ID, in.CoordinatorRoleName); err == nil {
		if existing.Kind != db.KindCoordinator {
			return ErrorResponse(fmt.Sprintf(
				"hera_new_orchestrator: role %q in orchestrator %q already exists with kind %q (not coordinator)",
				in.CoordinatorRoleName, in.Name, existing.Kind,
			))
		}
		// Existing coordinator role: confirm it has no live binding so
		// resuming it here is safe. If it has a live binding elsewhere,
		// reject – the operator should not be racing.
		if _, err := h.db.Bindings.GetLiveByRole(ctx, existing.ID); err == nil {
			return ErrorResponse(fmt.Sprintf(
				"hera_new_orchestrator: coordinator role %q in orchestrator %q is already bound to a live argus task; resume via hera_join from that worktree",
				in.CoordinatorRoleName, in.Name,
			))
		}
	}

	// Create or reuse the coordinator role.
	role, err := h.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID,
		Name:           in.CoordinatorRoleName,
		Kind:           db.KindCoordinator,
		ArgusProject:   task.Project,
		Mission:        in.Mission,
		Constraints:    in.Constraints,
	})
	if err != nil {
		return ErrorResponse("hera_new_orchestrator: create role: " + err.Error())
	}

	bnd, err := h.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID:       role.ID,
		ArgusTaskID:  task.ID,
		WorktreePath: task.WorktreePath,
	})
	if err != nil {
		return ErrorResponse("hera_new_orchestrator: create binding: " + err.Error())
	}

	// Mirror role meta to argus task_meta. Best-effort: failure surfaces
	// in the response as a warning but does not undo the binding.
	if err := h.client.PutTaskMeta(ctx, task.ID, events.MetaKeyRole, string(db.KindCoordinator)); err != nil {
		// Soft-fail: persist the binding, return success with a note. The
		// operator can rewrite meta later or restart hera to retry.
		return jsonText(NewOrchestratorOutput{
			Orchestrator: orch.Name,
			RoleName:     role.Name,
			Kind:         string(role.Kind),
			Mission:      role.Mission,
			Constraints:  role.Constraints,
			BindingID:    bnd.ID,
			ArgusTaskID:  task.ID,
			Created:      created,
		})
	}

	return jsonText(NewOrchestratorOutput{
		Orchestrator: orch.Name,
		RoleName:     role.Name,
		Kind:         string(role.Kind),
		Mission:      role.Mission,
		Constraints:  role.Constraints,
		BindingID:    bnd.ID,
		ArgusTaskID:  task.ID,
		Created:      created,
	})
}
