package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/anutron/hera/internal/db"
)

// MessageInjector is the dependency hera_send uses to deliver bytes
// into the recipient's PTY. *inject.Injector implements this.
type MessageInjector interface {
	Inject(ctx context.Context, taskID, senderRoleName string, msgID int64, tldr string) (db.DeliveryMode, error)
}

// SendHandler implements hera_send. It looks up the sender via cwd,
// resolves the recipient via explicit name or default routing (worker
// or freelance senders default to the orchestrator's coordinator;
// coordinator senders MUST supply an explicit `to`), persists the
// message, and (if the recipient has a live binding) delivers via the
// injector with idle gating.
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
	Cwd          string `json:"cwd"`
	Body         string `json:"body"`
	Tldr         string `json:"tldr"`
	To           string `json:"to,omitempty"`
	InReplyTo    *int64 `json:"in_reply_to,omitempty"`
	Orchestrator string `json:"orchestrator,omitempty"`
}

// SendOutput is the success payload.
type SendOutput struct {
	MessageID     int64  `json:"message_id"`
	RecipientRole string `json:"recipient_role"`
	DeliveryMode  string `json:"delivery_mode"`
}

// Handle implements Handler.
func (h *SendHandler) Handle(ctx context.Context, raw json.RawMessage) Response {
	if resp, gated := LinkGate(); gated {
		return resp
	}
	var in SendInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return ErrorResponse("hera_send: invalid input JSON: " + err.Error())
	}
	if in.Cwd == "" {
		return ErrorResponse("hera_send: cwd is required")
	}
	if in.Body == "" {
		return ErrorResponse("hera_send: body is required")
	}
	if strings.TrimSpace(in.Tldr) == "" {
		return ErrorResponse("hera_send: tldr is required — provide a one-line summary (≤120 chars)")
	}
	if strings.ContainsRune(in.Tldr, '\n') || len(in.Tldr) > 120 {
		return ErrorResponse("hera_send: tldr must be a single line of ≤120 characters")
	}

	_, senderRole, _, err := h.resolver.CallerRole(ctx, in.Cwd, in.Orchestrator)
	if err != nil {
		return ErrorResponse("hera_send: " + err.Error())
	}

	recipient, err := h.resolveRecipient(ctx, senderRole, in.To)
	if err != nil {
		return ErrorResponse("hera_send: " + err.Error())
	}

	msg, err := h.db.Messages.Create(ctx, db.CreateMessageInput{
		FromRoleID: senderRole.ID,
		ToRoleID:   recipient.ID,
		Body:       in.Body,
		Tldr:       in.Tldr,
		InReplyTo:  in.InReplyTo,
	})
	if err != nil {
		return ErrorResponse("hera_send: persist message: " + err.Error())
	}

	// Deliver.
	var mode db.DeliveryMode
	bnd, lookupErr := h.db.Bindings.GetLiveByRole(ctx, recipient.ID)
	switch {
	case errors.Is(lookupErr, db.ErrNotFound):
		mode = db.DeliveryQueuedNoBinding
	case lookupErr != nil:
		return ErrorResponse("hera_send: lookup recipient binding: " + lookupErr.Error())
	default:
		delivered, injectErr := h.injector.Inject(ctx, bnd.ArgusTaskID, senderRole.Name, msg.ID, in.Tldr)
		if injectErr != nil {
			return ErrorResponse("hera_send: inject: " + injectErr.Error())
		}
		mode = delivered
	}

	if err := h.db.Messages.SetDelivered(ctx, msg.ID, mode); err != nil {
		return ErrorResponse("hera_send: persist delivery mode: " + err.Error())
	}

	return jsonText(SendOutput{
		MessageID:     msg.ID,
		RecipientRole: recipient.Name,
		DeliveryMode:  string(mode),
	})
}

// resolveRecipient applies the routing rules: explicit `to` looks up the
// role by (orchestrator, name); empty `to` defaults to the coordinator
// for worker/freelance senders; coordinator senders MUST provide `to`.
func (h *SendHandler) resolveRecipient(ctx context.Context, sender *db.Role, to string) (*db.Role, error) {
	if to != "" {
		role, err := h.db.Roles.GetByOrchestratorAndName(ctx, sender.OrchestratorID, to)
		if errors.Is(err, db.ErrNotFound) {
			return nil, fmt.Errorf("recipient role %q does not exist in orchestrator", to)
		}
		if err != nil {
			return nil, err
		}
		return role, nil
	}

	switch sender.Kind {
	case db.KindCoordinator:
		return nil, errors.New("coordinator senders must supply an explicit `to` (talk to the human in your own pane, or address a specific worker by role name)")
	case db.KindWorker, db.KindFreelance:
		coord, err := h.findCoordinator(ctx, sender.OrchestratorID)
		if err != nil {
			return nil, err
		}
		return coord, nil
	}
	return nil, fmt.Errorf("unknown sender kind %q", sender.Kind)
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
