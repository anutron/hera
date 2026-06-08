package events

import (
	"context"
	"log/slog"

	"github.com/anutron/hera/internal/argus"
	"github.com/anutron/hera/internal/db"
)

// WorkerCompleteHandler archives hera-managed worker roles when their
// argus task transitions to "complete". When a coord spawns a worker via
// hera_spawn_worker (or the `w` rail key), the resulting task gains a
// worker role + binding. Completing the task is the worker's done signal;
// hera auto-archives the role so the rail hides it from the active
// section without the operator needing to press `a` manually.
//
// Only KindWorker roles are archived — coordinator roles that complete
// are left for the operator to manage.
type WorkerCompleteHandler struct {
	db  *db.DB
	log *slog.Logger
}

// NewWorkerCompleteHandler constructs a WorkerCompleteHandler.
func NewWorkerCompleteHandler(database *db.DB, log *slog.Logger) *WorkerCompleteHandler {
	if log == nil {
		log = slog.Default()
	}
	return &WorkerCompleteHandler{db: database, log: log}
}

// HandleEvent implements events.Handler. task.status_changed with to==complete
// triggers the auto-archive; all other events are ignored.
func (h *WorkerCompleteHandler) HandleEvent(ctx context.Context, ev argus.Event) {
	if ev.Type != TypeTaskStatusChanged {
		return
	}
	payload, err := ParseTaskStatusChanged(ev)
	if err != nil || payload.To != "complete" {
		return
	}
	taskID := ev.TaskID
	if taskID == "" {
		return
	}
	h.archiveCompletedWorkers(ctx, taskID)
}

func (h *WorkerCompleteHandler) archiveCompletedWorkers(ctx context.Context, taskID string) {
	bindings, err := h.db.Bindings.ListLiveByTaskID(ctx, taskID)
	if err != nil {
		h.log.Warn("worker_complete: list bindings", "task", taskID, "err", err)
		return
	}
	for _, bnd := range bindings {
		role, err := h.db.Roles.GetByID(ctx, bnd.RoleID)
		if err != nil {
			h.log.Warn("worker_complete: get role", "role_id", bnd.RoleID, "err", err)
			continue
		}
		if role.Kind != db.KindWorker {
			continue
		}
		if err := h.db.Roles.Archive(ctx, role.ID); err != nil {
			h.log.Warn("worker_complete: archive role",
				"role_id", role.ID, "task", taskID, "err", err)
			continue
		}
		h.log.Info("worker_complete: auto-archived completed worker",
			"role_id", role.ID, "role_name", role.Name, "task", taskID)
	}
}
