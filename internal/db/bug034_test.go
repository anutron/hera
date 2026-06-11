package db

import (
	"context"
	"errors"
	"testing"
)

// TestBug034_DeleteOrchWithTreeCursor is the BUG-034 regression. A coordinator
// that has consumed tree updates owns a tree_read_cursors row. Before the fix,
// DeleteOrchestrator failed with "FOREIGN KEY constraint failed (787)" because
// tree_read_cursors.role_id had no ON DELETE action and blocked the role delete
// cascaded from the orchestrator delete. Migration 0010 (cascade) and the
// transactional cursor-clear in OrchestratorsDAO.Delete both fix it.
func TestBug034_DeleteOrchWithTreeCursor(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	orch, role, _ := bug034Fixture(t, d, "cursor-probe")
	if err := d.TreeCursors.UpsertTreeCursor(ctx, role.ID, 42); err != nil {
		t.Fatalf("upsert tree cursor: %v", err)
	}

	if err := d.Orchestrators.Delete(ctx, orch.ID); err != nil {
		t.Fatalf("Delete with tree cursor present should succeed, got: %v", err)
	}

	// Orchestrator, its role, its cursor, and its bindings are all gone.
	assertGone(t, d, "orchestrators", "id", orch.ID)
	assertGone(t, d, "roles", "id", role.ID)
	assertGone(t, d, "tree_read_cursors", "role_id", role.ID)
	assertCount(t, d, "SELECT COUNT(*) FROM bindings WHERE orchestrator_id = ?", orch.ID, 0)
}

// TestBug034_MigrationCascadeFires confirms the durable fix: after migration
// 0010 the tree_read_cursors ON DELETE CASCADE fires on a raw orchestrator
// delete, independent of the DAO's explicit clear.
func TestBug034_MigrationCascadeFires(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	raw := d.Raw()

	orch, role, _ := bug034Fixture(t, d, "raw-cascade")
	if err := d.TreeCursors.UpsertTreeCursor(ctx, role.ID, 7); err != nil {
		t.Fatalf("upsert tree cursor: %v", err)
	}

	if _, err := raw.ExecContext(ctx, "DELETE FROM orchestrators WHERE id = ?", orch.ID); err != nil {
		t.Fatalf("raw orchestrator delete should cascade through tree_read_cursors after 0010, got: %v", err)
	}
	assertGone(t, d, "tree_read_cursors", "role_id", role.ID)
}

// TestBug034_DeleteNotFound preserves the ErrNotFound contract through the new
// transactional Delete.
func TestBug034_DeleteNotFound(t *testing.T) {
	d := openTestDB(t)
	if err := d.Orchestrators.Delete(context.Background(), 999999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete of missing orchestrator: want ErrNotFound, got %v", err)
	}
}

func bug034Fixture(t *testing.T, d *DB, project string) (*Orchestrator, *Role, *Binding) {
	t.Helper()
	ctx := context.Background()
	orch, err := d.Orchestrators.Create(ctx, "sherlock-3x-"+project)
	if err != nil {
		t.Fatalf("create orch: %v", err)
	}
	role, err := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID,
		Name:           "coord",
		Kind:           KindCoordinator,
		ArgusProject:   project,
	})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}
	bnd, err := d.Bindings.Create(ctx, CreateBindingInput{
		RoleID:         role.ID,
		OrchestratorID: orch.ID,
		ArgusTaskID:    "task-" + project,
		WorktreePath:   "/tmp/wt-" + project,
	})
	if err != nil {
		t.Fatalf("create binding: %v", err)
	}
	return orch, role, bnd
}

func assertGone(t *testing.T, d *DB, table, col string, id int64) {
	t.Helper()
	assertCount(t, d, "SELECT COUNT(*) FROM "+table+" WHERE "+col+" = ?", id, 0)
}

func assertCount(t *testing.T, d *DB, query string, arg int64, want int) {
	t.Helper()
	var got int
	if err := d.Raw().QueryRowContext(context.Background(), query, arg).Scan(&got); err != nil {
		t.Fatalf("count query %q: %v", query, err)
	}
	if got != want {
		t.Fatalf("count for %q (arg=%d): got %d, want %d", query, arg, got, want)
	}
}
