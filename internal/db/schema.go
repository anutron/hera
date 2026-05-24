package db

import (
	"database/sql"
	"fmt"
)

// migration is one ordered schema step. New migrations append to migrations
// below; the slice index determines the version.
type migration struct {
	name string
	sql  string
}

var migrations = []migration{
	{
		name: "0001_initial",
		sql: `
CREATE TABLE orchestrators (
    id          INTEGER PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    created_at  TEXT NOT NULL
);

CREATE TABLE roles (
    id              INTEGER PRIMARY KEY,
    orchestrator_id INTEGER NOT NULL REFERENCES orchestrators(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    kind            TEXT NOT NULL CHECK (kind IN ('coordinator','worker','freelance')),
    argus_project   TEXT NOT NULL,
    mission         TEXT NOT NULL DEFAULT '',
    constraints     TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL,
    UNIQUE (orchestrator_id, name)
);

CREATE INDEX roles_by_kind ON roles(orchestrator_id, kind);

CREATE TABLE bindings (
    id             INTEGER PRIMARY KEY,
    role_id        INTEGER NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    argus_task_id  TEXT NOT NULL,
    worktree_path  TEXT NOT NULL,
    started_at     TEXT NOT NULL,
    ended_at       TEXT,
    end_reason     TEXT
);

CREATE INDEX bindings_live_by_role ON bindings(role_id) WHERE ended_at IS NULL;
CREATE INDEX bindings_by_task ON bindings(argus_task_id) WHERE ended_at IS NULL;
CREATE INDEX bindings_by_worktree ON bindings(worktree_path) WHERE ended_at IS NULL;

CREATE TABLE messages (
    id            INTEGER PRIMARY KEY,
    from_role_id  INTEGER NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    to_role_id    INTEGER NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    body          TEXT NOT NULL,
    in_reply_to   INTEGER REFERENCES messages(id) ON DELETE SET NULL,
    sent_at       TEXT NOT NULL,
    read_at       TEXT,
    delivery_mode TEXT NOT NULL DEFAULT 'pending',
    delivered_at  TEXT
);

CREATE INDEX messages_inbox ON messages(to_role_id, read_at) WHERE read_at IS NULL;
CREATE INDEX messages_by_from ON messages(from_role_id, sent_at);

CREATE TABLE role_status (
    role_id    INTEGER PRIMARY KEY REFERENCES roles(id) ON DELETE CASCADE,
    status     TEXT NOT NULL CHECK (status IN ('idle','working','blocked','done')),
    updated_at TEXT NOT NULL
);

CREATE TABLE event_cursor (
    id                 INTEGER PRIMARY KEY CHECK (id = 1),
    last_seen_event_id INTEGER NOT NULL DEFAULT 0
);
INSERT INTO event_cursor (id, last_seen_event_id) VALUES (1, 0);

CREATE TABLE config (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
`,
	},
}

// migrate runs every migration whose index exceeds the database's stored
// user_version. user_version starts at 0 in a fresh database.
func (d *DB) migrate() error {
	var version int
	if err := d.sqldb.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}

	for i := version; i < len(migrations); i++ {
		m := migrations[i]
		if err := applyMigration(d.sqldb, m, i+1); err != nil {
			return fmt.Errorf("migration %d (%s): %w", i+1, m.name, err)
		}
	}
	return nil
}

func applyMigration(sqldb *sql.DB, m migration, newVersion int) error {
	tx, err := sqldb.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(m.sql); err != nil {
		return err
	}

	// PRAGMA user_version does not accept bound parameters; format the int
	// inline. newVersion is internal, not user-supplied.
	if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", newVersion)); err != nil {
		return err
	}

	return tx.Commit()
}

// SchemaVersion reports the current applied schema version.
func (d *DB) SchemaVersion() (int, error) {
	var v int
	err := d.sqldb.QueryRow(`PRAGMA user_version`).Scan(&v)
	return v, err
}
