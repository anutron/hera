package events

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/anutron/hera/internal/argus"
	"github.com/anutron/hera/internal/db"
)

func setupResync(t *testing.T) (*adoptTestEnv, *ResyncHandler) {
	t.Helper()
	e := setupAdopt(t) // reuses the fakeArgus + DB scaffolding
	h := NewResyncHandler(e.client, e.db, nil)
	return e, h
}

func resyncEvent(id int64) argus.Event {
	payload, _ := json.Marshal(ResyncPayload{Reason: "cursor_older_than_ring", Cursor: 1, Oldest: 100})
	return argus.Event{ID: id, Type: TypeResync, Payload: payload}
}

func TestResync_IgnoresNonResyncEvents(t *testing.T) {
	ctx := context.Background()
	e, h := setupResync(t)
	// Seed a live binding pointing at a task that DOES NOT exist in argus.
	orch, _ := e.db.Orchestrators.Create(ctx, "foo")
	role, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w", Kind: db.KindWorker, ArgusProject: "p",
	})
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: role.ID, ArgusTaskID: "gone-task", WorktreePath: "/tmp/gone",
	})

	// Non-resync event – handler should do nothing.
	h.HandleEvent(ctx, argus.Event{ID: 99, Type: TypeTaskCreated, TaskID: "gone-task"})

	// Binding should still be live.
	if _, err := e.db.Bindings.GetLiveByTaskID(ctx, "gone-task"); err != nil {
		t.Fatalf("expected binding to still be live; got %v", err)
	}
}

func TestResync_EndsBindingsForVanishedTasks(t *testing.T) {
	ctx := context.Background()
	e, h := setupResync(t)

	// Seed: one binding for a task that EXISTS in argus, one for a task that doesn't.
	orch, _ := e.db.Orchestrators.Create(ctx, "foo")
	livRole, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "live", Kind: db.KindWorker, ArgusProject: "p",
	})
	deadRole, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "dead", Kind: db.KindWorker, ArgusProject: "p",
	})
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: livRole.ID, ArgusTaskID: "still-here", WorktreePath: "/tmp/live",
	})
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: deadRole.ID, ArgusTaskID: "vanished", WorktreePath: "/tmp/dead",
	})
	// Only "still-here" is in argus's task list.
	e.fake.addTask(argus.Task{ID: "still-here", Project: "p", WorktreePath: "/tmp/live"})

	// Fire the resync event.
	h.HandleEvent(ctx, resyncEvent(123))

	// "still-here" binding should still be live.
	if _, err := e.db.Bindings.GetLiveByTaskID(ctx, "still-here"); err != nil {
		t.Fatalf("expected live binding for still-here to remain; got %v", err)
	}

	// "vanished" binding should be ended.
	if _, err := e.db.Bindings.GetLiveByTaskID(ctx, "vanished"); err == nil {
		t.Fatalf("expected vanished binding to be ended after resync")
	}

	// Confirm end_reason.
	allBindings, err := e.db.Bindings.ListByRole(ctx, deadRole.ID)
	if err != nil {
		t.Fatalf("ListByRole: %v", err)
	}
	if len(allBindings) != 1 {
		t.Fatalf("expected 1 binding for dead role, got %d", len(allBindings))
	}
	if allBindings[0].EndReason != "resync_missing" {
		t.Fatalf("expected end_reason=resync_missing, got %q", allBindings[0].EndReason)
	}
}

func TestResync_NoOpWhenAllTasksAlive(t *testing.T) {
	ctx := context.Background()
	e, h := setupResync(t)

	orch, _ := e.db.Orchestrators.Create(ctx, "foo")
	role, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w", Kind: db.KindWorker, ArgusProject: "p",
	})
	bnd, _ := e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: role.ID, ArgusTaskID: "alive", WorktreePath: "/tmp/alive",
	})
	e.fake.addTask(argus.Task{ID: "alive", Project: "p", WorktreePath: "/tmp/alive"})

	h.HandleEvent(ctx, resyncEvent(456))

	// Binding should still be the same row, still live.
	got, err := e.db.Bindings.GetLiveByTaskID(ctx, "alive")
	if err != nil {
		t.Fatalf("GetLiveByTaskID: %v", err)
	}
	if got.ID != bnd.ID {
		t.Fatalf("binding id changed unexpectedly")
	}
}

// silence path/filepath import keepers
var _ = filepath.Join
