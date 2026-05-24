package events

import (
	"context"
	"errors"
	"log/slog"

	"github.com/anutron/ludwig/internal/argus"
	"github.com/anutron/ludwig/internal/db"
)

// AdoptHandler implements the stricter auto-adopt rule from the spec:
// when a link.created event names a child whose parent is bound to a
// ludwig coordinator role AND the child has meta:ludwig.role=worker,
// adopt the child as a worker role under the parent's orchestrator.
//
// Tasks meeting only one of those conditions are NOT adopted. Skipped
// adoptions are logged at INFO.
type AdoptHandler struct {
	client *argus.Client
	db     *db.DB
	log    *slog.Logger
}

// NewAdoptHandler constructs an AdoptHandler.
func NewAdoptHandler(client *argus.Client, database *db.DB, log *slog.Logger) *AdoptHandler {
	if log == nil {
		log = slog.Default()
	}
	return &AdoptHandler{client: client, db: database, log: log}
}

// HandleEvent implements events.Handler. Only link.created is acted on;
// task.archived also ends the matching binding.
func (a *AdoptHandler) HandleEvent(ctx context.Context, ev argus.Event) {
	switch ev.Type {
	case TypeLinkCreated:
		a.handleLinkCreated(ctx, ev)
	case TypeTaskArchived:
		a.handleTaskArchived(ctx, ev)
	}
}

func (a *AdoptHandler) handleLinkCreated(ctx context.Context, ev argus.Event) {
	link, err := ParseLinkCreated(ev)
	if err != nil {
		a.log.Warn("link.created: bad payload", "id", ev.ID, "err", err)
		return
	}

	// (1) Is the parent bound to a ludwig coordinator role?
	parentBinding, err := a.db.Bindings.GetLiveByTaskID(ctx, link.Parent)
	if errors.Is(err, db.ErrNotFound) {
		// Parent isn't bound to anything ludwig owns – not our task to adopt.
		return
	}
	if err != nil {
		a.log.Warn("link.created: lookup parent binding", "parent", link.Parent, "err", err)
		return
	}

	parentRole, err := a.db.Roles.GetByID(ctx, parentBinding.RoleID)
	if err != nil {
		a.log.Warn("link.created: lookup parent role", "role_id", parentBinding.RoleID, "err", err)
		return
	}
	if parentRole.Kind != db.KindCoordinator {
		// Adoption only follows coordinator parents per design D4.
		return
	}

	// (2) Does the child task have meta:ludwig.role=worker?
	meta, err := a.client.GetTaskMeta(ctx, link.Child, MetaNamespace)
	if err != nil {
		a.log.Warn("link.created: fetch child meta", "child", link.Child, "err", err)
		return
	}
	roleVal, missionVal, constraintsVal := pickAdoptMeta(meta)

	if roleVal == "" {
		a.log.Info("link.created: skipped adoption (no meta:ludwig.role)",
			"child", link.Child, "parent", link.Parent)
		return
	}
	if roleVal != string(db.KindWorker) {
		a.log.Info("link.created: skipped adoption (meta:ludwig.role not 'worker')",
			"child", link.Child, "value", roleVal)
		return
	}

	// (3) Fetch the child task to learn its argus project + worktree.
	child, err := a.client.GetTask(ctx, link.Child)
	if err != nil {
		a.log.Warn("link.created: fetch child task", "child", link.Child, "err", err)
		return
	}

	// (4) Create role + binding atomically.
	role, err := a.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: parentRole.OrchestratorID,
		Name:           child.Name,
		Kind:           db.KindWorker,
		ArgusProject:   child.Project,
		Mission:        missionVal,
		Constraints:    constraintsVal,
	})
	if err != nil {
		a.log.Warn("adoption: create role", "child", link.Child, "err", err)
		return
	}
	if _, err := a.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID:       role.ID,
		ArgusTaskID:  child.ID,
		WorktreePath: child.WorktreePath,
	}); err != nil {
		a.log.Warn("adoption: create binding", "child", link.Child, "err", err)
		return
	}

	a.log.Info("adopted worker role",
		"role", role.Name, "child", child.ID,
		"orchestrator_id", parentRole.OrchestratorID,
	)

	// Mirror role meta back to argus task_meta so layout/UI predicates and
	// other plugins can read it. Best-effort; do not fail the adoption if
	// the write fails (e.g., transient network).
	if err := a.client.PutTaskMeta(ctx, child.ID, MetaKeyRole, string(db.KindWorker)); err != nil {
		a.log.Warn("adoption: mirror role meta", "child", child.ID, "err", err)
	}
}

// handleTaskArchived ends the binding for the archived task (if any).
func (a *AdoptHandler) handleTaskArchived(ctx context.Context, ev argus.Event) {
	if ev.TaskID == "" {
		return
	}
	bnd, err := a.db.Bindings.GetLiveByTaskID(ctx, ev.TaskID)
	if errors.Is(err, db.ErrNotFound) {
		return
	}
	if err != nil {
		a.log.Warn("task.archived: lookup binding", "task", ev.TaskID, "err", err)
		return
	}
	if err := a.db.Bindings.End(ctx, bnd.ID, "argus_archived"); err != nil {
		a.log.Warn("task.archived: end binding", "task", ev.TaskID, "err", err)
		return
	}
	a.log.Info("binding ended on task.archived", "task", ev.TaskID, "binding_id", bnd.ID)
}

// pickAdoptMeta extracts the role/mission/constraints values from a
// GetTaskMeta response.
func pickAdoptMeta(entries []argus.MetaEntry) (role, mission, constraints string) {
	for _, e := range entries {
		if e.Namespace != MetaNamespace {
			continue
		}
		switch e.Key {
		case MetaKeyRole:
			role = e.Value
		case MetaKeyMission:
			mission = e.Value
		case MetaKeyConstraints:
			constraints = e.Value
		}
	}
	return
}
