package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/anutron/hera/internal/db"
)

// ArgusNotifyCanceller is the subset of argus.Client used to cancel pending
// deliveries when messages are marked read.
type ArgusNotifyCanceller interface {
	CancelNotify(ctx context.Context, taskID, deliveryID string) error
}

// InboxHandler implements hera_inbox. Returns every unread message
// addressed to the caller's role, oldest-first, then cancels pending argus
// deliveries for those messages (best-effort).
type InboxHandler struct {
	resolver *Resolver
	db       *db.DB
	argus    ArgusNotifyCanceller
}

// NewInboxHandler constructs an InboxHandler.
func NewInboxHandler(r *Resolver, database *db.DB, argusClient ArgusNotifyCanceller) *InboxHandler {
	return &InboxHandler{resolver: r, db: database, argus: argusClient}
}

// InboxInput is the tool's input schema.
type InboxInput struct {
	Cwd          string `json:"cwd"`
	Orchestrator string `json:"orchestrator,omitempty"`
}

// InboxMessage is one row in the inbox response.
type InboxMessage struct {
	ID        int64  `json:"id"`
	FromRole  string `json:"from_role"`
	SentAt    string `json:"sent_at"`
	Body      string `json:"body"`
	InReplyTo *int64 `json:"in_reply_to,omitempty"`
}

// InboxOutput is the response payload.
type InboxOutput struct {
	RoleName string         `json:"role_name"`
	Count    int            `json:"count"`
	Messages []InboxMessage `json:"messages"`
}

// Handle implements Handler.
func (h *InboxHandler) Handle(ctx context.Context, raw json.RawMessage) Response {
	if resp, gated := LinkGate(); gated {
		return resp
	}
	var in InboxInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return ErrorResponse("hera_inbox: invalid input JSON: " + err.Error())
	}
	if in.Cwd == "" {
		return ErrorResponse("hera_inbox: cwd is required")
	}

	_, role, _, err := h.resolver.CallerRole(ctx, in.Cwd, in.Orchestrator)
	if err != nil {
		return ErrorResponse("hera_inbox: " + err.Error())
	}

	msgs, err := h.db.Messages.UnreadForRole(ctx, role.ID)
	if err != nil {
		return ErrorResponse("hera_inbox: query: " + err.Error())
	}

	if len(msgs) > 0 {
		ids := make([]int64, len(msgs))
		for i, m := range msgs {
			ids[i] = m.ID
		}
		if _, err := h.db.Messages.MarkRead(ctx, role.ID, ids); err != nil {
			return ErrorResponse("hera_inbox: mark read: " + err.Error())
		}
		// Cancel argus delivery for each read message — best-effort.
		h.cancelDeliveries(ctx, role.ID, msgs)
	}

	out := InboxOutput{RoleName: role.Name, Count: len(msgs)}
	for _, m := range msgs {
		from := fmt.Sprintf("role:%d", m.FromRoleID)
		if r, err := h.db.Roles.GetByID(ctx, m.FromRoleID); err == nil {
			from = r.Name
		}
		out.Messages = append(out.Messages, InboxMessage{
			ID:        m.ID,
			FromRole:  from,
			SentAt:    m.SentAt.Format("2006-01-02T15:04:05.000Z07:00"),
			Body:      m.Body,
			InReplyTo: m.InReplyTo,
		})
	}
	if out.Messages == nil {
		out.Messages = []InboxMessage{}
	}
	return jsonText(out)
}

// cancelDeliveries calls argus CancelNotify for each message best-effort.
// Errors are logged at debug level and never fail the handler response.
func (h *InboxHandler) cancelDeliveries(ctx context.Context, roleID int64, msgs []*db.Message) {
	bnd, err := h.db.Bindings.GetLiveByRole(ctx, roleID)
	if err != nil {
		return // no live binding; nothing to cancel
	}
	for _, m := range msgs {
		if err := h.argus.CancelNotify(ctx, bnd.ArgusTaskID, strconv.FormatInt(m.ID, 10)); err != nil {
			slog.Debug("hera_inbox: cancel delivery", "msg_id", m.ID, "err", err)
		}
	}
}
