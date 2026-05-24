package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/anutron/ludwig/internal/db"
)

// JoinHandler implements the ludwig_join MCP tool.
//
// Bare ludwig_join(cwd) is the re-incarnation claim: resolve cwd → task →
// binding → role, return the role's identity.
//
// ludwig_join with extended args (orchestrator, role_name, kind, ...) is
// the freelance-attach path: validate orchestrator exists, validate
// (orchestrator, role_name) does not already exist with a different
// kind, create role + binding + initial status atomically.
type JoinHandler struct {
	resolver *Resolver
	db       *db.DB
}

// NewJoinHandler constructs a JoinHandler.
func NewJoinHandler(r *Resolver, database *db.DB) *JoinHandler {
	return &JoinHandler{resolver: r, db: database}
}

// JoinInput is the ludwig_join tool's input schema.
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
	var in JoinInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return ErrorResponse("ludwig_join: invalid input JSON: " + err.Error())
	}
	if in.Cwd == "" {
		return ErrorResponse("ludwig_join: cwd is required")
	}

	task, err := h.resolver.TaskForCwd(ctx, in.Cwd)
	if err != nil {
		return ErrorResponse("ludwig_join: " + err.Error())
	}

	// Branch on freelance-attach vs re-incarnation.
	if in.Orchestrator != "" || in.RoleName != "" || in.Kind != "" {
		return h.freelance(ctx, task.ID, task.Project, task.WorktreePath, in)
	}
	return h.reincarnation(ctx, task.ID)
}

// reincarnation handles the bare ludwig_join(cwd) call.
func (h *JoinHandler) reincarnation(ctx context.Context, taskID string) Response {
	bnd, err := h.db.Bindings.GetLiveByTaskID(ctx, taskID)
	if errors.Is(err, db.ErrNotFound) {
		return ErrorResponse(
			"ludwig_join: this argus task is not bound to any ludwig role. " +
				"To attach as a freelance, call ludwig_join with explicit orchestrator, role_name, kind=\"freelance\", and (optional) mission/constraints/status.",
		)
	}
	if err != nil {
		return ErrorResponse("ludwig_join: " + err.Error())
	}
	role, err := h.db.Roles.GetByID(ctx, bnd.RoleID)
	if err != nil {
		return ErrorResponse("ludwig_join: load role: " + err.Error())
	}
	orch, err := h.db.Orchestrators.GetByID(ctx, role.OrchestratorID)
	if err != nil {
		return ErrorResponse("ludwig_join: load orchestrator: " + err.Error())
	}
	unread, err := h.db.Messages.CountUnreadForRole(ctx, role.ID)
	if err != nil {
		return ErrorResponse("ludwig_join: count unread: " + err.Error())
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

// freelance handles ludwig_join with explicit (orchestrator, role_name, kind, ...) args.
func (h *JoinHandler) freelance(ctx context.Context, argusTaskID, project, worktreePath string, in JoinInput) Response {
	if in.Orchestrator == "" || in.RoleName == "" || in.Kind == "" {
		return ErrorResponse("ludwig_join: orchestrator, role_name, and kind are all required for freelance attach")
	}
	kind := db.RoleKind(in.Kind)
	switch kind {
	case db.KindWorker, db.KindFreelance, db.KindCoordinator:
		// ok
	default:
		return ErrorResponse(fmt.Sprintf("ludwig_join: invalid kind %q (must be worker, freelance, or coordinator)", in.Kind))
	}

	orch, err := h.db.Orchestrators.GetByName(ctx, in.Orchestrator)
	if errors.Is(err, db.ErrNotFound) {
		return ErrorResponse(fmt.Sprintf("ludwig_join: orchestrator %q does not exist", in.Orchestrator))
	}
	if err != nil {
		return ErrorResponse("ludwig_join: " + err.Error())
	}

	if existing, err := h.db.Roles.GetByOrchestratorAndName(ctx, orch.ID, in.RoleName); err == nil && existing.Kind != kind {
		return ErrorResponse(fmt.Sprintf(
			"ludwig_join: role %q in orchestrator %q already exists with kind %q (not %q)",
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
		return ErrorResponse("ludwig_join: create role: " + err.Error())
	}

	bnd, err := h.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID:       role.ID,
		ArgusTaskID:  argusTaskID,
		WorktreePath: worktreePath,
	})
	if err != nil {
		return ErrorResponse("ludwig_join: create binding: " + err.Error())
	}

	if in.Status != "" {
		s := db.RoleStatusValue(in.Status)
		switch s {
		case db.StatusIdle, db.StatusWorking, db.StatusBlocked, db.StatusDone:
			if err := h.db.RoleStatus.Upsert(ctx, role.ID, s); err != nil {
				return ErrorResponse("ludwig_join: set status: " + err.Error())
			}
		default:
			return ErrorResponse(fmt.Sprintf("ludwig_join: invalid status %q", in.Status))
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
		return ErrorResponse("ludwig: marshal response: " + err.Error())
	}
	return TextResponse(string(b))
}
