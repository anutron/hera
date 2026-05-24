package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/anutron/ludwig/internal/db"
)

// MessageInjector is the dependency ludwig_send uses to deliver bytes
// into the recipient's PTY. *inject.Injector implements this.
type MessageInjector interface {
	Inject(ctx context.Context, taskID, senderRoleName, body string) (db.DeliveryMode, error)
}

// SendHandler implements ludwig_send. It looks up the sender via cwd,
// resolves the recipient via explicit name or default routing, persists
// the message, and (if the recipient has a live binding) delivers via
// the injector with idle gating.
type SendHandler struct {
	resolver *Resolver
	db       *db.DB
	injector MessageInjector
}

// NewSendHandler constructs a SendHandler.
func NewSendHandler(r *Resolver, database *db.DB, injector MessageInjector) *SendHandler {
	return &SendHandler{resolver: r, db: database, injector: injector}
}

// SendInput is the tool's input schema.
type SendInput struct {
	Cwd       string `json:"cwd"`
	Body      string `json:"body"`
	To        string `json:"to,omitempty"`
	InReplyTo *int64 `json:"in_reply_to,omitempty"`
}

// SendOutput is the success payload.
type SendOutput struct {
	MessageID     int64  `json:"message_id"`
	RecipientRole string `json:"recipient_role"`
	DeliveryMode  string `json:"delivery_mode"`
}

// Handle implements Handler.
func (h *SendHandler) Handle(ctx context.Context, raw json.RawMessage) Response {
	var in SendInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return ErrorResponse("ludwig_send: invalid input JSON: " + err.Error())
	}
	if in.Cwd == "" {
		return ErrorResponse("ludwig_send: cwd is required")
	}
	if in.Body == "" {
		return ErrorResponse("ludwig_send: body is required")
	}

	// Resolve sender.
	_, senderRole, _, err := h.resolver.CallerRole(ctx, in.Cwd)
	if err != nil {
		return ErrorResponse("ludwig_send: " + err.Error())
	}

	// Resolve recipient.
	recipient, recipientName, toKind, err := h.resolveRecipient(ctx, senderRole, in.To)
	if err != nil {
		return ErrorResponse("ludwig_send: " + err.Error())
	}

	// Build the message row. For user pseudo-recipient, to_role_id is nil.
	createIn := db.CreateMessageInput{
		FromRoleID: senderRole.ID,
		Body:       in.Body,
		ToKind:     toKind,
		InReplyTo:  in.InReplyTo,
	}
	if toKind == db.ToKindRole {
		id := recipient.ID
		createIn.ToRoleID = &id
	}
	msg, err := h.db.Messages.Create(ctx, createIn)
	if err != nil {
		return ErrorResponse("ludwig_send: persist message: " + err.Error())
	}

	// Pick a delivery mode.
	var mode db.DeliveryMode
	switch toKind {
	case db.ToKindUser:
		mode = db.DeliveryUserInbox
	case db.ToKindRole:
		// Look up recipient's live binding.
		bnd, lookupErr := h.db.Bindings.GetLiveByRole(ctx, recipient.ID)
		if errors.Is(lookupErr, db.ErrNotFound) {
			mode = db.DeliveryQueuedNoBinding
		} else if lookupErr != nil {
			return ErrorResponse("ludwig_send: lookup recipient binding: " + lookupErr.Error())
		} else {
			delivered, injectErr := h.injector.Inject(ctx, bnd.ArgusTaskID, senderRole.Name, in.Body)
			if injectErr != nil {
				return ErrorResponse("ludwig_send: inject: " + injectErr.Error())
			}
			mode = delivered
		}
	}

	if err := h.db.Messages.SetDelivered(ctx, msg.ID, mode); err != nil {
		return ErrorResponse("ludwig_send: persist delivery mode: " + err.Error())
	}

	return jsonText(SendOutput{
		MessageID:     msg.ID,
		RecipientRole: recipientName,
		DeliveryMode:  string(mode),
	})
}

// resolveRecipient implements the default-routing rule plus explicit `to`
// lookup. Returns the resolved *db.Role (nil for user-kind), the role
// name (or "user" sentinel), and the to_kind to persist.
func (h *SendHandler) resolveRecipient(ctx context.Context, sender *db.Role, to string) (*db.Role, string, db.ToKind, error) {
	if to == "user" {
		return nil, "user", db.ToKindUser, nil
	}
	if to != "" {
		role, err := h.db.Roles.GetByOrchestratorAndName(ctx, sender.OrchestratorID, to)
		if errors.Is(err, db.ErrNotFound) {
			return nil, "", "", fmt.Errorf("recipient role %q does not exist in orchestrator", to)
		}
		if err != nil {
			return nil, "", "", err
		}
		return role, role.Name, db.ToKindRole, nil
	}

	// Default routing.
	switch sender.Kind {
	case db.KindCoordinator:
		return nil, "user", db.ToKindUser, nil
	case db.KindWorker, db.KindFreelance:
		coord, err := h.findCoordinator(ctx, sender.OrchestratorID)
		if err != nil {
			return nil, "", "", err
		}
		return coord, coord.Name, db.ToKindRole, nil
	}
	return nil, "", "", fmt.Errorf("unknown sender kind %q", sender.Kind)
}

// findCoordinator returns the coordinator role under an orchestrator.
// Returns an error if there is no coordinator.
func (h *SendHandler) findCoordinator(ctx context.Context, orchestratorID int64) (*db.Role, error) {
	roles, err := h.db.Roles.ListByOrchestrator(ctx, orchestratorID)
	if err != nil {
		return nil, err
	}
	for _, r := range roles {
		if r.Kind == db.KindCoordinator {
			return r, nil
		}
	}
	return nil, errors.New("orchestrator has no coordinator role")
}
