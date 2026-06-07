package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// TestMigration0007_BindingsSurviveRoleTableRebuild is the regression test for
// BUG-044. Migration 0007 rebuilds the roles table (SQLite column-drop
// workaround: create-new → copy → DROP old → rename). With foreign_keys=ON,
// DROP TABLE roles fires ON DELETE CASCADE on bindings.role_id, hard-deleting
// every binding. This test:
//
//  1. Builds a DB manually up to schema version 6 (all migrations except 0007).
//  2. Seeds one orchestrator, one role, and one binding.
//  3. Applies migration 0007.
//  4. Asserts the binding still exists and still references the original
//     (preserved, not re-autoincremented) role and orchestrator IDs.
//  5. Asserts the migrated prompt column contains the old mission value.
func TestMigration0007_BindingsSurviveRoleTableRebuild(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "migration0007.sqlite")
	dsn := fmt.Sprintf(
		"file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)",
		dbPath,
	)
	sqldb, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer func() { _ = sqldb.Close() }()

	// Apply migrations 0001-0006 (indices 0-5, resulting in schema version 6).
	for i := 0; i < 6; i++ {
		m := migrations[i]
		if err := applyMigration(sqldb, m, i+1); err != nil {
			t.Fatalf("apply migration %d (%s): %v", i+1, m.name, err)
		}
	}

	// Verify schema version is 6 before seeding.
	var version int
	if err := sqldb.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("PRAGMA user_version: %v", err)
	}
	if version != 6 {
		t.Fatalf("expected schema version 6, got %d", version)
	}

	ctx := context.Background()
	now := "2024-01-01T00:00:00Z"

	// Seed an orchestrator.
	res, err := sqldb.ExecContext(ctx,
		`INSERT INTO orchestrators (name, created_at) VALUES ('test-orch', ?)`, now)
	if err != nil {
		t.Fatalf("insert orchestrator: %v", err)
	}
	orchID, _ := res.LastInsertId()

	// Seed a role using the pre-0007 schema (mission + constraints, not prompt).
	res, err = sqldb.ExecContext(ctx,
		`INSERT INTO roles (orchestrator_id, name, kind, argus_project, mission, constraints, created_at)
		 VALUES (?, 'coord', 'coordinator', 'proj', 'do the thing', '', ?)`,
		orchID, now,
	)
	if err != nil {
		t.Fatalf("insert role: %v", err)
	}
	roleID, _ := res.LastInsertId()

	// Seed a binding (using post-0004 schema: has orchestrator_id column).
	res, err = sqldb.ExecContext(ctx,
		`INSERT INTO bindings (role_id, orchestrator_id, argus_task_id, worktree_path, started_at)
		 VALUES (?, ?, 'task-1', '/tmp/wt', ?)`,
		roleID, orchID, now,
	)
	if err != nil {
		t.Fatalf("insert binding: %v", err)
	}
	bindingID, _ := res.LastInsertId()

	// Confirm pre-migration state: one binding exists.
	var preBndCount int
	if err := sqldb.QueryRowContext(ctx, `SELECT COUNT(*) FROM bindings`).Scan(&preBndCount); err != nil {
		t.Fatalf("pre-migration binding count: %v", err)
	}
	if preBndCount != 1 {
		t.Fatalf("pre-migration: expected 1 binding, got %d", preBndCount)
	}

	// Apply migration 0007 (index 6, sets version 7).
	m7 := migrations[6]
	if m7.name != "0007_mission_to_prompt" {
		t.Fatalf("migrations[6] is %q, not 0007_mission_to_prompt — slice order changed?", m7.name)
	}
	if err := applyMigration(sqldb, m7, 7); err != nil {
		t.Fatalf("apply migration 0007: %v", err)
	}

	// --- Binding survival ---
	var postBndCount int
	if err := sqldb.QueryRowContext(ctx, `SELECT COUNT(*) FROM bindings`).Scan(&postBndCount); err != nil {
		t.Fatalf("post-migration binding count: %v", err)
	}
	if postBndCount != 1 {
		t.Fatalf("BUG-044: migration 0007 cascade-deleted bindings — got %d rows, want 1", postBndCount)
	}

	// --- FK ID preservation: binding still points at the same role/orch IDs ---
	var gotRoleID, gotOrchID int64
	if err := sqldb.QueryRowContext(ctx,
		`SELECT role_id, orchestrator_id FROM bindings WHERE id = ?`, bindingID,
	).Scan(&gotRoleID, &gotOrchID); err != nil {
		t.Fatalf("query binding FKs: %v", err)
	}
	if gotRoleID != roleID {
		t.Fatalf("binding.role_id after migration: got %d, want %d", gotRoleID, roleID)
	}
	if gotOrchID != orchID {
		t.Fatalf("binding.orchestrator_id after migration: got %d, want %d", gotOrchID, orchID)
	}

	// --- Role ID preservation: rebuilt table must keep the original id ---
	var gotRoleRowID int64
	if err := sqldb.QueryRowContext(ctx,
		`SELECT id FROM roles WHERE id = ?`, roleID,
	).Scan(&gotRoleRowID); err != nil {
		t.Fatalf("roles row missing after migration (id=%d): %v", roleID, err)
	}
	if gotRoleRowID != roleID {
		t.Fatalf("role id shifted after migration: got %d, want %d", gotRoleRowID, roleID)
	}

	// --- Column migration: mission → prompt value preserved ---
	var prompt string
	if err := sqldb.QueryRowContext(ctx,
		`SELECT prompt FROM roles WHERE id = ?`, roleID,
	).Scan(&prompt); err != nil {
		t.Fatalf("prompt column missing after migration: %v", err)
	}
	if prompt != "do the thing" {
		t.Fatalf("prompt value after migration: got %q, want %q", prompt, "do the thing")
	}

	// --- FK integrity check: no orphaned rows anywhere in the schema ---
	rows, err := sqldb.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var fkViolations []string
	for rows.Next() {
		var tbl, parent string
		var rowid, fkid int64
		if err := rows.Scan(&tbl, &rowid, &parent, &fkid); err != nil {
			t.Fatalf("foreign_key_check scan: %v", err)
		}
		fkViolations = append(fkViolations, fmt.Sprintf("%s rowid=%d → %s fkid=%d", tbl, rowid, parent, fkid))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("foreign_key_check iteration: %v", err)
	}
	if len(fkViolations) > 0 {
		t.Fatalf("FK violations after migration 0007: %v", fkViolations)
	}
}

// TestMigration_BackupCreatedForPendingMigrations verifies that Open() creates
// a pre-migration backup file when there are pending migrations, and that the
// backup is a valid SQLite file at the pre-migration schema version.
func TestMigration_BackupCreatedForPendingMigrations(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.sqlite")

	migrationCount := len(migrations)

	// Build a DB at one-before-latest schema version by applying all
	// migrations except the last one directly (bypassing Open so no backup
	// is created yet).
	dsn := fmt.Sprintf(
		"file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)",
		dbPath,
	)
	rawDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	for i := 0; i < migrationCount-1; i++ {
		if err := applyMigration(rawDB, migrations[i], i+1); err != nil {
			_ = rawDB.Close()
			t.Fatalf("apply migration %d (%s): %v", i+1, migrations[i].name, err)
		}
	}
	_ = rawDB.Close()

	// Open via the public Open(). It should detect one pending migration,
	// create a backup, then apply the migration.
	d, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = d.Close() }()

	// The backup file must exist.
	backupPath := fmt.Sprintf("%s.pre-%d.bak", dbPath, migrationCount)
	fi, err := os.Stat(backupPath)
	if os.IsNotExist(err) {
		t.Fatalf("backup file not created: %s", backupPath)
	}
	if err != nil {
		t.Fatalf("stat backup: %v", err)
	}
	if fi.Size() == 0 {
		t.Fatalf("backup file is empty: %s", backupPath)
	}

	// The backup must be a valid SQLite DB at the pre-migration schema version.
	backupDB, err := sql.Open("sqlite", backupPath)
	if err != nil {
		t.Fatalf("open backup DB: %v", err)
	}
	defer func() { _ = backupDB.Close() }()

	var backupVersion int
	if err := backupDB.QueryRow("PRAGMA user_version").Scan(&backupVersion); err != nil {
		t.Fatalf("backup user_version: %v", err)
	}
	if backupVersion != migrationCount-1 {
		t.Fatalf("backup schema version: got %d, want %d (pre-migration)", backupVersion, migrationCount-1)
	}

	// The live DB must be at the latest version.
	liveVersion, err := d.SchemaVersion()
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if liveVersion != migrationCount {
		t.Fatalf("live DB version after Open: got %d, want %d", liveVersion, migrationCount)
	}
}

// TestMigration_NoBackupWhenFullyMigrated verifies that reopening a DB that is
// already at the latest schema version does not create any additional backup.
// (The initial Open from version 0 legitimately creates a backup; subsequent
// opens at the latest version must not add more.)
func TestMigration_NoBackupWhenFullyMigrated(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.sqlite")

	// First Open: creates the DB and runs all migrations. This may create a
	// backup (version 0 → latest has pending migrations).
	d1, err := Open(dbPath)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	_ = d1.Close()

	// Snapshot the backup files that exist after the first open.
	before, err := filepath.Glob(dbPath + ".pre-*.bak")
	if err != nil {
		t.Fatalf("Glob before: %v", err)
	}

	// Second Open: DB is already at latest version — no pending migrations.
	d2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer func() { _ = d2.Close() }()

	// No new backup files should have been created by the second open.
	after, err := filepath.Glob(dbPath + ".pre-*.bak")
	if err != nil {
		t.Fatalf("Glob after: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("second Open created unexpected backup files: before=%v after=%v", before, after)
	}
}

// TestMigration_PruneBackupsKeepsLatest verifies that pruneBackups removes the
// oldest files and retains exactly keepBackups of the most recent ones.
func TestMigration_PruneBackupsKeepsLatest(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.sqlite")

	total := keepBackups + 3
	for i := 1; i <= total; i++ {
		path := fmt.Sprintf("%s.pre-%d.bak", dbPath, i)
		if err := os.WriteFile(path, []byte("fake"), 0o644); err != nil {
			t.Fatalf("create fake backup %d: %v", i, err)
		}
	}

	if err := pruneBackups(dbPath, keepBackups); err != nil {
		t.Fatalf("pruneBackups: %v", err)
	}

	remaining, err := filepath.Glob(dbPath + ".pre-*.bak")
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if len(remaining) != keepBackups {
		t.Fatalf("after prune: got %d backup files, want %d", len(remaining), keepBackups)
	}
}
