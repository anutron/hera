package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// TreeCursorsDAO manages per-role tree-scan cursors.
type TreeCursorsDAO struct {
	db *sql.DB
}

// GetTreeCursor returns the stored cursor for roleID, or 0 if none exists.
func (t *TreeCursorsDAO) GetTreeCursor(ctx context.Context, roleID int64) (int64, error) {
	var cursor int64
	err := t.db.QueryRowContext(ctx,
		`SELECT cursor FROM tree_read_cursors WHERE role_id = ?`, roleID,
	).Scan(&cursor)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("tree_cursors.Get: %w", err)
	}
	return cursor, nil
}

// UpsertTreeCursor inserts or replaces the cursor for roleID.
func (t *TreeCursorsDAO) UpsertTreeCursor(ctx context.Context, roleID, cursor int64) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := t.db.ExecContext(ctx,
		`INSERT INTO tree_read_cursors (role_id, cursor, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT(role_id) DO UPDATE SET cursor = excluded.cursor, updated_at = excluded.updated_at`,
		roleID, cursor, now,
	)
	if err != nil {
		return fmt.Errorf("tree_cursors.Upsert: %w", err)
	}
	return nil
}
