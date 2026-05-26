package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// RolesDAO is the typed accessor for the roles table.
type RolesDAO struct {
	db     *sql.DB
	events *Broadcaster
}

// CreateInput captures the fields a Create call must supply.
type CreateRoleInput struct {
	OrchestratorID int64
	Name           string
	Kind           RoleKind
	ArgusProject   string
	Mission        string
	Constraints    string
}

// Create inserts a new active role under the given orchestrator. Returns
// the existing active row if (orchestrator_id, name) already exists with
// the same kind; returns an error if it exists with a different kind. An
// archived role with the same (orchestrator_id, name) does NOT block
// creation — a fresh active row is inserted in that case.
//
// Roles are write-once on mission/constraints/argus_project: if an active
// role with the same (orchestrator_id, name) already exists, the supplied
// Mission, Constraints, and ArgusProject inputs are SILENTLY IGNORED and
// the existing row's values are preserved. A role is a durable identity
// established at first creation; subsequent agents claiming the same role
// inherit the original mission.
func (r *RolesDAO) Create(ctx context.Context, in CreateRoleInput) (*Role, error) {
	existing, err := r.findActiveByOrchestratorAndName(ctx, in.OrchestratorID, in.Name)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	if existing != nil {
		if existing.Kind != in.Kind {
			return nil, fmt.Errorf("roles.Create: role %q exists with kind %q, not %q", in.Name, existing.Kind, in.Kind)
		}
		return existing, nil
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO roles (orchestrator_id, name, kind, argus_project, mission, constraints, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		in.OrchestratorID, in.Name, string(in.Kind), in.ArgusProject, in.Mission, in.Constraints, now,
	)
	if err != nil {
		return nil, fmt.Errorf("roles.Create: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	t, _ := time.Parse(time.RFC3339Nano, now)
	if r.events != nil {
		r.events.Emit(Event{Entity: EntityRole, Op: OpInsert, ID: id})
	}
	return &Role{
		ID:             id,
		OrchestratorID: in.OrchestratorID,
		Name:           in.Name,
		Kind:           in.Kind,
		ArgusProject:   in.ArgusProject,
		Mission:        in.Mission,
		Constraints:    in.Constraints,
		CreatedAt:      t,
	}, nil
}

// GetByID loads a role by its id. Archived rows are returned; primary-key
// lookups are not filtered on archived_at.
func (r *RolesDAO) GetByID(ctx context.Context, id int64) (*Role, error) {
	return r.scanOne(ctx,
		`SELECT id, orchestrator_id, name, kind, argus_project, mission, constraints, created_at, archived_at
		 FROM roles WHERE id = ?`, id)
}

// GetByOrchestratorAndName loads the active role with the given
// (orchestrator_id, name). Archived rows are invisible to this lookup;
// use ListByOrchestratorInclusive or GetByID to address archived roles.
func (r *RolesDAO) GetByOrchestratorAndName(ctx context.Context, orchID int64, name string) (*Role, error) {
	return r.findActiveByOrchestratorAndName(ctx, orchID, name)
}

func (r *RolesDAO) findActiveByOrchestratorAndName(ctx context.Context, orchID int64, name string) (*Role, error) {
	return r.scanOne(ctx,
		`SELECT id, orchestrator_id, name, kind, argus_project, mission, constraints, created_at, archived_at
		 FROM roles WHERE orchestrator_id = ? AND name = ? AND archived_at IS NULL`,
		orchID, name)
}

// ListByOrchestrator returns every active role under an orchestrator
// ordered by kind (coordinator first) then by name. Use
// ListByOrchestratorInclusive to also include archived rows.
func (r *RolesDAO) ListByOrchestrator(ctx context.Context, orchID int64) ([]*Role, error) {
	return r.listByOrchestrator(ctx, orchID, false)
}

// ListByOrchestratorInclusive returns every role under an orchestrator
// (active + archived) ordered by kind then by name.
func (r *RolesDAO) ListByOrchestratorInclusive(ctx context.Context, orchID int64) ([]*Role, error) {
	return r.listByOrchestrator(ctx, orchID, true)
}

func (r *RolesDAO) listByOrchestrator(ctx context.Context, orchID int64, includeArchived bool) ([]*Role, error) {
	query := `SELECT id, orchestrator_id, name, kind, argus_project, mission, constraints, created_at, archived_at
	          FROM roles WHERE orchestrator_id = ?`
	if !includeArchived {
		query += ` AND archived_at IS NULL`
	}
	query += ` ORDER BY (CASE kind WHEN 'coordinator' THEN 0 WHEN 'worker' THEN 1 ELSE 2 END), name`

	rows, err := r.db.QueryContext(ctx, query, orchID)
	if err != nil {
		return nil, fmt.Errorf("roles.ListByOrchestrator: %w", err)
	}
	defer rows.Close()

	var out []*Role
	for rows.Next() {
		role, err := r.scanFromRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, role)
	}
	return out, rows.Err()
}

// Archive marks a role as archived by setting archived_at to the current
// UTC timestamp. Idempotent: re-archiving an already-archived row is a
// no-op (the original archived_at is preserved). Returns ErrNotFound if
// no row matches the given id.
func (r *RolesDAO) Archive(ctx context.Context, id int64) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := r.db.ExecContext(ctx,
		`UPDATE roles SET archived_at = ? WHERE id = ? AND archived_at IS NULL`,
		now, id,
	)
	if err != nil {
		return fmt.Errorf("roles.Archive: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		if _, err := r.GetByID(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// Unarchive clears archived_at on a role. Idempotent: unarchiving an
// already-active row is a no-op. Returns ErrNotFound if no row matches
// the given id.
func (r *RolesDAO) Unarchive(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE roles SET archived_at = NULL WHERE id = ? AND archived_at IS NOT NULL`,
		id,
	)
	if err != nil {
		return fmt.Errorf("roles.Unarchive: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		if _, err := r.GetByID(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// Rename updates a role's name. The new name must be unique within the
// role's orchestrator across non-archived roles; archived siblings with
// the same name do not block the rename, and the same role name may
// coexist across different orchestrators. Returns ErrNotFound if no row
// matches id, or ErrNameConflict if newName is already held by another
// active role under the same orchestrator.
func (r *RolesDAO) Rename(ctx context.Context, id int64, newName string) error {
	cur, err := r.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if cur.Name == newName {
		return nil
	}

	var existsID int64
	err = r.db.QueryRowContext(ctx,
		`SELECT id FROM roles
		 WHERE orchestrator_id = ? AND name = ? AND archived_at IS NULL AND id != ?`,
		cur.OrchestratorID, newName, id,
	).Scan(&existsID)
	if err == nil {
		return fmt.Errorf("roles.Rename: %w: name %q held by role id=%d under orchestrator %d",
			ErrNameConflict, newName, existsID, cur.OrchestratorID)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("roles.Rename: %w", err)
	}

	if _, err := r.db.ExecContext(ctx,
		`UPDATE roles SET name = ? WHERE id = ?`,
		newName, id,
	); err != nil {
		return fmt.Errorf("roles.Rename: %w", err)
	}
	return nil
}

func (r *RolesDAO) scanOne(ctx context.Context, query string, args ...any) (*Role, error) {
	row := r.db.QueryRowContext(ctx, query, args...)
	role := &Role{}
	var kind, createdAt string
	var archivedAt sql.NullString
	if err := row.Scan(&role.ID, &role.OrchestratorID, &role.Name, &kind,
		&role.ArgusProject, &role.Mission, &role.Constraints, &createdAt, &archivedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	role.Kind = RoleKind(kind)
	role.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	if archivedAt.Valid {
		t, _ := time.Parse(time.RFC3339Nano, archivedAt.String)
		role.ArchivedAt = &t
	}
	return role, nil
}

func (r *RolesDAO) scanFromRows(rows *sql.Rows) (*Role, error) {
	role := &Role{}
	var kind, createdAt string
	var archivedAt sql.NullString
	if err := rows.Scan(&role.ID, &role.OrchestratorID, &role.Name, &kind,
		&role.ArgusProject, &role.Mission, &role.Constraints, &createdAt, &archivedAt); err != nil {
		return nil, err
	}
	role.Kind = RoleKind(kind)
	role.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	if archivedAt.Valid {
		t, _ := time.Parse(time.RFC3339Nano, archivedAt.String)
		role.ArchivedAt = &t
	}
	return role, nil
}
