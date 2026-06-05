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
		sql:  initialSchema,
	},
	{
		name: "0002_bindings_unique_live",
		sql: `
-- Defense in depth: prevent two live bindings from coexisting for the
-- same argus task, role, or worktree path. Handlers already pre-check
-- via GetLiveByTaskID, but a race between two MCP calls could slip
-- past the application-level guard. Partial unique indexes turn the
-- race into a deterministic INSERT failure instead of data corruption.
CREATE UNIQUE INDEX bindings_live_unique_task ON bindings(argus_task_id) WHERE ended_at IS NULL;
CREATE UNIQUE INDEX bindings_live_unique_role ON bindings(role_id) WHERE ended_at IS NULL;
CREATE UNIQUE INDEX bindings_live_unique_worktree ON bindings(worktree_path) WHERE ended_at IS NULL;
`,
	},
	{
		name: "0003_archived_at",
		sql: `
-- Add nullable archived_at (RFC3339 string) to orchestrators and roles,
-- and replace the column-level UNIQUE constraints with partial unique
-- indexes scoped to active (archived_at IS NULL) rows. This lets an
-- archived row coexist with a fresh active row of the same name, which
-- the hera-view rename/archive/resurrect semantics require.
--
-- SQLite cannot drop a column-level UNIQUE constraint in place, so we
-- follow the documented table-recreation pattern. FK enforcement is
-- deferred for the duration of this transaction so the intermediate
-- DROP/RENAME steps do not fail integrity checks; at commit, every
-- parent table is restored under its original name with all original
-- IDs intact, so child rows still resolve their FKs.
PRAGMA defer_foreign_keys = ON;

-- Recreate orchestrators: archived_at column + name UNIQUE becomes partial.
CREATE TABLE orchestrators_new (
    id          INTEGER PRIMARY KEY,
    name        TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    archived_at TEXT
);
INSERT INTO orchestrators_new (id, name, created_at)
    SELECT id, name, created_at FROM orchestrators;
DROP TABLE orchestrators;
ALTER TABLE orchestrators_new RENAME TO orchestrators;
CREATE UNIQUE INDEX orchestrators_active_name ON orchestrators(name) WHERE archived_at IS NULL;
CREATE INDEX orchestrators_by_archived ON orchestrators(archived_at);

-- Recreate roles: archived_at column + (orchestrator_id, name) UNIQUE becomes partial.
CREATE TABLE roles_new (
    id              INTEGER PRIMARY KEY,
    orchestrator_id INTEGER NOT NULL REFERENCES orchestrators(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    kind            TEXT NOT NULL CHECK (kind IN ('coordinator','worker','freelance')),
    argus_project   TEXT NOT NULL,
    mission         TEXT NOT NULL DEFAULT '',
    constraints     TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL,
    archived_at     TEXT
);
INSERT INTO roles_new (id, orchestrator_id, name, kind, argus_project, mission, constraints, created_at)
    SELECT id, orchestrator_id, name, kind, argus_project, mission, constraints, created_at FROM roles;
DROP TABLE roles;
ALTER TABLE roles_new RENAME TO roles;
CREATE INDEX roles_by_kind ON roles(orchestrator_id, kind);
CREATE UNIQUE INDEX roles_active_name ON roles(orchestrator_id, name) WHERE archived_at IS NULL;
CREATE INDEX roles_by_archived ON roles(archived_at);
`,
	},
	{
		name: "0004_bindings_per_orchestrator",
		sql: `
-- Relax the bindings uniqueness invariant from "one live binding per
-- argus task" to "one live binding per (argus task, orchestrator)".
-- This unlocks nested orchestration: a single argus task can be a
-- worker in orchestrator A and simultaneously a coordinator in
-- orchestrator B. Role-side uniqueness (one live binding per role)
-- stays — a role is still incarnated at most once.
--
-- The orchestrator_id column is denormalized from the role's
-- orchestrator so the partial-unique-by-(task, orch) and
-- (worktree, orch) indexes can be expressed directly in the index
-- definition without a JOIN. We back-fill from roles at migration
-- time; every existing binding row gets a non-NULL value because the
-- role FK guarantees a parent row exists.
ALTER TABLE bindings ADD COLUMN orchestrator_id INTEGER REFERENCES orchestrators(id) ON DELETE CASCADE;

UPDATE bindings
   SET orchestrator_id = (SELECT orchestrator_id FROM roles WHERE roles.id = bindings.role_id);

DROP INDEX bindings_live_unique_task;
DROP INDEX bindings_live_unique_worktree;

CREATE UNIQUE INDEX bindings_live_unique_task_orch
    ON bindings(argus_task_id, orchestrator_id) WHERE ended_at IS NULL;
CREATE UNIQUE INDEX bindings_live_unique_worktree_orch
    ON bindings(worktree_path, orchestrator_id) WHERE ended_at IS NULL;
`,
	},
	{
		name: "0005_pinned_at",
		sql: `
-- Add nullable pinned_at (RFC3339 string) to orchestrators and roles,
-- mirroring archived_at. Pin is a hera-view rail concern (a Pinned
-- section at the rail top, the 'P' key) — argus exposes no pin/SetPinned
-- REST endpoint, so pin state lives hera-side. Pin and archive are
-- mutually exclusive: the DAO Pin verbs clear archived_at when pinning,
-- and Archive clears pinned_at when archiving, mirroring argus's
-- SetPinned/SetArchived. A plain ADD COLUMN suffices — no uniqueness
-- constraint changes, so no table recreation is required.
ALTER TABLE orchestrators ADD COLUMN pinned_at TEXT;
ALTER TABLE roles ADD COLUMN pinned_at TEXT;
CREATE INDEX orchestrators_by_pinned ON orchestrators(pinned_at);
CREATE INDEX roles_by_pinned ON roles(pinned_at);
`,
	},
	{
		name: "0006_nudge_tracking",
		sql: `
-- Add nudge_count and nudged_at to the messages table for the delivery-receipt
-- re-nudge loop. nudge_count tracks how many doorbell PTY writes have been
-- emitted for this message; nudged_at is the RFC3339 timestamp of the most
-- recent nudge. Both start 0/NULL on existing rows and new rows alike.
ALTER TABLE messages ADD COLUMN nudge_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE messages ADD COLUMN nudged_at TEXT;
CREATE INDEX messages_nudge_scan ON messages(delivery_mode, read_at, nudge_count)
    WHERE delivery_mode = 'idle_submit' AND read_at IS NULL;
`,
	},
}

const initialSchema = `
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
`

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
	defer func() { _ = tx.Rollback() }()

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
