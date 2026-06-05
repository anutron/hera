package events

import (
	"context"
	"errors"
	"log/slog"

	"github.com/anutron/hera/internal/argus"
	"github.com/anutron/hera/internal/db"
)

// ResyncHandler reconciles hera's bindings against argus's current task
// list when a resync event arrives (cursor older than argus's retained
// event ring).
type ResyncHandler struct {
	client *argus.Client
	db     *db.DB
	log    *slog.Logger
}

// NewResyncHandler constructs a ResyncHandler.
func NewResyncHandler(client *argus.Client, database *db.DB, log *slog.Logger) *ResyncHandler {
	if log == nil {
		log = slog.Default()
	}
	return &ResyncHandler{client: client, db: database, log: log}
}

// HandleEvent implements events.Handler. Only the resync type triggers
// work.
func (r *ResyncHandler) HandleEvent(ctx context.Context, ev argus.Event) {
	if ev.Type != TypeResync {
		return
	}
	if err := r.Reconcile(ctx); err != nil {
		r.log.Warn("resync: reconcile failed", "err", err)
	}
}

// Reconcile fetches the current argus task list (including archived tasks)
// and ends any hera binding whose argus task is no longer present at all.
// Archived tasks are included in the "live" set so that merely-archived
// tasks do not have their bindings ended — only tasks that have been
// deleted/pruned trigger binding termination. Exported so the periodic
// reconciler can call the same logic on its own timer.
func (r *ResyncHandler) Reconcile(ctx context.Context) error {
	tasks, err := r.client.ListTasksAll(ctx)
	if err != nil {
		return err
	}
	live := make(map[string]struct{}, len(tasks))
	for _, t := range tasks {
		live[t.ID] = struct{}{}
	}

	// Walk every orchestrator's roles, look at each live binding, end the
	// binding if the task is gone.
	orchs, err := r.db.Orchestrators.List(ctx)
	if err != nil {
		return err
	}
	var ended int
	for _, o := range orchs {
		roles, err := r.db.Roles.ListByOrchestrator(ctx, o.ID)
		if err != nil {
			return err
		}
		for _, role := range roles {
			bnd, err := r.db.Bindings.GetLiveByRole(ctx, role.ID)
			if errors.Is(err, db.ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			if _, ok := live[bnd.ArgusTaskID]; !ok {
				if err := r.db.Bindings.End(ctx, bnd.ID, "resync_missing"); err != nil {
					return err
				}
				ended++
			}
		}
	}
	r.log.Info("resync reconciled", "ended_bindings", ended, "live_tasks", len(live))
	return nil
}
