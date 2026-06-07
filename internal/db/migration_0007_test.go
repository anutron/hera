package db

import (
	"context"
	"database/sql"
	"fmt"
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
