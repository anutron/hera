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

// Create inserts a new role under the given orchestrator. Returns the
// existing row if (orchestrator_id, name) already exists with the same
// kind; returns an error if it exists with a different kind.
//
// Roles are write-once on mission/constraints/argus_project: if a role
// with the same (orchestrator_id, name) already exists, the supplied
// Mission, Constraints, and ArgusProject inputs are SILENTLY IGNORED
// and the existing row's values are preserved. This is intentional —
// a role is a durable identity established at first creation; subsequent
// agents claiming the same role inherit the original mission. To change
// a role's mission, the caller must edit the row directly (v1 has no
// hera_update_role tool; planned for v1.1 if needed).
func (r *RolesDAO) Create(ctx context.Context, in CreateRoleInput) (*Role, error) {
	existing, err := r.GetByOrchestratorAndName(ctx, in.OrchestratorID, in.Name)
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

// GetByID loads a role by its id.
func (r *RolesDAO) GetByID(ctx context.Context, id int64) (*Role, error) {
	return r.scanOne(ctx,
		`SELECT id, orchestrator_id, name, kind, argus_project, mission, constraints, created_at
		 FROM roles WHERE id = ?`, id)
}

// GetByOrchestratorAndName loads a role by its unique (orchestrator_id, name).
func (r *RolesDAO) GetByOrchestratorAndName(ctx context.Context, orchID int64, name string) (*Role, error) {
	return r.scanOne(ctx,
		`SELECT id, orchestrator_id, name, kind, argus_project, mission, constraints, created_at
		 FROM roles WHERE orchestrator_id = ? AND name = ?`, orchID, name)
}

// ListByOrchestrator returns every role under an orchestrator ordered by
// kind (coordinator first) then by name.
func (r *RolesDAO) ListByOrchestrator(ctx context.Context, orchID int64) ([]*Role, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, orchestrator_id, name, kind, argus_project, mission, constraints, created_at
		 FROM roles WHERE orchestrator_id = ?
		 ORDER BY (CASE kind WHEN 'coordinator' THEN 0 WHEN 'worker' THEN 1 ELSE 2 END), name`, orchID)
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

func (r *RolesDAO) scanOne(ctx context.Context, query string, args ...any) (*Role, error) {
	row := r.db.QueryRowContext(ctx, query, args...)
	role := &Role{}
	var kind, createdAt string
	if err := row.Scan(&role.ID, &role.OrchestratorID, &role.Name, &kind,
		&role.ArgusProject, &role.Mission, &role.Constraints, &createdAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	role.Kind = RoleKind(kind)
	role.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	return role, nil
}

func (r *RolesDAO) scanFromRows(rows *sql.Rows) (*Role, error) {
	role := &Role{}
	var kind, createdAt string
	if err := rows.Scan(&role.ID, &role.OrchestratorID, &role.Name, &kind,
		&role.ArgusProject, &role.Mission, &role.Constraints, &createdAt); err != nil {
		return nil, err
	}
	role.Kind = RoleKind(kind)
	role.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	return role, nil
}
