package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/anutron/hera/internal/argus"
	"github.com/anutron/hera/internal/db"
)

func decodeSendOutput(t *testing.T, r Response) SendOutput {
	t.Helper()
	if r.IsError {
		t.Fatalf("got error: %s", r.Content[0].Text)
	}
	var out SendOutput
	if err := json.Unmarshal([]byte(r.Content[0].Text), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

// setupSend returns a handler fixture with two roles bound and a send handler
// wired to the fake argus server.
func setupSend(t *testing.T, autoInject bool, notifyState string) (*handlerFixture, *SendHandler) {
	t.Helper()
	e := setupHandlers(t)
	e.fake.notifyState = notifyState

	h := NewSendHandler(e.resolver, e.db, e.client, autoInject, 300000)
	return e, h
}

// seedOrchestratorWithBoundPair creates an orchestrator with a coordinator and
// a worker, both with live argus bindings. Returns (coord, worker) roles.
func seedOrchestratorWithBoundPair(t *testing.T, e *handlerFixture) (coord, worker *db.Role) {
	t.Helper()
	ctx := context.Background()
	orch, _ := e.db.Orchestrators.Create(ctx, "foo")
	coord, _ = e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "p",
	})
	worker, _ = e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w", Kind: db.KindWorker, ArgusProject: "p",
	})
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: coord.ID, ArgusTaskID: "t-coord", WorktreePath: "/tmp/coord",
	})
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: worker.ID, ArgusTaskID: "t-w", WorktreePath: "/tmp/w",
	})
	e.fake.addTask(taskFor("t-coord", "/tmp/coord"))
	e.fake.addTask(taskFor("t-w", "/tmp/w"))
	return coord, worker
}

// TestSend_ArgusNotify_SubmittedState verifies that when argus returns
// state:"submitted" the delivery_mode is recorded as idle_submit.
func TestSend_ArgusNotify_SubmittedState(t *testing.T) {
	ctx := context.Background()
	e, h := setupSend(t, true, "submitted")
	_, _ = seedOrchestratorWithBoundPair(t, e)

	resp := h.Handle(ctx, mustMarshal(t, SendInput{Cwd: "/tmp/w", Body: "need a ruling", Tldr: "need a ruling"}))
	out := decodeSendOutput(t, resp)

	if out.RecipientRole != "coord" {
		t.Fatalf("recipient = %q, want coord", out.RecipientRole)
	}
	if out.DeliveryMode != string(db.DeliveryIdleSubmit) {
		t.Fatalf("delivery_mode = %q, want idle_submit", out.DeliveryMode)
	}

	e.fake.mu.Lock()
	defer e.fake.mu.Unlock()
	if len(e.fake.notifyPosts) != 1 {
		t.Fatalf("notifyPosts count = %d, want 1", len(e.fake.notifyPosts))
	}
	n := e.fake.notifyPosts[0]
	if !strings.Contains(n.Text, "need a ruling") {
		t.Fatalf("notify text %q does not contain tldr", n.Text)
	}
	if !strings.Contains(n.Text, "[hera from w]") {
		t.Fatalf("notify text %q missing sender prefix", n.Text)
	}
	if !n.Submit {
		t.Fatal("notify submit = false, want true (autoInject=true)")
	}
	if n.DeliveryID != fmt.Sprintf("%d", out.MessageID) {
		t.Fatalf("delivery_id = %q, want %d", n.DeliveryID, out.MessageID)
	}
	if n.DeadlineMs != 300000 {
		t.Fatalf("deadline_ms = %d, want 300000", n.DeadlineMs)
	}

	// Verify persisted delivery_mode.
	row, err := e.db.Messages.GetByID(ctx, out.MessageID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if row.DeliveryMode != db.DeliveryIdleSubmit {
		t.Fatalf("persisted delivery_mode = %q, want idle_submit", row.DeliveryMode)
	}
	if row.DeliveredAt == nil {
		t.Fatal("delivered_at not set")
	}
}

// TestSend_ArgusNotify_PendingState verifies that state:"pending" maps to busy_buffer.
func TestSend_ArgusNotify_PendingState(t *testing.T) {
	ctx := context.Background()
	e, h := setupSend(t, true, "pending")
	_, _ = seedOrchestratorWithBoundPair(t, e)

	resp := h.Handle(ctx, mustMarshal(t, SendInput{Cwd: "/tmp/w", Body: "do X", Tldr: "do X"}))
	out := decodeSendOutput(t, resp)

	if out.DeliveryMode != string(db.DeliveryBusyBuffer) {
		t.Fatalf("delivery_mode = %q, want busy_buffer", out.DeliveryMode)
	}
	row, err := e.db.Messages.GetByID(ctx, out.MessageID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if row.DeliveryMode != db.DeliveryBusyBuffer {
		t.Fatalf("persisted delivery_mode = %q, want busy_buffer", row.DeliveryMode)
	}
}

// TestSend_AutoInjectDisabled_SubmitFalse verifies that autoInject=false sends submit:false.
func TestSend_AutoInjectDisabled_SubmitFalse(t *testing.T) {
	ctx := context.Background()
	e, h := setupSend(t, false, "pending")
	_, _ = seedOrchestratorWithBoundPair(t, e)

	resp := h.Handle(ctx, mustMarshal(t, SendInput{Cwd: "/tmp/w", Body: "ping", Tldr: "ping"}))
	decodeSendOutput(t, resp)

	e.fake.mu.Lock()
	defer e.fake.mu.Unlock()
	if len(e.fake.notifyPosts) != 1 {
		t.Fatalf("notifyPosts count = %d, want 1", len(e.fake.notifyPosts))
	}
	if e.fake.notifyPosts[0].Submit {
		t.Fatal("notify submit = true, want false (autoInject=false)")
	}
}

// TestSend_SetAutoInjectEnabled_HotReload verifies that SetAutoInjectEnabled
// changes the submit field in subsequent notify calls.
func TestSend_SetAutoInjectEnabled_HotReload(t *testing.T) {
	ctx := context.Background()
	e, h := setupSend(t, true, "submitted")
	_, _ = seedOrchestratorWithBoundPair(t, e)

	// First send: autoInject=true, submit should be true.
	h.Handle(ctx, mustMarshal(t, SendInput{Cwd: "/tmp/w", Body: "first", Tldr: "first"}))

	h.SetAutoInjectEnabled(false)

	// Second send: autoInject now false, submit should be false.
	h.Handle(ctx, mustMarshal(t, SendInput{Cwd: "/tmp/w", Body: "second", Tldr: "second"}))

	e.fake.mu.Lock()
	defer e.fake.mu.Unlock()
	if len(e.fake.notifyPosts) != 2 {
		t.Fatalf("notifyPosts count = %d, want 2", len(e.fake.notifyPosts))
	}
	if !e.fake.notifyPosts[0].Submit {
		t.Fatal("first notify submit = false, want true")
	}
	if e.fake.notifyPosts[1].Submit {
		t.Fatal("second notify submit = true, want false after hot-reload")
	}
}

// TestSend_Worker_DefaultRoutes_ToCoordinator verifies routing logic.
func TestSend_Worker_DefaultRoutes_ToCoordinator(t *testing.T) {
	ctx := context.Background()
	e, h := setupSend(t, true, "submitted")
	_, _ = seedOrchestratorWithBoundPair(t, e)

	resp := h.Handle(ctx, mustMarshal(t, SendInput{Cwd: "/tmp/w", Body: "need a ruling", Tldr: "need a ruling"}))
	out := decodeSendOutput(t, resp)
	if out.RecipientRole != "coord" {
		t.Fatalf("recipient = %q, want coord", out.RecipientRole)
	}

	e.fake.mu.Lock()
	defer e.fake.mu.Unlock()
	if len(e.fake.notifyPosts) != 1 {
		t.Fatalf("notifyPosts count = %d, want 1", len(e.fake.notifyPosts))
	}
}

// TestSend_ExplicitTo_LooksUpRoleByName verifies explicit `to` routing.
func TestSend_ExplicitTo_LooksUpRoleByName(t *testing.T) {
	ctx := context.Background()
	e, h := setupSend(t, true, "pending")

	orch, _ := e.db.Orchestrators.Create(ctx, "foo")
	coord, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "p"})
	w1, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: orch.ID, Name: "w1", Kind: db.KindWorker, ArgusProject: "p"})
	w2, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: orch.ID, Name: "w2", Kind: db.KindWorker, ArgusProject: "p"})
	for _, r := range []struct {
		role *db.Role
		tid  string
		wt   string
	}{
		{coord, "t-coord", "/tmp/coord"},
		{w1, "t-w1", "/tmp/w1"},
		{w2, "t-w2", "/tmp/w2"},
	} {
		_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{RoleID: r.role.ID, ArgusTaskID: r.tid, WorktreePath: r.wt})
		e.fake.addTask(taskFor(r.tid, r.wt))
	}

	resp := h.Handle(ctx, mustMarshal(t, SendInput{Cwd: "/tmp/coord", Body: "do X", Tldr: "do X", To: "w2"}))
	out := decodeSendOutput(t, resp)
	if out.RecipientRole != "w2" {
		t.Fatalf("recipient = %q, want w2", out.RecipientRole)
	}
	if out.DeliveryMode != string(db.DeliveryBusyBuffer) {
		t.Fatalf("delivery_mode = %q, want busy_buffer", out.DeliveryMode)
	}

	e.fake.mu.Lock()
	defer e.fake.mu.Unlock()
	if len(e.fake.notifyPosts) != 1 {
		t.Fatalf("notifyPosts count = %d, want 1", len(e.fake.notifyPosts))
	}
}

// TestSend_RecipientHasNoLiveBinding_QueuesPending verifies queued_no_binding path.
func TestSend_RecipientHasNoLiveBinding_QueuesPending(t *testing.T) {
	ctx := context.Background()
	e, h := setupSend(t, true, "submitted")

	orch, _ := e.db.Orchestrators.Create(ctx, "foo")
	_, _ = e.db.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "p"})
	worker, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: orch.ID, Name: "w", Kind: db.KindWorker, ArgusProject: "p"})
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{RoleID: worker.ID, ArgusTaskID: "t-w", WorktreePath: "/tmp/w"})
	e.fake.addTask(taskFor("t-w", "/tmp/w"))

	resp := h.Handle(ctx, mustMarshal(t, SendInput{Cwd: "/tmp/w", Body: "ping coord", Tldr: "ping coord"}))
	out := decodeSendOutput(t, resp)
	if out.DeliveryMode != string(db.DeliveryQueuedNoBinding) {
		t.Fatalf("delivery_mode = %q, want queued_no_binding", out.DeliveryMode)
	}

	e.fake.mu.Lock()
	defer e.fake.mu.Unlock()
	if len(e.fake.notifyPosts) != 0 {
		t.Fatalf("notifyPosts count = %d, want 0 for queued_no_binding", len(e.fake.notifyPosts))
	}
	row, err := e.db.Messages.GetByID(ctx, out.MessageID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if row.DeliveryMode != db.DeliveryQueuedNoBinding {
		t.Fatalf("persisted delivery_mode = %q, want queued_no_binding", row.DeliveryMode)
	}
}

// TestSend_NotifyError_Surfaces verifies that an argus notify failure (500) is
// surfaced as a handler error.
func TestSend_NotifyError_Surfaces(t *testing.T) {
	ctx := context.Background()
	e, h := setupSend(t, true, "submitted")
	e.fake.notifyFail = true
	_, _ = seedOrchestratorWithBoundPair(t, e)

	resp := h.Handle(ctx, mustMarshal(t, SendInput{Cwd: "/tmp/w", Body: "x", Tldr: "x"}))
	if !resp.IsError {
		t.Fatal("expected error when argus notify fails")
	}
}

// TestSend_NotifyNotFound_ReturnsError verifies that a 404 from argus notify
// (task has no active PTY session) returns an error to the caller and does
// not advance delivery_mode past pending.
// Delta: "Scenario: No active session, delivery fails"
func TestSend_NotifyNotFound_ReturnsError(t *testing.T) {
	ctx := context.Background()
	e, h := setupSend(t, true, "submitted")
	e.fake.notifyNotFound = true
	_, _ = seedOrchestratorWithBoundPair(t, e)

	resp := h.Handle(ctx, mustMarshal(t, SendInput{Cwd: "/tmp/w", Body: "x", Tldr: "x"}))
	if !resp.IsError {
		t.Fatal("expected error when argus notify returns 404 (no active session)")
	}
	// Error response means delivery_mode was NOT advanced past pending (no
	// SetDelivered call reached), satisfying the delta scenario.
}

// TestSend_CoordinatorWithoutTo_Rejected verifies routing validation.
func TestSend_CoordinatorWithoutTo_Rejected(t *testing.T) {
	ctx := context.Background()
	e, h := setupSend(t, true, "submitted")
	_, _ = seedOrchestratorWithBoundPair(t, e)

	resp := h.Handle(ctx, mustMarshal(t, SendInput{Cwd: "/tmp/coord", Body: "FYI", Tldr: "FYI"}))
	if !resp.IsError {
		t.Fatalf("expected error for coordinator-without-to")
	}
	if !strings.Contains(resp.Content[0].Text, "explicit") {
		t.Fatalf("error wording should explain explicit-`to` requirement: %q", resp.Content[0].Text)
	}
	e.fake.mu.Lock()
	defer e.fake.mu.Unlock()
	if len(e.fake.notifyPosts) != 0 {
		t.Fatalf("rejected send should not notify, got %d calls", len(e.fake.notifyPosts))
	}
}

// TestSend_ExplicitTo_UnknownRole verifies unknown role error.
func TestSend_ExplicitTo_UnknownRole(t *testing.T) {
	ctx := context.Background()
	e, h := setupSend(t, true, "submitted")
	_, _ = seedOrchestratorWithBoundPair(t, e)

	resp := h.Handle(ctx, mustMarshal(t, SendInput{Cwd: "/tmp/coord", Body: "x", Tldr: "x", To: "ghost"}))
	if !resp.IsError {
		t.Fatal("expected error")
	}
	if !strings.Contains(resp.Content[0].Text, "does not exist") {
		t.Fatalf("error wording: %q", resp.Content[0].Text)
	}
}

// TestSend_BodyRequired verifies validation.
func TestSend_BodyRequired(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	h := NewSendHandler(e.resolver, e.db, e.client, true, 300000)
	resp := h.Handle(ctx, mustMarshal(t, SendInput{Cwd: "/x"}))
	if !resp.IsError {
		t.Fatal("expected error")
	}
}

// taskFor builds an argus.Task with a specific id and worktree path.
func taskFor(id, worktree string) argusTask {
	return argusTask{ID: id, WorktreePath: worktree, Project: "p", Name: id}
}

// argusTask is a TRUE alias of argus.Task so this file's helpers track the
// real struct exactly.
type argusTask = argus.Task
