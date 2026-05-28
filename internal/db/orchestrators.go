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

// ErrNameConflict is returned when a name change collides with an existing
// active row in the same scope.
var ErrNameConflict = errors.New("db: name already in use by an active row")

// ErrAmbiguous is returned by lookups that expect at most one row but find
// multiple. Callers must disambiguate (typically by providing an additional
// filter). The bindings DAO returns this when a task or worktree holds 2+
// live bindings across different orchestrators and the caller asked for the
// orchestrator-agnostic single-row variant.
var ErrAmbiguous = errors.New("db: lookup ambiguous (multiple rows match)")

// OrchestratorsDAO is the typed accessor for the orchestrators table.
type OrchestratorsDAO struct {
	db     *sql.DB
	events *Broadcaster
}

// Create inserts a new active orchestrator. If an active orchestrator with
// the same name already exists, Create returns the existing row
// (idempotent). An archived orchestrator with the same name does NOT
// block creation — a fresh active row is inserted in that case.
func (o *OrchestratorsDAO) Create(ctx context.Context, name string) (*Orchestrator, error) {
	existing, err := o.findActiveByName(ctx, name)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := o.db.ExecContext(ctx,
		`INSERT INTO orchestrators (name, created_at) VALUES (?, ?)`,
		name, now,
	)
	if err != nil {
		return nil, fmt.Errorf("orchestrators.Create: %w", err)
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

// GetByName loads the active orchestrator with the given name. Archived
// rows are not returned; an archived row with this name is invisible to
// GetByName. Use GetByID to address an archived orchestrator directly.
func (o *OrchestratorsDAO) GetByName(ctx context.Context, name string) (*Orchestrator, error) {
	return o.findActiveByName(ctx, name)
}

func (o *OrchestratorsDAO) findActiveByName(ctx context.Context, name string) (*Orchestrator, error) {
	row := o.db.QueryRowContext(ctx,
		`SELECT id, name, created_at, archived_at FROM orchestrators
		 WHERE name = ? AND archived_at IS NULL`,
		name,
	)
	return scanOrchestrator(row)
}

// GetByID loads an orchestrator by its id. Archived rows are returned;
// primary-key lookups are not filtered on archived_at.
func (o *OrchestratorsDAO) GetByID(ctx context.Context, id int64) (*Orchestrator, error) {
	row := o.db.QueryRowContext(ctx,
		`SELECT id, name, created_at, archived_at FROM orchestrators WHERE id = ?`,
		id,
	)
	return scanOrchestrator(row)
}

// List returns every active (non-archived) orchestrator ordered by name.
// Use ListInclusive to also include archived rows.
func (o *OrchestratorsDAO) List(ctx context.Context) ([]*Orchestrator, error) {
	return o.list(ctx, false)
}

// ListInclusive returns every orchestrator (active + archived) ordered by
// name.
func (o *OrchestratorsDAO) ListInclusive(ctx context.Context) ([]*Orchestrator, error) {
	return o.list(ctx, true)
}

func (o *OrchestratorsDAO) list(ctx context.Context, includeArchived bool) ([]*Orchestrator, error) {
	query := `SELECT id, name, created_at, archived_at FROM orchestrators`
	if !includeArchived {
		query += ` WHERE archived_at IS NULL`
	}
	query += ` ORDER BY name`

	rows, err := o.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("orchestrators.List: %w", err)
	}
	defer rows.Close()

	var out []*Orchestrator
	for rows.Next() {
		orch, err := scanOrchestratorRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, orch)
	}
	return out, rows.Err()
}

// Archive marks an orchestrator as archived by setting archived_at to the
// current UTC timestamp. Idempotent: re-archiving an already-archived row
// is a no-op (the original archived_at is preserved). Returns ErrNotFound
// if no row matches the given id.
func (o *OrchestratorsDAO) Archive(ctx context.Context, id int64) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := o.db.ExecContext(ctx,
		`UPDATE orchestrators SET archived_at = ? WHERE id = ? AND archived_at IS NULL`,
		now, id,
	)
	if err != nil {
		return fmt.Errorf("orchestrators.Archive: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		if _, err := o.GetByID(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// Unarchive clears archived_at on an orchestrator. Idempotent: unarchiving
// an already-active row is a no-op. Returns ErrNotFound if no row matches
// the given id.
func (o *OrchestratorsDAO) Unarchive(ctx context.Context, id int64) error {
	res, err := o.db.ExecContext(ctx,
		`UPDATE orchestrators SET archived_at = NULL WHERE id = ? AND archived_at IS NOT NULL`,
		id,
	)
	if err != nil {
		return fmt.Errorf("orchestrators.Unarchive: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		if _, err := o.GetByID(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// Rename updates an orchestrator's name. The new name must be unique
// across non-archived orchestrators; archived rows with the same name do
// not block the rename. Returns ErrNotFound if no row matches id, or
// ErrNameConflict if newName is already held by another active row.
func (o *OrchestratorsDAO) Rename(ctx context.Context, id int64, newName string) error {
	cur, err := o.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if cur.Name == newName {
		return nil
	}

	var existsID int64
	err = o.db.QueryRowContext(ctx,
		`SELECT id FROM orchestrators
		 WHERE name = ? AND archived_at IS NULL AND id != ?`,
		newName, id,
	).Scan(&existsID)
	if err == nil {
		return fmt.Errorf("orchestrators.Rename: %w: name %q held by id=%d", ErrNameConflict, newName, existsID)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("orchestrators.Rename: %w", err)
	}

	if _, err := o.db.ExecContext(ctx,
		`UPDATE orchestrators SET name = ? WHERE id = ?`,
		newName, id,
	); err != nil {
		return fmt.Errorf("orchestrators.Rename: %w", err)
	}
	return nil
}

func scanOrchestrator(row *sql.Row) (*Orchestrator, error) {
	var orch Orchestrator
	var createdAt string
	var archivedAt sql.NullString
	if err := row.Scan(&orch.ID, &orch.Name, &createdAt, &archivedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("orchestrators.scan: %w", err)
	}
	orch.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	if archivedAt.Valid {
		t, _ := time.Parse(time.RFC3339Nano, archivedAt.String)
		orch.ArchivedAt = &t
	}
	return &orch, nil
}

func scanOrchestratorRows(rows *sql.Rows) (*Orchestrator, error) {
	var orch Orchestrator
	var createdAt string
	var archivedAt sql.NullString
	if err := rows.Scan(&orch.ID, &orch.Name, &createdAt, &archivedAt); err != nil {
		return nil, err
	}
	orch.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	if archivedAt.Valid {
		t, _ := time.Parse(time.RFC3339Nano, archivedAt.String)
		orch.ArchivedAt = &t
	}
	return &orch, nil
}
