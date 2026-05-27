package view

import (
	"context"
	"errors"
	"fmt"

	"github.com/anutron/hera/internal/argus"
	"github.com/anutron/hera/internal/db"
	"github.com/anutron/hera/internal/view/ops"
)

// dbAdapter wires *db.DB's DAOs into the ops.DB interface. Each method
// translates db's typed rows + sentinel errors into the ops layer's
// neutral shapes (so the ops package does not import internal/db).
type dbAdapter struct {
	d *db.DB
}

func newDBAdapter(d *db.DB) *dbAdapter { return &dbAdapter{d: d} }

func (a *dbAdapter) GetOrchestratorByID(ctx context.Context, id int64) (*ops.Orchestrator, error) {
	o, err := a.d.Orchestrators.GetByID(ctx, id)
	if err != nil {
		return nil, translateDBErr(err)
	}
	return adaptOrchestrator(o), nil
}

func (a *dbAdapter) GetOrchestratorByName(ctx context.Context, name string) (*ops.Orchestrator, error) {
	o, err := a.d.Orchestrators.GetByName(ctx, name)
	if err != nil {
		return nil, translateDBErr(err)
	}
	return adaptOrchestrator(o), nil
}

func (a *dbAdapter) ListOrchestrators(ctx context.Context) ([]*ops.Orchestrator, error) {
	rows, err := a.d.Orchestrators.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*ops.Orchestrator, 0, len(rows))
	for _, o := range rows {
		out = append(out, adaptOrchestrator(o))
	}
	return out, nil
}

func (a *dbAdapter) ArchiveOrchestrator(ctx context.Context, id int64) error {
	return translateDBErr(a.d.Orchestrators.Archive(ctx, id))
}

func (a *dbAdapter) UnarchiveOrchestrator(ctx context.Context, id int64) error {
	return translateDBErr(a.d.Orchestrators.Unarchive(ctx, id))
}

func (a *dbAdapter) RenameOrchestrator(ctx context.Context, id int64, newName string) error {
	return translateDBErr(a.d.Orchestrators.Rename(ctx, id, newName))
}

func (a *dbAdapter) GetRoleByID(ctx context.Context, id int64) (*ops.Role, error) {
	r, err := a.d.Roles.GetByID(ctx, id)
	if err != nil {
		return nil, translateDBErr(err)
	}
	return adaptRole(r), nil
}

func (a *dbAdapter) ListRolesByOrchestrator(ctx context.Context, orchID int64) ([]*ops.Role, error) {
	rows, err := a.d.Roles.ListByOrchestrator(ctx, orchID)
	if err != nil {
		return nil, err
	}
	out := make([]*ops.Role, 0, len(rows))
	for _, r := range rows {
		out = append(out, adaptRole(r))
	}
	return out, nil
}

func (a *dbAdapter) ArchiveRole(ctx context.Context, id int64) error {
	return translateDBErr(a.d.Roles.Archive(ctx, id))
}

func (a *dbAdapter) UnarchiveRole(ctx context.Context, id int64) error {
	return translateDBErr(a.d.Roles.Unarchive(ctx, id))
}

func (a *dbAdapter) RenameRole(ctx context.Context, id int64, newName string) error {
	return translateDBErr(a.d.Roles.Rename(ctx, id, newName))
}

func (a *dbAdapter) GetLiveBindingByRole(ctx context.Context, roleID int64) (*ops.Binding, error) {
	b, err := a.d.Bindings.GetLiveByRole(ctx, roleID)
	if err != nil {
		return nil, translateDBErr(err)
	}
	return adaptBinding(b), nil
}

func (a *dbAdapter) EndBinding(ctx context.Context, bindingID int64, reason string) error {
	return translateDBErr(a.d.Bindings.End(ctx, bindingID, reason))
}

func adaptOrchestrator(o *db.Orchestrator) *ops.Orchestrator {
	if o == nil {
		return nil
	}
	return &ops.Orchestrator{ID: o.ID, Name: o.Name, Archived: o.ArchivedAt != nil}
}

func adaptRole(r *db.Role) *ops.Role {
	if r == nil {
		return nil
	}
	return &ops.Role{
		ID:             r.ID,
		OrchestratorID: r.OrchestratorID,
		Name:           r.Name,
		Kind:           ops.RoleKind(r.Kind),
		ArgusProject:   r.ArgusProject,
		Mission:        r.Mission,
		Constraints:    r.Constraints,
		Archived:       r.ArchivedAt != nil,
	}
}

func adaptBinding(b *db.Binding) *ops.Binding {
	if b == nil {
		return nil
	}
	return &ops.Binding{
		ID:           b.ID,
		RoleID:       b.RoleID,
		ArgusTaskID:  b.ArgusTaskID,
		WorktreePath: b.WorktreePath,
	}
}

func translateDBErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, db.ErrNotFound) {
		return ops.ErrNotFound
	}
	return err
}

// argusAdapter wraps *argus.Client to satisfy the ops.ArgusClient
// interface. Translates ops.CreateTaskRequest into the argus REST
// payload shape.
type argusAdapter struct {
	c *argus.Client
}

func newArgusAdapter(c *argus.Client) *argusAdapter { return &argusAdapter{c: c} }

func (a *argusAdapter) CreateTask(ctx context.Context, req ops.CreateTaskRequest) (*ops.CreatedTask, error) {
	if a.c == nil {
		return nil, fmt.Errorf("argusAdapter: nil client")
	}
	in := argus.CreateTaskInput{
		Project: req.Project,
		Name:    req.Name,
		Prompt:  req.Prompt,
	}
	out, err := a.c.CreateTask(ctx, in, req.Meta)
	if err != nil {
		return nil, err
	}
	return &ops.CreatedTask{ID: out.ID, Name: out.Name}, nil
}

func (a *argusAdapter) ArchiveTask(ctx context.Context, taskID string) error {
	if a.c == nil {
		return fmt.Errorf("argusAdapter: nil client")
	}
	return a.c.ArchiveTask(ctx, taskID)
}

func (a *argusAdapter) UnarchiveTask(ctx context.Context, taskID string) error {
	if a.c == nil {
		return fmt.Errorf("argusAdapter: nil client")
	}
	return a.c.UnarchiveTask(ctx, taskID)
}
