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

// JoinHandler implements the hera_join MCP tool.
//
// Three calling shapes:
//
//   - hera_join(cwd) — claim mode, no orchestrator. Resolves the
//     calling task's single live binding. If 0 bindings, errors with a
//     hint to attach or new_orchestrator. If 2+, errors listing each
//     binding's orchestrator/role/kind and directs the caller to
//     re-invoke with orchestrator=<name>.
//   - hera_join(cwd, orchestrator=X) — claim mode, explicit. Returns
//     the binding's identity for orchestrator X, or errors with the
//     attach signature if no such binding exists.
//   - hera_join(cwd, orchestrator=X, role_name=R, kind=K, ...) —
//     attach mode. Worker or freelance only; coordinator bootstrap
//     uses hera_new_orchestrator. Rejects only when the calling task
//     already has a live binding TO ORCHESTRATOR X; bindings to other
//     orchestrators are accepted (multi-binding).
type JoinHandler struct {
	resolver *Resolver
	db       *db.DB
	client   *argus.Client
}

// NewJoinHandler constructs a JoinHandler.
func NewJoinHandler(r *Resolver, database *db.DB, client *argus.Client) *JoinHandler {
	return &JoinHandler{resolver: r, db: database, client: client}
}

// JoinInput is the hera_join tool's input schema.
type JoinInput struct {
	Cwd          string `json:"cwd"`
	Orchestrator string `json:"orchestrator,omitempty"`
	RoleName     string `json:"role_name,omitempty"`
	Kind         string `json:"kind,omitempty"`
	Mission      string `json:"mission,omitempty"`
	Constraints  string `json:"constraints,omitempty"`
	Status       string `json:"status,omitempty"`
}

// JoinOutput is the success payload (returned as a JSON-formatted text block).
type JoinOutput struct {
	Orchestrator       string `json:"orchestrator"`
	RoleName           string `json:"role_name"`
	Kind               string `json:"kind"`
	Mission            string `json:"mission"`
	Constraints        string `json:"constraints"`
	Status             string `json:"status,omitempty"`
	UnreadMessageCount int    `json:"unread_message_count"`
	BindingID          int64  `json:"binding_id"`
	ArgusTaskID        string `json:"argus_task_id"`
}

// Handle implements Handler.
func (h *JoinHandler) Handle(ctx context.Context, raw json.RawMessage) Response {
	if resp, gated := LinkGate(); gated {
		return resp
	}
	var in JoinInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return ErrorResponse("hera_join: invalid input JSON: " + err.Error())
	}
	if in.Cwd == "" {
		return ErrorResponse("hera_join: cwd is required")
	}

	task, err := h.resolver.TaskForCwd(ctx, in.Cwd)
	if err != nil {
		return ErrorResponse("hera_join: " + err.Error())
	}

	// Attach mode requires role_name AND kind. Anything else (no
	// role_name, no kind) is claim mode; orchestrator may or may not
	// be specified.
	if in.RoleName != "" || in.Kind != "" {
		return h.attach(ctx, task.ID, task.Project, task.WorktreePath, in)
	}
	return h.claim(ctx, task.ID, in.Orchestrator)
}

// claim handles the bare or orchestrator-only hera_join — re-incarnation:
// look up the calling task's live binding for the given orchestrator (or
// the single binding when no orchestrator is supplied), and return the
// role's identity.
func (h *JoinHandler) claim(ctx context.Context, taskID, orchestrator string) Response {
	if orchestrator != "" {
		orch, err := h.db.Orchestrators.GetByName(ctx, orchestrator)
		if errors.Is(err, db.ErrNotFound) {
			return ErrorResponse(fmt.Sprintf(
				"hera_join: orchestrator %q does not exist. To bootstrap a new orchestrator, call hera_new_orchestrator. To attach as a worker or freelance to an existing one, supply role_name and kind.",
				orchestrator,
			))
		}
		if err != nil {
			return ErrorResponse("hera_join: " + err.Error())
		}
		bnd, err := h.db.Bindings.GetLiveByTaskAndOrchestrator(ctx, taskID, orch.ID)
		if errors.Is(err, db.ErrNotFound) {
			return ErrorResponse(fmt.Sprintf(
				"hera_join: this argus task is not bound to orchestrator %q. To attach, call hera_join with role_name and kind.",
				orchestrator,
			))
		}
		if err != nil {
			return ErrorResponse("hera_join: " + err.Error())
		}
		return h.identityResponse(ctx, bnd)
	}

	bindings, err := h.db.Bindings.ListLiveByTaskID(ctx, taskID)
	if err != nil {
		return ErrorResponse("hera_join: " + err.Error())
	}
	switch len(bindings) {
	case 0:
		return ErrorResponse(
			"hera_join: this argus task is not bound to any hera role. " +
				"To attach as a freelance, call hera_join with explicit orchestrator, role_name, kind=\"freelance\", and (optional) mission/constraints/status. " +
				"To bootstrap a new orchestrator, call hera_new_orchestrator.",
		)
	case 1:
		return h.identityResponse(ctx, bindings[0])
	default:
		return ErrorResponse("hera_join: " + h.resolver.buildAmbiguousError(ctx, bindings).Error())
	}
}

// identityResponse loads the role + orchestrator + status + inbox-count
// for a binding and returns the standard JoinOutput.
func (h *JoinHandler) identityResponse(ctx context.Context, bnd *db.Binding) Response {
	role, err := h.db.Roles.GetByID(ctx, bnd.RoleID)
	if err != nil {
		return ErrorResponse("hera_join: load role: " + err.Error())
	}
	orch, err := h.db.Orchestrators.GetByID(ctx, role.OrchestratorID)
	if err != nil {
		return ErrorResponse("hera_join: load orchestrator: " + err.Error())
	}
	unread, err := h.db.Messages.CountUnreadForRole(ctx, role.ID)
	if err != nil {
		return ErrorResponse("hera_join: count unread: " + err.Error())
	}
	statusVal := ""
	if rs, err := h.db.RoleStatus.Get(ctx, role.ID); err == nil {
		statusVal = string(rs.Status)
	}
	return jsonText(JoinOutput{
		Orchestrator:       orch.Name,
		RoleName:           role.Name,
		Kind:               string(role.Kind),
		Mission:            role.Mission,
		Constraints:        role.Constraints,
		Status:             statusVal,
		UnreadMessageCount: unread,
		BindingID:          bnd.ID,
		ArgusTaskID:        bnd.ArgusTaskID,
	})
}

// attach handles hera_join with explicit (orchestrator, role_name, kind, ...) args.
// Kind must be worker or freelance; coordinator bootstrap lives in hera_new_orchestrator.
func (h *JoinHandler) attach(ctx context.Context, argusTaskID, project, worktreePath string, in JoinInput) Response {
	if in.Orchestrator == "" || in.RoleName == "" || in.Kind == "" {
		return ErrorResponse("hera_join: orchestrator, role_name, and kind are all required for attach")
	}
	kind := db.RoleKind(in.Kind)
	switch kind {
	case db.KindWorker, db.KindFreelance:
		// ok
	case db.KindCoordinator:
		return ErrorResponse("hera_join: kind=coordinator is not supported by hera_join; use hera_new_orchestrator to bootstrap a new orchestrator")
	default:
		return ErrorResponse(fmt.Sprintf("hera_join: invalid kind %q (must be worker or freelance)", in.Kind))
	}

	orch, err := h.db.Orchestrators.GetByName(ctx, in.Orchestrator)
	if errors.Is(err, db.ErrNotFound) {
		return ErrorResponse(fmt.Sprintf("hera_join: orchestrator %q does not exist", in.Orchestrator))
	}
	if err != nil {
		return ErrorResponse("hera_join: " + err.Error())
	}

	// Reject only if the calling argus task is already bound to THIS
	// orchestrator. Bindings to other orchestrators are fine — that's
	// the multi-binding case.
	if _, err := h.db.Bindings.GetLiveByTaskAndOrchestrator(ctx, argusTaskID, orch.ID); err == nil {
		return ErrorResponse(fmt.Sprintf(
			"hera_join: this argus task is already bound to orchestrator %q; call hera_join(cwd, orchestrator=%q) with no role_name to claim the existing binding",
			in.Orchestrator, in.Orchestrator,
		))
	} else if !errors.Is(err, db.ErrNotFound) {
		return ErrorResponse("hera_join: lookup existing binding: " + err.Error())
	}

	if existing, err := h.db.Roles.GetByOrchestratorAndName(ctx, orch.ID, in.RoleName); err == nil && existing.Kind != kind {
		return ErrorResponse(fmt.Sprintf(
			"hera_join: role %q in orchestrator %q already exists with kind %q (not %q)",
			in.RoleName, in.Orchestrator, existing.Kind, kind,
		))
	}

	role, err := h.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID,
		Name:           in.RoleName,
		Kind:           kind,
		ArgusProject:   project,
		Mission:        in.Mission,
		Constraints:    in.Constraints,
	})
	if err != nil {
		return ErrorResponse("hera_join: create role: " + err.Error())
	}

	bnd, err := h.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID:         role.ID,
		OrchestratorID: orch.ID,
		ArgusTaskID:    argusTaskID,
		WorktreePath:   worktreePath,
	})
	if err != nil {
		return ErrorResponse("hera_join: create binding: " + err.Error())
	}

	// Mirror meta:hera.role to the bound argus task. Best-effort: a
	// transient argus failure shouldn't undo the binding.
	_ = h.client.PutTaskMeta(ctx, argusTaskID, events.MetaKeyRole, string(kind))

	if in.Status != "" {
		s := db.RoleStatusValue(in.Status)
		switch s {
		case db.StatusIdle, db.StatusWorking, db.StatusBlocked, db.StatusDone:
			if err := h.db.RoleStatus.Upsert(ctx, role.ID, s); err != nil {
				return ErrorResponse("hera_join: set status: " + err.Error())
			}
		default:
			return ErrorResponse(fmt.Sprintf("hera_join: invalid status %q", in.Status))
		}
	}

	return jsonText(JoinOutput{
		Orchestrator: orch.Name,
		RoleName:     role.Name,
		Kind:         string(role.Kind),
		Mission:      role.Mission,
		Constraints:  role.Constraints,
		Status:       in.Status,
		BindingID:    bnd.ID,
		ArgusTaskID:  argusTaskID,
	})
}

// jsonText marshals v and wraps it in a text content block.
func jsonText(v any) Response {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return ErrorResponse("hera: marshal response: " + err.Error())
	}
	return TextResponse(string(b))
}
