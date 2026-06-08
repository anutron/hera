package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/anutron/hera/internal/argus"
	"github.com/anutron/hera/internal/db"
)

const bounceResumeTldr = "argus bounced — please resume"
const bounceResumeBody = "argus bounced — please resume any unfinished work and report back to your coordinator"

// workerNotifier is the minimal delivery interface BounceRecoverer needs.
// *argus.Client satisfies it.
type workerNotifier interface {
	NotifyTask(ctx context.Context, taskID string, in argus.NotifyInput) (*argus.NotifyResponse, error)
}

// BounceRecoverer sends resume messages to all active managed workers when
// argus bounces. It is called from the OnRestart wrapper in daemon.Start
// after link recovery (re-registration) has succeeded.
//
// Only kind=worker roles are messaged. Freelancers are excluded — they
// are self-managed. Workers with no live binding are skipped.
type BounceRecoverer struct {
	DB         *db.DB
	Notifier   workerNotifier
	AutoSubmit bool
	DeadlineMs int64
	Log        *slog.Logger
}

// bouncePointer formats the PTY pointer text injected into the worker's
// session. Mirrors mcp.formatPointer so workers see a consistent shape.
func bouncePointer(senderRoleName string, msgID int64, tldr string) string {
	return fmt.Sprintf("[hera from %s] msg #%d — %s", senderRoleName, msgID, tldr)
}

// ResumeWorkers iterates every active orchestrator, finds all active worker
// roles with live bindings, and sends each a static resume message from the
// orchestrator's coordinator. Errors on individual workers are logged but do
// not abort the sweep.
func (r *BounceRecoverer) ResumeWorkers(ctx context.Context) {
	log := r.Log
	if log == nil {
		log = slog.Default()
	}

	orchestrators, err := r.DB.Orchestrators.List(ctx)
	if err != nil {
		log.Warn("bounce recovery: list orchestrators", "err", err)
		return
	}

	for _, orch := range orchestrators {
		r.resumeOrchestrator(ctx, orch, log)
	}
}

func (r *BounceRecoverer) resumeOrchestrator(ctx context.Context, orch *db.Orchestrator, log *slog.Logger) {
	roles, err := r.DB.Roles.ListByOrchestrator(ctx, orch.ID)
	if err != nil {
		log.Warn("bounce recovery: list roles", "orchestrator", orch.Name, "err", err)
		return
	}

	var coord *db.Role
	var workers []*db.Role
	for _, role := range roles {
		switch role.Kind {
		case db.KindCoordinator:
			coord = role
		case db.KindWorker:
			workers = append(workers, role)
			// KindFreelance excluded by design
		}
	}

	if coord == nil || len(workers) == 0 {
		return
	}

	for _, worker := range workers {
		bnd, err := r.DB.Bindings.GetLiveByRole(ctx, worker.ID)
		if errors.Is(err, db.ErrNotFound) {
			continue
		}
		if err != nil {
			log.Warn("bounce recovery: get live binding", "worker", worker.Name, "err", err)
			continue
		}

		msg, err := r.DB.Messages.Create(ctx, db.CreateMessageInput{
			FromRoleID: coord.ID,
			ToRoleID:   worker.ID,
			Body:       bounceResumeBody,
			Tldr:       bounceResumeTldr,
		})
		if err != nil {
			log.Warn("bounce recovery: create message", "worker", worker.Name, "err", err)
			continue
		}

		var mode db.DeliveryMode
		resp, notifyErr := r.Notifier.NotifyTask(ctx, bnd.ArgusTaskID, argus.NotifyInput{
			Text:       bouncePointer(coord.Name, msg.ID, bounceResumeTldr),
			Submit:     r.AutoSubmit,
			DeliveryID: strconv.FormatInt(msg.ID, 10),
			DeadlineMs: r.DeadlineMs,
		})
		if notifyErr != nil {
			log.Warn("bounce recovery: notify worker", "worker", worker.Name, "err", notifyErr)
			mode = db.DeliveryQueuedNoBinding
		} else if resp.State == "submitted" {
			mode = db.DeliveryIdleSubmit
		} else {
			mode = db.DeliveryBusyBuffer
		}

		if err := r.DB.Messages.SetDelivered(ctx, msg.ID, mode); err != nil {
			log.Warn("bounce recovery: set delivered", "worker", worker.Name, "err", err)
		}

		log.Info("bounce recovery: resumed worker",
			"orchestrator", orch.Name, "worker", worker.Name, "delivery", mode)
	}
}
