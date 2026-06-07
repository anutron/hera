package db

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// openTestDB creates a fresh DB file under t.TempDir() and returns it. The
// caller does not need to remember to Close; t.Cleanup wires it.
func openTestDB(t *testing.T) *DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")
	d, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func TestOpen_CreatesFileAndMigrates(t *testing.T) {
	d := openTestDB(t)
	v, err := d.SchemaVersion()
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if v != len(migrations) {
		t.Fatalf("SchemaVersion: got %d, want %d", v, len(migrations))
	}
}

func TestOpen_IsIdempotent_ReopenWithExistingFile(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")
	d1, err := Open(dbPath)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := d1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	d2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer func() { _ = d2.Close() }()

	v, err := d2.SchemaVersion()
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if v != len(migrations) {
		t.Fatalf("SchemaVersion after reopen: got %d, want %d", v, len(migrations))
	}
}

func TestOrchestrators_CreateAndGet(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)

	orch, err := d.Orchestrators.Create(ctx, "foo")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if orch.Name != "foo" || orch.ID == 0 {
		t.Fatalf("Create returned %+v", orch)
	}

	got, err := d.Orchestrators.GetByName(ctx, "foo")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if got.ID != orch.ID {
		t.Fatalf("GetByName returned id %d, want %d", got.ID, orch.ID)
	}

	gotByID, err := d.Orchestrators.GetByID(ctx, orch.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if gotByID.Name != "foo" {
		t.Fatalf("GetByID name = %q", gotByID.Name)
	}
}

func TestOrchestrators_CreateIsIdempotent(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)

	o1, err := d.Orchestrators.Create(ctx, "foo")
	if err != nil {
		t.Fatalf("first Create: %v", err)
	}
	o2, err := d.Orchestrators.Create(ctx, "foo")
	if err != nil {
		t.Fatalf("second Create: %v", err)
	}
	if o1.ID != o2.ID {
		t.Fatalf("idempotent Create produced different ids: %d vs %d", o1.ID, o2.ID)
	}
}

func TestOrchestrators_GetByName_NotFound(t *testing.T) {
	d := openTestDB(t)
	_, err := d.Orchestrators.GetByName(context.Background(), "missing")
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestRoles_CreateAndGet(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")

	role, err := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID,
		Name:           "coordinator",
		Kind:           KindCoordinator,
		ArgusProject:   "argus",
		Prompt:         "build the substrate",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if role.Kind != KindCoordinator {
		t.Fatalf("kind = %s", role.Kind)
	}
	if role.Prompt != "build the substrate" {
		t.Fatalf("prompt = %q", role.Prompt)
	}
}

func TestRoles_CreateConflictingKindFails(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")

	if _, err := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coordinator", Kind: KindCoordinator,
		ArgusProject: "argus",
	}); err != nil {
		t.Fatalf("first Create: %v", err)
	}

	_, err := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coordinator", Kind: KindWorker,
		ArgusProject: "argus",
	})
	if err == nil {
		t.Fatalf("expected conflict error, got nil")
	}
	if !strings.Contains(err.Error(), "different") && !strings.Contains(err.Error(), "exists with kind") {
		t.Fatalf("unexpected error wording: %v", err)
	}
}

func TestRoles_CreateSameKindIsIdempotent(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")

	r1, err := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coordinator", Kind: KindCoordinator,
		ArgusProject: "argus",
	})
	if err != nil {
		t.Fatalf("first Create: %v", err)
	}
	r2, err := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coordinator", Kind: KindCoordinator,
		ArgusProject: "argus",
	})
	if err != nil {
		t.Fatalf("second Create: %v", err)
	}
	if r1.ID != r2.ID {
		t.Fatalf("ids differ: %d vs %d", r1.ID, r2.ID)
	}
}

func TestBindings_LifecycleStartAndEnd(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")
	role, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coordinator", Kind: KindCoordinator,
		ArgusProject: "argus",
	})

	bnd, err := d.Bindings.Create(ctx, CreateBindingInput{
		RoleID: role.ID, ArgusTaskID: "task-1", WorktreePath: "/tmp/wt",
	})
	if err != nil {
		t.Fatalf("Bindings.Create: %v", err)
	}
	if bnd.EndedAt != nil {
		t.Fatalf("new binding should have ended_at NULL")
	}

	live, err := d.Bindings.GetLiveByTaskID(ctx, "task-1")
	if err != nil {
		t.Fatalf("GetLiveByTaskID: %v", err)
	}
	if live.ID != bnd.ID {
		t.Fatalf("live binding id mismatch")
	}

	if err := d.Bindings.End(ctx, bnd.ID, "archived"); err != nil {
		t.Fatalf("End: %v", err)
	}

	_, err = d.Bindings.GetLiveByTaskID(ctx, "task-1")
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound after End, got %v", err)
	}
}

func TestBindings_EndIsIdempotentlySafe(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")
	role, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "x", Kind: KindWorker, ArgusProject: "p",
	})
	bnd, _ := d.Bindings.Create(ctx, CreateBindingInput{
		RoleID: role.ID, ArgusTaskID: "task-1", WorktreePath: "/tmp/wt",
	})

	if err := d.Bindings.End(ctx, bnd.ID, "archived"); err != nil {
		t.Fatalf("first End: %v", err)
	}
	err := d.Bindings.End(ctx, bnd.ID, "archived")
	if err != ErrNotFound {
		t.Fatalf("double-end should return ErrNotFound, got %v", err)
	}
}

func TestBindings_GetLiveByWorktree(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")
	role, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "x", Kind: KindWorker, ArgusProject: "p",
	})
	_, _ = d.Bindings.Create(ctx, CreateBindingInput{
		RoleID: role.ID, ArgusTaskID: "T-1", WorktreePath: "/tmp/specific-worktree",
	})

	got, err := d.Bindings.GetLiveByWorktree(ctx, "/tmp/specific-worktree")
	if err != nil {
		t.Fatalf("GetLiveByWorktree: %v", err)
	}
	if got.ArgusTaskID != "T-1" {
		t.Fatalf("unexpected binding: %+v", got)
	}
}

func TestBindings_ListLiveExcludesEnded(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")
	r1, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coord", Kind: KindCoordinator, ArgusProject: "foo",
	})
	r2, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w1", Kind: KindWorker, ArgusProject: "foo",
	})
	r3, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w2", Kind: KindWorker, ArgusProject: "foo",
	})

	live, _ := d.Bindings.Create(ctx, CreateBindingInput{RoleID: r1.ID, ArgusTaskID: "live-1", WorktreePath: "/tmp/1"})
	_, _ = d.Bindings.Create(ctx, CreateBindingInput{RoleID: r2.ID, ArgusTaskID: "live-2", WorktreePath: "/tmp/2"})
	ended, _ := d.Bindings.Create(ctx, CreateBindingInput{RoleID: r3.ID, ArgusTaskID: "ended-1", WorktreePath: "/tmp/3"})
	if err := d.Bindings.End(ctx, ended.ID, "test"); err != nil {
		t.Fatalf("End: %v", err)
	}
	_ = live // keep the linter happy; we care about the visible side-effects below

	got, err := d.Bindings.ListLive(ctx)
	if err != nil {
		t.Fatalf("ListLive: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListLive returned %d rows, want 2 (excluding ended)", len(got))
	}
	seen := map[string]bool{}
	for _, b := range got {
		seen[b.ArgusTaskID] = true
		if b.EndedAt != nil {
			t.Errorf("ListLive returned ended binding %s", b.ArgusTaskID)
		}
	}
	if !seen["live-1"] || !seen["live-2"] {
		t.Fatalf("ListLive missing expected ids: %+v", seen)
	}
	if seen["ended-1"] {
		t.Fatalf("ListLive included ended binding")
	}
}

func TestMessages_CreateInboxMarkRead(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")
	coord, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coordinator", Kind: KindCoordinator,
		ArgusProject: "argus",
	})
	worker, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "f2-impl", Kind: KindWorker,
		ArgusProject: "argus",
	})

	msg, err := d.Messages.Create(ctx, CreateMessageInput{
		FromRoleID: coord.ID,
		ToRoleID:   worker.ID,
		Body:       "please review",
	})
	if err != nil {
		t.Fatalf("Messages.Create: %v", err)
	}
	if msg.DeliveryMode != DeliveryPending {
		t.Fatalf("delivery_mode = %s, want %s", msg.DeliveryMode, DeliveryPending)
	}

	inbox, err := d.Messages.UnreadForRole(ctx, worker.ID)
	if err != nil {
		t.Fatalf("UnreadForRole: %v", err)
	}
	if len(inbox) != 1 || inbox[0].ID != msg.ID {
		t.Fatalf("inbox unexpected: %+v", inbox)
	}

	n, err := d.Messages.MarkRead(ctx, worker.ID, []int64{msg.ID})
	if err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	if n != 1 {
		t.Fatalf("MarkRead n = %d, want 1", n)
	}

	inboxAfter, err := d.Messages.UnreadForRole(ctx, worker.ID)
	if err != nil {
		t.Fatalf("UnreadForRole 2: %v", err)
	}
	if len(inboxAfter) != 0 {
		t.Fatalf("inbox after mark-read should be empty, got %d", len(inboxAfter))
	}
}

func TestMessages_MarkReadDoesNotTouchOtherRolesMessages(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")
	coord, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coordinator", Kind: KindCoordinator,
		ArgusProject: "argus",
	})
	w1, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w1", Kind: KindWorker, ArgusProject: "argus",
	})
	w2, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w2", Kind: KindWorker, ArgusProject: "argus",
	})

	msg, _ := d.Messages.Create(ctx, CreateMessageInput{
		FromRoleID: coord.ID, ToRoleID: w1.ID, Body: "for w1",
	})

	// w2 tries to mark w1's message read.
	n, err := d.Messages.MarkRead(ctx, w2.ID, []int64{msg.ID})
	if err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	if n != 0 {
		t.Fatalf("cross-role mark-read updated %d rows, want 0", n)
	}

	// Confirm w1's inbox still shows the message unread.
	inbox, _ := d.Messages.UnreadForRole(ctx, w1.ID)
	if len(inbox) != 1 {
		t.Fatalf("w1 inbox = %d, want 1", len(inbox))
	}
}

func TestMessages_CreateRequiresToRoleID(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")
	coord, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "c", Kind: KindCoordinator, ArgusProject: "p",
	})
	_, err := d.Messages.Create(ctx, CreateMessageInput{
		FromRoleID: coord.ID,
		Body:       "no recipient",
	})
	if err == nil {
		t.Fatalf("expected error when ToRoleID is zero")
	}
}

func TestMessages_SetDelivered(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")
	coord, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "c", Kind: KindCoordinator, ArgusProject: "p",
	})
	w, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w", Kind: KindWorker, ArgusProject: "p",
	})
	msg, _ := d.Messages.Create(ctx, CreateMessageInput{
		FromRoleID: coord.ID, ToRoleID: w.ID, Body: "x",
	})

	if err := d.Messages.SetDelivered(ctx, msg.ID, DeliveryIdleSubmit); err != nil {
		t.Fatalf("SetDelivered: %v", err)
	}
	got, err := d.Messages.GetByID(ctx, msg.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.DeliveryMode != DeliveryIdleSubmit {
		t.Fatalf("delivery_mode = %s, want %s", got.DeliveryMode, DeliveryIdleSubmit)
	}
	if got.DeliveredAt == nil {
		t.Fatalf("delivered_at not set")
	}
}

func TestRoleStatus_UpsertAndGet(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")
	role, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "x", Kind: KindWorker, ArgusProject: "p",
	})

	if err := d.RoleStatus.Upsert(ctx, role.ID, StatusWorking); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	rs, err := d.RoleStatus.Get(ctx, role.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if rs.Status != StatusWorking {
		t.Fatalf("status = %s", rs.Status)
	}

	if err := d.RoleStatus.Upsert(ctx, role.ID, StatusBlocked); err != nil {
		t.Fatalf("second Upsert: %v", err)
	}
	rs, _ = d.RoleStatus.Get(ctx, role.ID)
	if rs.Status != StatusBlocked {
		t.Fatalf("status after second upsert = %s", rs.Status)
	}
}

func TestEventCursor_GetSetMonotonic(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)

	got, err := d.EventCursor.Get(ctx)
	if err != nil {
		t.Fatalf("Get on fresh DB: %v", err)
	}
	if got != 0 {
		t.Fatalf("fresh cursor = %d, want 0", got)
	}

	if err := d.EventCursor.Set(ctx, 10); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, _ = d.EventCursor.Get(ctx)
	if got != 10 {
		t.Fatalf("cursor = %d, want 10", got)
	}

	// Setting a smaller cursor is silently ignored (monotonic guard).
	if err := d.EventCursor.Set(ctx, 5); err != nil {
		t.Fatalf("Set smaller: %v", err)
	}
	got, _ = d.EventCursor.Get(ctx)
	if got != 10 {
		t.Fatalf("cursor regressed: %d", got)
	}

	// Setting a larger cursor advances.
	if err := d.EventCursor.Set(ctx, 42); err != nil {
		t.Fatalf("Set larger: %v", err)
	}
	got, _ = d.EventCursor.Get(ctx)
	if got != 42 {
		t.Fatalf("cursor = %d, want 42", got)
	}
}

func TestConfig_GetSet(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)

	_, err := d.Config.Get(ctx, "missing")
	if err != ErrNotFound {
		t.Fatalf("Get missing: got %v, want ErrNotFound", err)
	}

	if err := d.Config.Set(ctx, "foo", "bar"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	v, err := d.Config.Get(ctx, "foo")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if v != "bar" {
		t.Fatalf("Get returned %q, want %q", v, "bar")
	}

	if err := d.Config.Set(ctx, "foo", "baz"); err != nil {
		t.Fatalf("Set upsert: %v", err)
	}
	v, _ = d.Config.Get(ctx, "foo")
	if v != "baz" {
		t.Fatalf("upsert failed: %q", v)
	}
}
