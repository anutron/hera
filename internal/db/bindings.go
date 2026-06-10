package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// BindingsDAO is the typed accessor for the bindings table.
type BindingsDAO struct {
	db     *sql.DB
	events *Broadcaster
}

// CreateBindingInput captures the fields needed to start a binding.
// OrchestratorID must equal the role's orchestrator_id; callers
// resolve it before calling Create so the row is correct without an
// extra JOIN inside the DAO.
type CreateBindingInput struct {
	RoleID         int64
	OrchestratorID int64
	ArgusTaskID    string
	WorktreePath   string
}

// Create inserts a new live binding row (ended_at NULL). If
// OrchestratorID is zero on the input, Create derives it from the
// role's orchestrator_id so callers that haven't been migrated to
// the explicit form still produce well-formed rows.
func (b *BindingsDAO) Create(ctx context.Context, in CreateBindingInput) (*Binding, error) {
	if in.OrchestratorID == 0 {
		if err := b.db.QueryRowContext(ctx,
			`SELECT orchestrator_id FROM roles WHERE id = ?`, in.RoleID,
		).Scan(&in.OrchestratorID); err != nil {
			return nil, fmt.Errorf("bindings.Create: derive orchestrator_id from role %d: %w", in.RoleID, err)
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := b.db.ExecContext(ctx,
		`INSERT INTO bindings (role_id, orchestrator_id, argus_task_id, worktree_path, started_at)
		 VALUES (?, ?, ?, ?, ?)`,
		in.RoleID, in.OrchestratorID, in.ArgusTaskID, in.WorktreePath, now,
	)
	if err != nil {
		return nil, fmt.Errorf("bindings.Create: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	t, _ := time.Parse(time.RFC3339Nano, now)
	if b.events != nil {
		b.events.Emit(Event{Entity: EntityBinding, Op: OpInsert, ID: id})
	}
	return &Binding{
		ID:             id,
		RoleID:         in.RoleID,
		OrchestratorID: in.OrchestratorID,
		ArgusTaskID:    in.ArgusTaskID,
		WorktreePath:   in.WorktreePath,
		StartedAt:      t,
	}, nil
}

// End marks a binding as ended with the given reason. Returns ErrNotFound
// if no live binding exists with that id.
func (b *BindingsDAO) End(ctx context.Context, bindingID int64, reason string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := b.db.ExecContext(ctx,
		`UPDATE bindings SET ended_at = ?, end_reason = ?
		 WHERE id = ? AND ended_at IS NULL`,
		now, reason, bindingID,
	)
	if err != nil {
		return fmt.Errorf("bindings.End: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	if b.events != nil {
		b.events.Emit(Event{Entity: EntityBinding, Op: OpUpdate, ID: bindingID})
	}
	return nil
}

// GetLiveByTaskID returns the live binding for an argus task id when
// exactly one exists. Returns ErrNotFound if none exists and
// ErrAmbiguous if two or more exist (the multi-binding case introduced
// by migration 0004). Callers in the multi-binding case should switch
// to ListLiveByTaskID or GetLiveByTaskAndOrchestrator.
func (b *BindingsDAO) GetLiveByTaskID(ctx context.Context, taskID string) (*Binding, error) {
	rows, err := b.db.QueryContext(ctx,
		`SELECT id, role_id, orchestrator_id, argus_task_id, worktree_path, started_at, ended_at, end_reason
		 FROM bindings WHERE argus_task_id = ? AND ended_at IS NULL`, taskID)
	if err != nil {
		return nil, fmt.Errorf("bindings.GetLiveByTaskID: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var found *Binding
	for rows.Next() {
		bnd, err := scanBindingRow(rows)
		if err != nil {
			return nil, err
		}
		if found != nil {
			return nil, ErrAmbiguous
		}
		found = bnd
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if found == nil {
		return nil, ErrNotFound
	}
	return found, nil
}

// GetLiveByTaskAndOrchestrator returns the live binding for (taskID,
// orchID) if one exists. ErrNotFound otherwise. This is the primary
// lookup for handlers in the multi-binding world: each tool call
// resolves its own orchestrator context first, then asks "is THIS
// task bound under THAT orchestrator?"
func (b *BindingsDAO) GetLiveByTaskAndOrchestrator(ctx context.Context, taskID string, orchID int64) (*Binding, error) {
	return b.scanOne(ctx,
		`SELECT id, role_id, orchestrator_id, argus_task_id, worktree_path, started_at, ended_at, end_reason
		 FROM bindings WHERE argus_task_id = ? AND orchestrator_id = ? AND ended_at IS NULL`,
		taskID, orchID)
}

// ListLiveByTaskID returns every live binding for an argus task,
// ordered by started_at ASC. Empty slice if none. Used by the
// auto-adopt parent-disambiguation path and by the task.archived
// handler that ends every binding for the archived task.
func (b *BindingsDAO) ListLiveByTaskID(ctx context.Context, taskID string) ([]*Binding, error) {
	rows, err := b.db.QueryContext(ctx,
		`SELECT id, role_id, orchestrator_id, argus_task_id, worktree_path, started_at, ended_at, end_reason
		 FROM bindings WHERE argus_task_id = ? AND ended_at IS NULL
		 ORDER BY started_at ASC`, taskID)
	if err != nil {
		return nil, fmt.Errorf("bindings.ListLiveByTaskID: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []*Binding
	for rows.Next() {
		bnd, err := scanBindingRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, bnd)
	}
	return out, rows.Err()
}

// ListByTaskID returns EVERY binding for an argus task id — live AND ended —
// across all roles and orchestrators, ordered by started_at ascending (id
// breaks same-instant ties). Backs ReparentCoordinator's idempotent teardown
// (BUG-026): a parent-link binding the resync reconciler ended (end_reason
// 'resync_missing', when the coord task is gone from argus) leaves its link
// role behind, so a live-only lookup misses it and the next re-parent piles up
// a de-collided duplicate. Listing all states lets the re-parent delete every
// stale link role first.
func (b *BindingsDAO) ListByTaskID(ctx context.Context, taskID string) ([]*Binding, error) {
	rows, err := b.db.QueryContext(ctx,
		`SELECT id, role_id, orchestrator_id, argus_task_id, worktree_path, started_at, ended_at, end_reason
		 FROM bindings WHERE argus_task_id = ?
		 ORDER BY started_at ASC, id ASC`, taskID)
	if err != nil {
		return nil, fmt.Errorf("bindings.ListByTaskID: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []*Binding
	for rows.Next() {
		bnd, err := scanBindingRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, bnd)
	}
	return out, rows.Err()
}

// GetLiveByWorktree returns the live binding (if any) for a worktree path.
// Returns ErrAmbiguous if 2+ live bindings exist for that worktree (the
// multi-binding case). Most callers should use the per-orchestrator
// variants instead.
func (b *BindingsDAO) GetLiveByWorktree(ctx context.Context, worktreePath string) (*Binding, error) {
	rows, err := b.db.QueryContext(ctx,
		`SELECT id, role_id, orchestrator_id, argus_task_id, worktree_path, started_at, ended_at, end_reason
		 FROM bindings WHERE worktree_path = ? AND ended_at IS NULL`, worktreePath)
	if err != nil {
		return nil, fmt.Errorf("bindings.GetLiveByWorktree: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var found *Binding
	for rows.Next() {
		bnd, err := scanBindingRow(rows)
		if err != nil {
			return nil, err
		}
		if found != nil {
			return nil, ErrAmbiguous
		}
		found = bnd
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if found == nil {
		return nil, ErrNotFound
	}
	return found, nil
}

// GetLiveByRole returns the live binding (if any) for a role.
func (b *BindingsDAO) GetLiveByRole(ctx context.Context, roleID int64) (*Binding, error) {
	return b.scanOne(ctx,
		`SELECT id, role_id, orchestrator_id, argus_task_id, worktree_path, started_at, ended_at, end_reason
		 FROM bindings WHERE role_id = ? AND ended_at IS NULL`, roleID)
}

// GetLatestByRole returns the role's most recent binding by started_at
// regardless of whether it has ended (id breaks same-instant ties).
// Returns ErrNotFound when the role has never had a binding. Backs the
// role-ops fallback for archived rows: archiving a task ENDS its binding
// (end_reason='argus_archived') while preserving the argus_task_id, so a
// live-only lookup misses exactly the rows whose ops still need the task.
func (b *BindingsDAO) GetLatestByRole(ctx context.Context, roleID int64) (*Binding, error) {
	return b.scanOne(ctx,
		`SELECT id, role_id, orchestrator_id, argus_task_id, worktree_path, started_at, ended_at, end_reason
		 FROM bindings WHERE role_id = ? ORDER BY started_at DESC, id DESC LIMIT 1`, roleID)
}

// ListLive returns every binding whose ended_at is NULL. Ordered by
// started_at ascending so callers iterating for startup-seed purposes
// see oldest bindings first (the order is informational, not contract).
func (b *BindingsDAO) ListLive(ctx context.Context) ([]*Binding, error) {
	rows, err := b.db.QueryContext(ctx,
		`SELECT id, role_id, orchestrator_id, argus_task_id, worktree_path, started_at, ended_at, end_reason
		 FROM bindings WHERE ended_at IS NULL ORDER BY started_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("bindings.ListLive: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []*Binding
	for rows.Next() {
		bnd, err := scanBindingRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, bnd)
	}
	return out, rows.Err()
}

// ListByRole returns every binding for a role ordered by started_at desc.
// The first row is the live binding (if any).
func (b *BindingsDAO) ListByRole(ctx context.Context, roleID int64) ([]*Binding, error) {
	rows, err := b.db.QueryContext(ctx,
		`SELECT id, role_id, orchestrator_id, argus_task_id, worktree_path, started_at, ended_at, end_reason
		 FROM bindings WHERE role_id = ? ORDER BY started_at DESC`, roleID)
	if err != nil {
		return nil, fmt.Errorf("bindings.ListByRole: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []*Binding
	for rows.Next() {
		bnd, err := scanBindingRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, bnd)
	}
	return out, rows.Err()
}

func (b *BindingsDAO) scanOne(ctx context.Context, query string, args ...any) (*Binding, error) {
	row := b.db.QueryRowContext(ctx, query, args...)
	var bnd Binding
	var startedAt string
	var endedAt, endReason sql.NullString
	if err := row.Scan(&bnd.ID, &bnd.RoleID, &bnd.OrchestratorID, &bnd.ArgusTaskID, &bnd.WorktreePath,
		&startedAt, &endedAt, &endReason); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	bnd.StartedAt, _ = time.Parse(time.RFC3339Nano, startedAt)
	if endedAt.Valid {
		t, _ := time.Parse(time.RFC3339Nano, endedAt.String)
		bnd.EndedAt = &t
	}
	if endReason.Valid {
		bnd.EndReason = endReason.String
	}
	return &bnd, nil
}

func scanBindingRow(rows *sql.Rows) (*Binding, error) {
	var bnd Binding
	var startedAt string
	var endedAt, endReason sql.NullString
	if err := rows.Scan(&bnd.ID, &bnd.RoleID, &bnd.OrchestratorID, &bnd.ArgusTaskID, &bnd.WorktreePath,
		&startedAt, &endedAt, &endReason); err != nil {
		return nil, err
	}
	bnd.StartedAt, _ = time.Parse(time.RFC3339Nano, startedAt)
	if endedAt.Valid {
		t, _ := time.Parse(time.RFC3339Nano, endedAt.String)
		bnd.EndedAt = &t
	}
	if endReason.Valid {
		bnd.EndReason = endReason.String
	}
	return &bnd, nil
}
