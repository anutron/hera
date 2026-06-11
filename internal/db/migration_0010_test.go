package db

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// TestMigration0010_TreeCursorsCascade is the BUG-034 migration regression. It
// reconstructs the live-DB state at schema version 9 — a coordinator role with
// a tree_read_cursors row whose role_id FK has NO ON DELETE action — proves the
// orchestrator delete is BLOCKED (FK 787) before migration 0010, applies 0010,
// and proves the same delete now cascades cleanly.
func TestMigration0010_TreeCursorsCascade(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "migration0010.sqlite")
	dsn := fmt.Sprintf(
		"file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)",
		dbPath,
	)
	sqldb, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer func() { _ = sqldb.Close() }()

	// Apply migrations 0001-0009 (indices 0-8 → schema version 9).
	for i := 0; i < 9; i++ {
		if err := applyMigration(sqldb, migrations[i], i+1); err != nil {
			t.Fatalf("apply migration %d (%s): %v", i+1, migrations[i].name, err)
		}
	}
	var version int
	if err := sqldb.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("PRAGMA user_version: %v", err)
	}
	if version != 9 {
		t.Fatalf("expected schema version 9 before 0010, got %d", version)
	}

	ctx := context.Background()
	now := "2024-01-01T00:00:00Z"

	res, err := sqldb.ExecContext(ctx,
		`INSERT INTO orchestrators (name, created_at) VALUES ('sherlock-3x', ?)`, now)
	if err != nil {
		t.Fatalf("insert orchestrator: %v", err)
	}
	orchID, _ := res.LastInsertId()

	res, err = sqldb.ExecContext(ctx,
		`INSERT INTO roles (orchestrator_id, name, kind, argus_project, prompt, created_at)
		 VALUES (?, 'coord', 'coordinator', 'sherlock', '', ?)`, orchID, now)
	if err != nil {
		t.Fatalf("insert role: %v", err)
	}
	roleID, _ := res.LastInsertId()

	// The coord consumed tree updates → it owns a cursor row. This is exactly
	// orch 28's state in the live DB.
	if _, err := sqldb.ExecContext(ctx,
		`INSERT INTO tree_read_cursors (role_id, cursor, updated_at) VALUES (?, 42, ?)`,
		roleID, now,
	); err != nil {
		t.Fatalf("insert tree cursor: %v", err)
	}

	// Pre-0010: the delete is blocked by the cursor FK. Reproduce the bug.
	if _, err := sqldb.ExecContext(ctx, `DELETE FROM orchestrators WHERE id = ?`, orchID); err == nil {
		t.Fatalf("pre-0010 delete should be blocked by tree_read_cursors FK, but it succeeded")
	} else if !strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
		t.Fatalf("pre-0010 delete: expected FK failure, got %v", err)
	}

	// Apply migration 0010.
	m10 := migrations[9]
	if m10.name != "0010_tree_cursors_cascade" {
		t.Fatalf("migrations[9] is %q, not 0010_tree_cursors_cascade — slice order changed?", m10.name)
	}
	if err := applyMigration(sqldb, m10, 10); err != nil {
		t.Fatalf("apply migration 0010: %v", err)
	}

	// The cursor row survives the rebuild with its value intact.
	var cursor int64
	if err := sqldb.QueryRowContext(ctx,
		`SELECT cursor FROM tree_read_cursors WHERE role_id = ?`, roleID,
	).Scan(&cursor); err != nil {
		t.Fatalf("cursor row missing after 0010 rebuild: %v", err)
	}
	if cursor != 42 {
		t.Fatalf("cursor value after 0010: got %d, want 42", cursor)
	}

	// Post-0010: the delete now cascades through tree_read_cursors.
	if _, err := sqldb.ExecContext(ctx, `DELETE FROM orchestrators WHERE id = ?`, orchID); err != nil {
		t.Fatalf("post-0010 delete should cascade cleanly, got: %v", err)
	}

	var remainingCursors int
	if err := sqldb.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM tree_read_cursors WHERE role_id = ?`, roleID,
	).Scan(&remainingCursors); err != nil {
		t.Fatalf("count cursors: %v", err)
	}
	if remainingCursors != 0 {
		t.Fatalf("tree_read_cursors row survived cascade: count=%d", remainingCursors)
	}

	// FK integrity is clean after the whole sequence.
	rows, err := sqldb.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var tbl, parent string
		var rowid, fkid int64
		if err := rows.Scan(&tbl, &rowid, &parent, &fkid); err != nil {
			t.Fatalf("foreign_key_check scan: %v", err)
		}
		t.Errorf("FK violation after 0010: %s rowid=%d → %s fkid=%d", tbl, rowid, parent, fkid)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("foreign_key_check iteration: %v", err)
	}
}
