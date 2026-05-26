package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// BindingsDAO is the typed accessor for the bindings table.
type BindingsDAO struct{ db *sql.DB }

// CreateBindingInput captures the fields needed to start a binding.
type CreateBindingInput struct {
	RoleID       int64
	ArgusTaskID  string
	WorktreePath string
}

// Create inserts a new live binding row (ended_at NULL).
func (b *BindingsDAO) Create(ctx context.Context, in CreateBindingInput) (*Binding, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := b.db.ExecContext(ctx,
		`INSERT INTO bindings (role_id, argus_task_id, worktree_path, started_at)
		 VALUES (?, ?, ?, ?)`,
		in.RoleID, in.ArgusTaskID, in.WorktreePath, now,
	)
	if err != nil {
		return nil, fmt.Errorf("bindings.Create: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	t, _ := time.Parse(time.RFC3339Nano, now)
	return &Binding{
		ID:           id,
		RoleID:       in.RoleID,
		ArgusTaskID:  in.ArgusTaskID,
		WorktreePath: in.WorktreePath,
		StartedAt:    t,
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
	return nil
}

// GetLiveByTaskID returns the live binding (if any) for an argus task id.
func (b *BindingsDAO) GetLiveByTaskID(ctx context.Context, taskID string) (*Binding, error) {
	return b.scanOne(ctx,
		`SELECT id, role_id, argus_task_id, worktree_path, started_at, ended_at, end_reason
		 FROM bindings WHERE argus_task_id = ? AND ended_at IS NULL`, taskID)
}

// GetLiveByWorktree returns the live binding (if any) for a worktree path.
func (b *BindingsDAO) GetLiveByWorktree(ctx context.Context, worktreePath string) (*Binding, error) {
	return b.scanOne(ctx,
		`SELECT id, role_id, argus_task_id, worktree_path, started_at, ended_at, end_reason
		 FROM bindings WHERE worktree_path = ? AND ended_at IS NULL`, worktreePath)
}

// GetLiveByRole returns the live binding (if any) for a role.
func (b *BindingsDAO) GetLiveByRole(ctx context.Context, roleID int64) (*Binding, error) {
	return b.scanOne(ctx,
		`SELECT id, role_id, argus_task_id, worktree_path, started_at, ended_at, end_reason
		 FROM bindings WHERE role_id = ? AND ended_at IS NULL`, roleID)
}

// ListLive returns every binding whose ended_at is NULL. Ordered by
// started_at ascending so callers iterating for startup-seed purposes
// see oldest bindings first (the order is informational, not contract).
func (b *BindingsDAO) ListLive(ctx context.Context) ([]*Binding, error) {
	rows, err := b.db.QueryContext(ctx,
		`SELECT id, role_id, argus_task_id, worktree_path, started_at, ended_at, end_reason
		 FROM bindings WHERE ended_at IS NULL ORDER BY started_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("bindings.ListLive: %w", err)
	}
	defer rows.Close()

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
		`SELECT id, role_id, argus_task_id, worktree_path, started_at, ended_at, end_reason
		 FROM bindings WHERE role_id = ? ORDER BY started_at DESC`, roleID)
	if err != nil {
		return nil, fmt.Errorf("bindings.ListByRole: %w", err)
	}
	defer rows.Close()

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
	if err := row.Scan(&bnd.ID, &bnd.RoleID, &bnd.ArgusTaskID, &bnd.WorktreePath,
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
	if err := rows.Scan(&bnd.ID, &bnd.RoleID, &bnd.ArgusTaskID, &bnd.WorktreePath,
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
