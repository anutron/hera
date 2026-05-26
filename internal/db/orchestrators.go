package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound is returned when a row lookup finds nothing.
var ErrNotFound = errors.New("db: row not found")

// OrchestratorsDAO is the typed accessor for the orchestrators table.
type OrchestratorsDAO struct {
	db     *sql.DB
	events *Broadcaster
}

// Create inserts a new orchestrator. If an orchestrator with the same name
// already exists, Create returns the existing row (idempotent).
func (o *OrchestratorsDAO) Create(ctx context.Context, name string) (*Orchestrator, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := o.db.ExecContext(ctx,
		`INSERT INTO orchestrators (name, created_at) VALUES (?, ?) ON CONFLICT(name) DO NOTHING`,
		name, now,
	)
	if err != nil {
		return nil, fmt.Errorf("orchestrators.Create: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Existing row; load and return.
		return o.GetByName(ctx, name)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("orchestrators.Create: LastInsertId: %w", err)
	}
	t, _ := time.Parse(time.RFC3339Nano, now)
	if o.events != nil {
		o.events.Emit(Event{Entity: EntityOrchestrator, Op: OpInsert, ID: id})
	}
	return &Orchestrator{ID: id, Name: name, CreatedAt: t}, nil
}

// GetByName loads an orchestrator by its unique name.
func (o *OrchestratorsDAO) GetByName(ctx context.Context, name string) (*Orchestrator, error) {
	row := o.db.QueryRowContext(ctx,
		`SELECT id, name, created_at FROM orchestrators WHERE name = ?`,
		name,
	)
	var orch Orchestrator
	var createdAt string
	if err := row.Scan(&orch.ID, &orch.Name, &createdAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("orchestrators.GetByName: %w", err)
	}
	orch.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	return &orch, nil
}

// GetByID loads an orchestrator by its id.
func (o *OrchestratorsDAO) GetByID(ctx context.Context, id int64) (*Orchestrator, error) {
	row := o.db.QueryRowContext(ctx,
		`SELECT id, name, created_at FROM orchestrators WHERE id = ?`,
		id,
	)
	var orch Orchestrator
	var createdAt string
	if err := row.Scan(&orch.ID, &orch.Name, &createdAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("orchestrators.GetByID: %w", err)
	}
	orch.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	return &orch, nil
}

// List returns every orchestrator ordered by name.
func (o *OrchestratorsDAO) List(ctx context.Context) ([]*Orchestrator, error) {
	rows, err := o.db.QueryContext(ctx, `SELECT id, name, created_at FROM orchestrators ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("orchestrators.List: %w", err)
	}
	defer rows.Close()

	var out []*Orchestrator
	for rows.Next() {
		var orch Orchestrator
		var createdAt string
		if err := rows.Scan(&orch.ID, &orch.Name, &createdAt); err != nil {
			return nil, err
		}
		orch.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		out = append(out, &orch)
	}
	return out, rows.Err()
}
