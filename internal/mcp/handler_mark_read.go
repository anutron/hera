package mcp

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"

	"github.com/anutron/hera/internal/db"
)

// MarkReadHandler implements hera_mark_read. Marks one or more messages read
// for the caller's role and cancels pending argus deliveries (best-effort).
type MarkReadHandler struct {
	resolver *Resolver
	db       *db.DB
	argus    ArgusNotifyCanceller
}

// NewMarkReadHandler constructs a MarkReadHandler.
func NewMarkReadHandler(r *Resolver, database *db.DB, argusClient ArgusNotifyCanceller) *MarkReadHandler {
	return &MarkReadHandler{resolver: r, db: database, argus: argusClient}
}

// MarkReadInput is the tool's input schema.
type MarkReadInput struct {
	Cwd          string  `json:"cwd"`
	MessageIDs   []int64 `json:"message_ids"`
	Orchestrator string  `json:"orchestrator,omitempty"`
}

// MarkReadOutput is the success payload.
type MarkReadOutput struct {
	RoleName        string `json:"role_name"`
	MarkedReadCount int    `json:"marked_read_count"`
}

// Handle implements Handler.
func (h *MarkReadHandler) Handle(ctx context.Context, raw json.RawMessage) Response {
	if resp, gated := LinkGate(); gated {
		return resp
	}
	var in MarkReadInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return ErrorResponse("hera_mark_read: invalid input JSON: " + err.Error())
	}
	if in.Cwd == "" {
		return ErrorResponse("hera_mark_read: cwd is required")
	}
	if len(in.MessageIDs) == 0 {
		return ErrorResponse("hera_mark_read: message_ids must contain at least one id")
	}

	_, role, _, err := h.resolver.CallerRole(ctx, in.Cwd, in.Orchestrator)
	if err != nil {
		return ErrorResponse("hera_mark_read: " + err.Error())
	}

	n, err := h.db.Messages.MarkRead(ctx, role.ID, in.MessageIDs)
	if err != nil {
		return ErrorResponse("hera_mark_read: db: " + err.Error())
	}

	// Cancel argus delivery for each marked-read message — best-effort.
	h.cancelDeliveries(ctx, role.ID, in.MessageIDs)

	return jsonText(MarkReadOutput{RoleName: role.Name, MarkedReadCount: n})
}

// cancelDeliveries calls argus CancelNotify for each message ID best-effort.
// Errors are logged at debug level and never fail the handler response.
func (h *MarkReadHandler) cancelDeliveries(ctx context.Context, roleID int64, messageIDs []int64) {
	bnd, err := h.db.Bindings.GetLiveByRole(ctx, roleID)
	if err != nil {
		return // no live binding; nothing to cancel
	}
	for _, id := range messageIDs {
		if err := h.argus.CancelNotify(ctx, bnd.ArgusTaskID, strconv.FormatInt(id, 10)); err != nil {
			slog.Debug("hera_mark_read: cancel delivery", "msg_id", id, "err", err)
		}
	}
}
