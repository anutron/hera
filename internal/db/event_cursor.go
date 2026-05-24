package db

import (
	"context"
	"database/sql"
	"fmt"
)

// EventCursorDAO owns the singleton event_cursor row.
type EventCursorDAO struct{ db *sql.DB }

// Get returns the last-seen event id (0 if never persisted).
func (e *EventCursorDAO) Get(ctx context.Context) (int64, error) {
	var id int64
	err := e.db.QueryRowContext(ctx,
		`SELECT last_seen_event_id FROM event_cursor WHERE id = 1`,
	).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("event_cursor.Get: %w", err)
	}
	return id, nil
}

// Set advances the cursor to the given event id. Only advances; calls with
// a smaller id are silently ignored so out-of-order delivery does not
// regress the cursor.
func (e *EventCursorDAO) Set(ctx context.Context, lastSeenEventID int64) error {
	_, err := e.db.ExecContext(ctx,
		`UPDATE event_cursor SET last_seen_event_id = ?
		 WHERE id = 1 AND last_seen_event_id < ?`,
		lastSeenEventID, lastSeenEventID,
	)
	if err != nil {
		return fmt.Errorf("event_cursor.Set: %w", err)
	}
	return nil
}
