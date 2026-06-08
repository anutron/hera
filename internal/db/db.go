package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// DB wraps a *sql.DB and exposes the typed DAOs hera uses for its local
// state.
//
// Open creates the parent directory if missing, opens the SQLite file in
// WAL mode, and runs any pending schema migrations.
type DB struct {
	sqldb *sql.DB
	path  string

	Orchestrators *OrchestratorsDAO
	Roles         *RolesDAO
	Bindings      *BindingsDAO
	Messages      *MessagesDAO
	RoleStatus    *RoleStatusDAO
	EventCursor   *EventCursorDAO
	Config        *ConfigDAO
	TreeCursors   *TreeCursorsDAO

	// Events broadcasts DAO writes on the rail-watched tables
	// (orchestrators, roles, bindings) to in-process subscribers
	// such as the rail UI. See internal/db/events.go.
	Events *Broadcaster
}

// Open returns a DB connected to the SQLite file at path. If the file does
// not exist it is created. WAL mode is enabled and migrations run to the
// latest version before Open returns.
func Open(path string) (*DB, error) {
	if path == "" {
		return nil, fmt.Errorf("db.Open: empty path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("db.Open: mkdir parent: %w", err)
	}

	// _journal_mode=WAL is set up-front; busy_timeout avoids spurious
	// SQLITE_BUSY when a reader and writer race.
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)", path)
	sqldb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("db.Open: sql.Open: %w", err)
	}
	if err := sqldb.Ping(); err != nil {
		_ = sqldb.Close()
		return nil, fmt.Errorf("db.Open: ping: %w", err)
	}

	d := &DB{sqldb: sqldb, path: path}
	if err := d.migrate(); err != nil {
		_ = sqldb.Close()
		return nil, fmt.Errorf("db.Open: migrate: %w", err)
	}

	d.Events = NewBroadcaster()
	d.Orchestrators = &OrchestratorsDAO{db: sqldb, events: d.Events}
	d.Roles = &RolesDAO{db: sqldb, events: d.Events}
	d.Bindings = &BindingsDAO{db: sqldb, events: d.Events}
	d.Messages = &MessagesDAO{db: sqldb}
	d.RoleStatus = &RoleStatusDAO{db: sqldb}
	d.EventCursor = &EventCursorDAO{db: sqldb}
	d.Config = &ConfigDAO{db: sqldb}
	d.TreeCursors = &TreeCursorsDAO{db: sqldb}

	return d, nil
}

// Close shuts down the underlying *sql.DB and the events broadcaster.
func (d *DB) Close() error {
	if d == nil || d.sqldb == nil {
		return nil
	}
	if d.Events != nil {
		d.Events.Close()
	}
	return d.sqldb.Close()
}

// Path returns the on-disk path of the SQLite file.
func (d *DB) Path() string { return d.path }

// Raw returns the wrapped *sql.DB. Reserved for tests; production code
// should use the DAOs.
func (d *DB) Raw() *sql.DB { return d.sqldb }
