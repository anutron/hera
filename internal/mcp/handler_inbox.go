package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/anutron/ludwig/internal/db"
)

// InboxHandler implements ludwig_inbox. Returns every unread message
// addressed to the caller's role, oldest-first.
type InboxHandler struct {
	resolver *Resolver
	db       *db.DB
}

// NewInboxHandler constructs an InboxHandler.
func NewInboxHandler(r *Resolver, database *db.DB) *InboxHandler {
	return &InboxHandler{resolver: r, db: database}
}

// InboxInput is the tool's input schema.
type InboxInput struct {
	Cwd string `json:"cwd"`
}

// InboxMessage is one row in the inbox response.
type InboxMessage struct {
	ID         int64  `json:"id"`
	FromRole   string `json:"from_role"`
	SentAt     string `json:"sent_at"`
	Body       string `json:"body"`
	InReplyTo  *int64 `json:"in_reply_to,omitempty"`
}

// InboxOutput is the response payload.
type InboxOutput struct {
	RoleName string         `json:"role_name"`
	Count    int            `json:"count"`
	Messages []InboxMessage `json:"messages"`
}

// Handle implements Handler.
func (h *InboxHandler) Handle(ctx context.Context, raw json.RawMessage) Response {
	var in InboxInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return ErrorResponse("ludwig_inbox: invalid input JSON: " + err.Error())
	}
	if in.Cwd == "" {
		return ErrorResponse("ludwig_inbox: cwd is required")
	}

	_, role, _, err := h.resolver.CallerRole(ctx, in.Cwd)
	if err != nil {
		if errors.Is(err, ErrNoBinding) {
			return ErrorResponse("ludwig_inbox: " + err.Error())
		}
		return ErrorResponse("ludwig_inbox: " + err.Error())
	}

	msgs, err := h.db.Messages.UnreadForRole(ctx, role.ID)
	if err != nil {
		return ErrorResponse("ludwig_inbox: query: " + err.Error())
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

	// silence "unused" on db.ErrNotFound when the role has zero unread messages.
	_ = db.ErrNotFound
	return jsonText(out)
}
