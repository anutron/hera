package mcp

import (
	"context"
	"encoding/json"

	"github.com/anutron/hera/internal/db"
)

// MarkReadHandler implements hera_mark_read. Marks one or more
// messages read for the caller's role. Messages belonging to other roles
// are silently skipped.
type MarkReadHandler struct {
	resolver *Resolver
	db       *db.DB
}

// NewMarkReadHandler constructs a MarkReadHandler.
func NewMarkReadHandler(r *Resolver, database *db.DB) *MarkReadHandler {
	return &MarkReadHandler{resolver: r, db: database}
}

// MarkReadInput is the tool's input schema.
type MarkReadInput struct {
	Cwd        string  `json:"cwd"`
	MessageIDs []int64 `json:"message_ids"`
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

	_, role, _, err := h.resolver.CallerRole(ctx, in.Cwd)
	if err != nil {
		return ErrorResponse("hera_mark_read: " + err.Error())
	}

	n, err := h.db.Messages.MarkRead(ctx, role.ID, in.MessageIDs)
	if err != nil {
		return ErrorResponse("hera_mark_read: db: " + err.Error())
	}

	return jsonText(MarkReadOutput{RoleName: role.Name, MarkedReadCount: n})
}
