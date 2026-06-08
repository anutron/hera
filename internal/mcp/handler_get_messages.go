package mcp

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/anutron/hera/internal/db"
)

// GetMessagesHandler implements hera_get_messages. Fetches full message bodies
// by ID list. Access is restricted to messages where the sender or recipient
// belongs to the caller's orchestrator subtree.
type GetMessagesHandler struct {
	resolver *Resolver
	db       *db.DB
}

// NewGetMessagesHandler constructs a GetMessagesHandler.
func NewGetMessagesHandler(r *Resolver, database *db.DB) *GetMessagesHandler {
	return &GetMessagesHandler{resolver: r, db: database}
}

// GetMessagesInput is the tool's input schema.
type GetMessagesInput struct {
	Cwd          string  `json:"cwd"`
	Orchestrator string  `json:"orchestrator,omitempty"`
	IDs          []int64 `json:"ids"`
}

// GetMessageResult is one message in the response. Error is non-empty for
// inaccessible or missing messages (no top-level error is returned for
// per-ID failures).
type GetMessageResult struct {
	ID               int64  `json:"id"`
	SentAt           string `json:"sent_at,omitempty"`
	FromRole         string `json:"from_role,omitempty"`
	FromOrchestrator string `json:"from_orchestrator,omitempty"`
	ToRole           string `json:"to_role,omitempty"`
	ToOrchestrator   string `json:"to_orchestrator,omitempty"`
	Tldr             string `json:"tldr,omitempty"`
	Body             string `json:"body,omitempty"`
	Error            string `json:"error,omitempty"`
}

// GetMessagesOutput is the success payload.
type GetMessagesOutput struct {
	Messages []GetMessageResult `json:"messages"`
}

// Handle implements Handler.
func (h *GetMessagesHandler) Handle(ctx context.Context, raw json.RawMessage) Response {
	if resp, gated := LinkGate(); gated {
		return resp
	}
	var in GetMessagesInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return ErrorResponse("hera_get_messages: invalid input JSON: " + err.Error())
	}
	if in.Cwd == "" {
		return ErrorResponse("hera_get_messages: cwd is required")
	}
	if len(in.IDs) == 0 {
		return ErrorResponse("hera_get_messages: ids must contain at least one id")
	}

	_, _, orch, err := h.resolver.CallerRole(ctx, in.Cwd, in.Orchestrator)
	if err != nil {
		return ErrorResponse("hera_get_messages: " + err.Error())
	}

	orchIDs, _, err := db.SubtreeOrchIDs(ctx, h.db.Raw(), orch.ID, 6)
	if err != nil {
		return ErrorResponse("hera_get_messages: subtree: " + err.Error())
	}
	subtreeSet := make(map[int64]struct{}, len(orchIDs))
	for _, id := range orchIDs {
		subtreeSet[id] = struct{}{}
	}

	out := GetMessagesOutput{Messages: make([]GetMessageResult, 0, len(in.IDs))}
	for _, msgID := range in.IDs {
		result := h.fetchOne(ctx, msgID, subtreeSet)
		out.Messages = append(out.Messages, result)
	}
	return jsonText(out)
}

func (h *GetMessagesHandler) fetchOne(ctx context.Context, msgID int64, subtreeSet map[int64]struct{}) GetMessageResult {
	msg, err := h.db.Messages.GetByID(ctx, msgID)
	if errors.Is(err, db.ErrNotFound) {
		return GetMessageResult{ID: msgID, Error: "not found"}
	}
	if err != nil {
		return GetMessageResult{ID: msgID, Error: "fetch error: " + err.Error()}
	}

	// Resolve sender role + orchestrator.
	fromRole, _ := h.db.Roles.GetByID(ctx, msg.FromRoleID)
	toRole, _ := h.db.Roles.GetByID(ctx, msg.ToRoleID)

	// Access check: sender or recipient must be in the caller's subtree.
	inSubtree := false
	if fromRole != nil {
		if _, ok := subtreeSet[fromRole.OrchestratorID]; ok {
			inSubtree = true
		}
	}
	if toRole != nil {
		if _, ok := subtreeSet[toRole.OrchestratorID]; ok {
			inSubtree = true
		}
	}
	if !inSubtree {
		return GetMessageResult{ID: msgID, Error: "access denied: message not in caller's subtree"}
	}

	// Resolve orchestrator names.
	var fromOrchName, toOrchName string
	if fromRole != nil {
		if o, err := h.db.Orchestrators.GetByID(ctx, fromRole.OrchestratorID); err == nil {
			fromOrchName = o.Name
		}
	}
	if toRole != nil {
		if o, err := h.db.Orchestrators.GetByID(ctx, toRole.OrchestratorID); err == nil {
			toOrchName = o.Name
		}
	}

	fromRoleName := ""
	if fromRole != nil {
		fromRoleName = fromRole.Name
	}
	toRoleName := ""
	if toRole != nil {
		toRoleName = toRole.Name
	}

	return GetMessageResult{
		ID:               msg.ID,
		SentAt:           msg.SentAt.Format("2006-01-02T15:04:05.000Z07:00"),
		FromRole:         fromRoleName,
		FromOrchestrator: fromOrchName,
		ToRole:           toRoleName,
		ToOrchestrator:   toOrchName,
		Tldr:             msg.Tldr,
		Body:             msg.Body,
	}
}
