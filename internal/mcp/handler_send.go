package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/anutron/hera/internal/argus"
	"github.com/anutron/hera/internal/db"
)

// ArgusNotifier is the subset of argus.Client used by the send handler.
// *argus.Client satisfies this interface.
type ArgusNotifier interface {
	NotifyTask(ctx context.Context, taskID string, in argus.NotifyInput) (*argus.NotifyResponse, error)
}

// SendHandler implements hera_send. It persists the message and delegates PTY
// delivery to argus's notify endpoint, which owns idle detection, submit CR,
// retry, and deduplication.
type SendHandler struct {
	resolver   *Resolver
	db         *db.DB
	argus      ArgusNotifier
	autoInject atomic.Bool
	deadlineMs int64
}

// NewSendHandler constructs a SendHandler. autoInjectEnabled controls the
// submit: field in notify calls; deadlineMs is the delivery deadline in
// milliseconds.
func NewSendHandler(r *Resolver, database *db.DB, client ArgusNotifier, autoInjectEnabled bool, deadlineMs int64) *SendHandler {
	h := &SendHandler{resolver: r, db: database, argus: client, deadlineMs: deadlineMs}
	h.autoInject.Store(autoInjectEnabled)
	return h
}

// SetAutoInjectEnabled satisfies the autoInjectSwitch interface used by the
// settings save handler for hot-reload of the auto_inject_enabled setting.
func (h *SendHandler) SetAutoInjectEnabled(b bool) {
	h.autoInject.Store(b)
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

	// Deliver via argus notify.
	var mode db.DeliveryMode
	bnd, lookupErr := h.db.Bindings.GetLiveByRole(ctx, recipient.ID)
	switch {
	case errors.Is(lookupErr, db.ErrNotFound):
		mode = db.DeliveryQueuedNoBinding
	case lookupErr != nil:
		return ErrorResponse("hera_send: lookup recipient binding: " + lookupErr.Error())
	default:
		pointer := formatPointer(senderRole.Name, msg.ID, in.Tldr)
		resp, notifyErr := h.argus.NotifyTask(ctx, bnd.ArgusTaskID, argus.NotifyInput{
			Text:       pointer,
			Submit:     h.autoInject.Load(),
			DeliveryID: strconv.FormatInt(msg.ID, 10),
			DeadlineMs: h.deadlineMs,
		})
		if notifyErr != nil {
			return ErrorResponse("hera_send: notify: " + notifyErr.Error())
		}
		if resp.State == "submitted" {
			mode = db.DeliveryIdleSubmit
		} else {
			mode = db.DeliveryBusyBuffer
		}
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

// formatPointer returns the PTY representation of a message pointer.
// The recipient sees this subject-line and calls hera_inbox to read the
// full body (which marks the message as read).
func formatPointer(senderRoleName string, msgID int64, tldr string) string {
	return fmt.Sprintf("[hera from %s] msg #%d — %s", senderRoleName, msgID, tldr)
}

// resolveRecipient applies routing rules: explicit `to` looks up by name;
// empty `to` defaults to coordinator for worker/freelance; coordinator MUST supply `to`.
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
