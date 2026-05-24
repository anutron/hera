package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// RoleStatusDAO is the typed accessor for the role_status table.
type RoleStatusDAO struct{ db *sql.DB }

// Upsert sets the status for a role, inserting or updating as needed.
func (r *RoleStatusDAO) Upsert(ctx context.Context, roleID int64, status RoleStatusValue) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO role_status (role_id, status, updated_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(role_id) DO UPDATE SET status = excluded.status, updated_at = excluded.updated_at`,
		roleID, string(status), now,
	)
	if err != nil {
		return fmt.Errorf("role_status.Upsert: %w", err)
	}
	return nil
}

// Get returns the current status for a role, or ErrNotFound if none is set.
func (r *RoleStatusDAO) Get(ctx context.Context, roleID int64) (*RoleStatus, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT role_id, status, updated_at FROM role_status WHERE role_id = ?`, roleID,
	)
	var rs RoleStatus
	var status, updatedAt string
	if err := row.Scan(&rs.RoleID, &status, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("role_status.Get: %w", err)
	}
	rs.Status = RoleStatusValue(status)
	rs.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return &rs, nil
}
