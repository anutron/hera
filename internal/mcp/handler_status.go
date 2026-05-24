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

// StatusHandler implements hera_status. It updates the caller role's
// status and mirrors the value to argus task_meta so other observers can
// see it.
type StatusHandler struct {
	resolver *Resolver
	db       *db.DB
	client   *argus.Client
}

// NewStatusHandler constructs a StatusHandler.
func NewStatusHandler(r *Resolver, database *db.DB, client *argus.Client) *StatusHandler {
	return &StatusHandler{resolver: r, db: database, client: client}
}

// StatusInput is the tool's input schema.
type StatusInput struct {
	Cwd    string `json:"cwd"`
	Status string `json:"status"`
}

// StatusOutput is the success payload.
type StatusOutput struct {
	RoleName     string `json:"role_name"`
	Status       string `json:"status"`
	UpdatedAt    string `json:"updated_at"`
	MetaMirrored bool   `json:"meta_mirrored"`
}

// Handle implements Handler.
func (h *StatusHandler) Handle(ctx context.Context, raw json.RawMessage) Response {
	var in StatusInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return ErrorResponse("hera_status: invalid input JSON: " + err.Error())
	}
	if in.Cwd == "" {
		return ErrorResponse("hera_status: cwd is required")
	}
	if in.Status == "" {
		return ErrorResponse("hera_status: status is required")
	}
	s := db.RoleStatusValue(in.Status)
	switch s {
	case db.StatusIdle, db.StatusWorking, db.StatusBlocked, db.StatusDone:
		// ok
	default:
		return ErrorResponse(fmt.Sprintf("hera_status: invalid status %q (must be one of: idle, working, blocked, done)", in.Status))
	}

	_, role, bnd, err := h.resolver.CallerRole(ctx, in.Cwd)
	if err != nil {
		if errors.Is(err, ErrNoBinding) {
			return ErrorResponse("hera_status: " + err.Error())
		}
		return ErrorResponse("hera_status: " + err.Error())
	}

	if err := h.db.RoleStatus.Upsert(ctx, role.ID, s); err != nil {
		return ErrorResponse("hera_status: persist status: " + err.Error())
	}

	// Mirror to argus task_meta. Best-effort; report success on the
	// response but don't fail the call if the write errors out.
	mirrored := true
	if err := h.client.PutTaskMeta(ctx, bnd.ArgusTaskID, events.MetaKeyThreadStatus, in.Status); err != nil {
		mirrored = false
	}

	updatedAt := ""
	if rs, err := h.db.RoleStatus.Get(ctx, role.ID); err == nil {
		updatedAt = rs.UpdatedAt.Format("2006-01-02T15:04:05.000Z07:00")
	}

	return jsonText(StatusOutput{
		RoleName:     role.Name,
		Status:       in.Status,
		UpdatedAt:    updatedAt,
		MetaMirrored: mirrored,
	})
}
