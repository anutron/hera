package events

import (
	"context"
	"log/slog"

	"github.com/anutron/hera/internal/argus"
	"github.com/anutron/hera/internal/db"
)

// AdoptHandler implements the stricter auto-adopt rule from the spec:
// when a link.created event names a child whose parent is bound to a
// hera coordinator role AND the child has meta:hera.role=worker,
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

// HandleEvent implements events.Handler. link.created triggers adoption;
// task.deleted ends the matching binding. task.archived is a no-op —
// archive is reversible so the binding must remain resumable.
func (a *AdoptHandler) HandleEvent(ctx context.Context, ev argus.Event) {
	switch ev.Type {
	case TypeLinkCreated:
		a.handleLinkCreated(ctx, ev)
	case TypeTaskArchived:
		a.handleTaskArchived(ctx, ev)
	case TypeTaskDeleted:
		a.handleTaskDeleted(ctx, ev)
	}
}

func (a *AdoptHandler) handleLinkCreated(ctx context.Context, ev argus.Event) {
	link, err := ParseLinkCreated(ev)
	if err != nil {
		a.log.Warn("link.created: bad payload", "id", ev.ID, "err", err)
		return
	}

	// (1) Is the parent bound to a hera coordinator role? With
	// multi-binding the parent task may hold multiple live bindings;
	// adoption only attributes a child to a coordinator role. We
	// require exactly one coordinator binding on the parent so the
	// child's orchestrator is unambiguous.
	parentBindings, err := a.db.Bindings.ListLiveByTaskID(ctx, link.Parent)
	if err != nil {
		a.log.Warn("link.created: lookup parent bindings", "parent", link.Parent, "err", err)
		return
	}
	if len(parentBindings) == 0 {
		// Parent isn't bound to anything hera owns – not our task to adopt.
		return
	}
	var coordBindings []*db.Binding
	var coordOrchestrators []string
	for _, b := range parentBindings {
		role, err := a.db.Roles.GetByID(ctx, b.RoleID)
		if err != nil {
			a.log.Warn("link.created: lookup parent role",
				"role_id", b.RoleID, "err", err)
			return
		}
		if role.Kind != db.KindCoordinator {
			continue
		}
		// Orchestrator archive does not auto-end its bindings (the
		// hera-view archive flow keeps history intact). Filter those
		// out here so an archived parent orchestrator is treated as
		// "no longer a hera coordinator" and adoption is skipped.
		orch, err := a.db.Orchestrators.GetByID(ctx, role.OrchestratorID)
		if err != nil {
			a.log.Warn("link.created: lookup parent orchestrator",
				"orch_id", role.OrchestratorID, "err", err)
			return
		}
		if orch.ArchivedAt != nil {
			continue
		}
		coordBindings = append(coordBindings, b)
		coordOrchestrators = append(coordOrchestrators, orch.Name)
	}
	if len(coordBindings) == 0 {
		// Parent has no coordinator binding under any orchestrator —
		// adoption only follows coordinator parents per design D4.
		return
	}
	if len(coordBindings) > 1 {
		a.log.Warn("link.created: skipped adoption (parent has multiple coordinator bindings)",
			"parent", link.Parent, "child", link.Child,
			"orchestrators", coordOrchestrators,
			"hint", "operator must attach the child explicitly via hera_join to disambiguate")
		return
	}
	parentBinding := coordBindings[0]
	parentRole, err := a.db.Roles.GetByID(ctx, parentBinding.RoleID)
	if err != nil {
		a.log.Warn("link.created: re-lookup parent role", "role_id", parentBinding.RoleID, "err", err)
		return
	}

	// (2) Does the child task have meta:hera.role=worker?
	meta, err := a.client.GetTaskMeta(ctx, link.Child, MetaNamespace)
	if err != nil {
		a.log.Warn("link.created: fetch child meta", "child", link.Child, "err", err)
		return
	}
	roleVal, missionVal, constraintsVal := pickAdoptMeta(meta)

	if roleVal == "" {
		a.log.Info("link.created: skipped adoption (no meta:hera.role)",
			"child", link.Child, "parent", link.Parent, "missing_key", MetaKeyRole)
		return
	}
	if roleVal != string(db.KindWorker) {
		a.log.Info("link.created: skipped adoption (meta:hera.role not 'worker')",
			"child", link.Child, "parent", link.Parent, "missing_key", MetaKeyRole, "value", roleVal)
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
		RoleID:         role.ID,
		OrchestratorID: parentRole.OrchestratorID,
		ArgusTaskID:    child.ID,
		WorktreePath:   child.WorktreePath,
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

// handleTaskArchived is intentionally a no-op with respect to binding
// lifecycle. Archive is a reversible visibility change — the worktree
// still exists, the agent may still be live, and the role must remain
// resumable. Only task.deleted ends a binding.
func (a *AdoptHandler) handleTaskArchived(_ context.Context, ev argus.Event) {
	if ev.TaskID == "" {
		return
	}
	a.log.Info("task.archived: binding preserved (archive is non-destructive)", "task", ev.TaskID)
}

// handleTaskDeleted ends every live binding for the deleted task.
func (a *AdoptHandler) handleTaskDeleted(ctx context.Context, ev argus.Event) {
	if ev.TaskID == "" {
		return
	}
	bindings, err := a.db.Bindings.ListLiveByTaskID(ctx, ev.TaskID)
	if err != nil {
		a.log.Warn("task.deleted: list bindings", "task", ev.TaskID, "err", err)
		return
	}
	for _, bnd := range bindings {
		if err := a.db.Bindings.End(ctx, bnd.ID, "task_deleted"); err != nil {
			a.log.Warn("task.deleted: end binding", "task", ev.TaskID, "binding_id", bnd.ID, "err", err)
			continue
		}
		a.log.Info("binding ended on task.deleted", "task", ev.TaskID, "binding_id", bnd.ID)
	}
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
