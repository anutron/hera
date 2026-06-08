package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/anutron/hera/internal/argus"
	"github.com/anutron/hera/internal/db"
)

// fakeInjector records every Inject call and returns a configurable mode.
type fakeInjector struct {
	mu     sync.Mutex
	calls  []fakeInjectCall
	mode   db.DeliveryMode
	failOn map[string]error // taskID → error
}

type fakeInjectCall struct {
	TaskID     string
	SenderRole string
	MsgID      int64
	Tldr       string
}

func (f *fakeInjector) Inject(ctx context.Context, taskID, senderRoleName string, msgID int64, tldr string) (db.DeliveryMode, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.failOn[taskID]; ok {
		return db.DeliveryPending, err
	}
	f.calls = append(f.calls, fakeInjectCall{taskID, senderRoleName, msgID, tldr})
	return f.mode, nil
}

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

func TestSend_Worker_DefaultRoutes_ToCoordinator(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	orch, _ := e.db.Orchestrators.Create(ctx, "foo")
	coord, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "p",
	})
	worker, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w", Kind: db.KindWorker, ArgusProject: "p",
	})
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: worker.ID, ArgusTaskID: "t-w", WorktreePath: "/tmp/w",
	})
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: coord.ID, ArgusTaskID: "t-coord", WorktreePath: "/tmp/coord",
	})
	e.fake.addTask(taskFor("t-w", "/tmp/w"))
	e.fake.addTask(taskFor("t-coord", "/tmp/coord"))

	inj := &fakeInjector{mode: db.DeliveryIdleSubmit}
	h := NewSendHandler(e.resolver, e.db, inj)
	resp := h.Handle(ctx, mustMarshal(t, SendInput{Cwd: "/tmp/w", Body: "need a ruling", Tldr: "need a ruling"}))
	out := decodeSendOutput(t, resp)
	if out.RecipientRole != "coord" {
		t.Fatalf("recipient = %q", out.RecipientRole)
	}
	if out.DeliveryMode != string(db.DeliveryIdleSubmit) {
		t.Fatalf("delivery = %q", out.DeliveryMode)
	}
	inj.mu.Lock()
	defer inj.mu.Unlock()
	if len(inj.calls) != 1 || inj.calls[0].TaskID != "t-coord" {
		t.Fatalf("inject calls = %+v", inj.calls)
	}
	if inj.calls[0].SenderRole != "w" {
		t.Fatalf("sender on inject = %q", inj.calls[0].SenderRole)
	}
	// Verify the message row was updated with the chosen delivery mode (spec MUSTs this).
	row, err := e.db.Messages.GetByID(ctx, out.MessageID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if row.DeliveryMode != db.DeliveryIdleSubmit {
		t.Fatalf("persisted delivery_mode = %q, want %q", row.DeliveryMode, db.DeliveryIdleSubmit)
	}
	if row.DeliveredAt == nil {
		t.Fatalf("delivered_at not set on persisted row")
	}
}

func TestSend_CoordinatorWithoutTo_Rejected(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	orch, _ := e.db.Orchestrators.Create(ctx, "foo")
	coord, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "p",
	})
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: coord.ID, ArgusTaskID: "t-coord", WorktreePath: "/tmp/coord",
	})
	e.fake.addTask(taskFor("t-coord", "/tmp/coord"))

	inj := &fakeInjector{mode: db.DeliveryIdleSubmit}
	h := NewSendHandler(e.resolver, e.db, inj)
	resp := h.Handle(ctx, mustMarshal(t, SendInput{Cwd: "/tmp/coord", Body: "FYI", Tldr: "FYI"}))
	if !resp.IsError {
		t.Fatalf("expected error for coordinator-without-to, got success: %+v", resp)
	}
	if !strings.Contains(resp.Content[0].Text, "explicit") {
		t.Fatalf("error wording should explain the explicit-`to` requirement: %q", resp.Content[0].Text)
	}
	inj.mu.Lock()
	defer inj.mu.Unlock()
	if len(inj.calls) != 0 {
		t.Fatalf("rejected send should not inject, got %d calls", len(inj.calls))
	}
}

func TestSend_ExplicitTo_LooksUpRoleByName(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	orch, _ := e.db.Orchestrators.Create(ctx, "foo")
	coord, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "p",
	})
	w1, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w1", Kind: db.KindWorker, ArgusProject: "p",
	})
	w2, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w2", Kind: db.KindWorker, ArgusProject: "p",
	})
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: coord.ID, ArgusTaskID: "t-coord", WorktreePath: "/tmp/coord",
	})
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: w1.ID, ArgusTaskID: "t-w1", WorktreePath: "/tmp/w1",
	})
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: w2.ID, ArgusTaskID: "t-w2", WorktreePath: "/tmp/w2",
	})
	e.fake.addTask(taskFor("t-coord", "/tmp/coord"))
	e.fake.addTask(taskFor("t-w1", "/tmp/w1"))
	e.fake.addTask(taskFor("t-w2", "/tmp/w2"))

	inj := &fakeInjector{mode: db.DeliveryBusyBuffer}
	h := NewSendHandler(e.resolver, e.db, inj)
	resp := h.Handle(ctx, mustMarshal(t, SendInput{Cwd: "/tmp/coord", Body: "do X", Tldr: "do X", To: "w2"}))
	out := decodeSendOutput(t, resp)
	if out.RecipientRole != "w2" {
		t.Fatalf("recipient = %q", out.RecipientRole)
	}
	if out.DeliveryMode != string(db.DeliveryBusyBuffer) {
		t.Fatalf("delivery = %q", out.DeliveryMode)
	}
	inj.mu.Lock()
	if len(inj.calls) != 1 || inj.calls[0].TaskID != "t-w2" {
		inj.mu.Unlock()
		t.Fatalf("inject call routed to wrong task: %+v", inj.calls)
	}
	inj.mu.Unlock()
	// Verify the message row was updated with busy_buffer (spec MUSTs this).
	row, err := e.db.Messages.GetByID(ctx, out.MessageID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if row.DeliveryMode != db.DeliveryBusyBuffer {
		t.Fatalf("persisted delivery_mode = %q, want %q", row.DeliveryMode, db.DeliveryBusyBuffer)
	}
}

func TestSend_ExplicitTo_UnknownRole(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	orch, _ := e.db.Orchestrators.Create(ctx, "foo")
	coord, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "p",
	})
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: coord.ID, ArgusTaskID: "t-coord", WorktreePath: "/tmp/coord",
	})
	e.fake.addTask(taskFor("t-coord", "/tmp/coord"))

	inj := &fakeInjector{}
	h := NewSendHandler(e.resolver, e.db, inj)
	resp := h.Handle(ctx, mustMarshal(t, SendInput{Cwd: "/tmp/coord", Body: "x", Tldr: "x", To: "ghost"}))
	if !resp.IsError {
		t.Fatalf("expected error")
	}
	if !strings.Contains(resp.Content[0].Text, "does not exist") {
		t.Fatalf("error wording: %q", resp.Content[0].Text)
	}
}

func TestSend_RecipientHasNoLiveBinding_QueuesPending(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	orch, _ := e.db.Orchestrators.Create(ctx, "foo")
	coord, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "p",
	})
	worker, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w", Kind: db.KindWorker, ArgusProject: "p",
	})
	// Only the worker is bound; coordinator has no live binding.
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: worker.ID, ArgusTaskID: "t-w", WorktreePath: "/tmp/w",
	})
	e.fake.addTask(taskFor("t-w", "/tmp/w"))
	_ = coord

	inj := &fakeInjector{mode: db.DeliveryIdleSubmit}
	h := NewSendHandler(e.resolver, e.db, inj)
	resp := h.Handle(ctx, mustMarshal(t, SendInput{Cwd: "/tmp/w", Body: "ping coord", Tldr: "ping coord"}))
	out := decodeSendOutput(t, resp)
	if out.DeliveryMode != string(db.DeliveryQueuedNoBinding) {
		t.Fatalf("delivery = %q, want queued_no_binding", out.DeliveryMode)
	}
	// Verify the message row was persisted with the queued mode (spec MUSTs this).
	row, err := e.db.Messages.GetByID(ctx, out.MessageID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if row.DeliveryMode != db.DeliveryQueuedNoBinding {
		t.Fatalf("persisted delivery_mode = %q, want %q", row.DeliveryMode, db.DeliveryQueuedNoBinding)
	}
	inj.mu.Lock()
	if len(inj.calls) != 0 {
		t.Fatalf("queued-no-binding should not inject")
	}
	inj.mu.Unlock()
}

func TestSend_InjectError_Surfaces(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	orch, _ := e.db.Orchestrators.Create(ctx, "foo")
	coord, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "p",
	})
	worker, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w", Kind: db.KindWorker, ArgusProject: "p",
	})
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: worker.ID, ArgusTaskID: "t-w", WorktreePath: "/tmp/w",
	})
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: coord.ID, ArgusTaskID: "t-coord", WorktreePath: "/tmp/coord",
	})
	e.fake.addTask(taskFor("t-w", "/tmp/w"))
	e.fake.addTask(taskFor("t-coord", "/tmp/coord"))

	inj := &fakeInjector{
		mode:   db.DeliveryIdleSubmit,
		failOn: map[string]error{"t-coord": errors.New("network unreachable")},
	}
	h := NewSendHandler(e.resolver, e.db, inj)
	resp := h.Handle(ctx, mustMarshal(t, SendInput{Cwd: "/tmp/w", Body: "x", Tldr: "x"}))
	if !resp.IsError {
		t.Fatalf("expected error")
	}
	if !strings.Contains(resp.Content[0].Text, "network unreachable") {
		t.Fatalf("error did not surface root cause: %q", resp.Content[0].Text)
	}
}

func TestSend_BodyRequired(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	inj := &fakeInjector{}
	h := NewSendHandler(e.resolver, e.db, inj)
	resp := h.Handle(ctx, mustMarshal(t, SendInput{Cwd: "/x"}))
	if !resp.IsError {
		t.Fatalf("expected error")
	}
}

// taskFor builds an argus.Task with a specific id and worktree path.
func taskFor(id, worktree string) argusTask {
	return argusTask{ID: id, WorktreePath: worktree, Project: "p", Name: id}
}

// argusTask is a TRUE alias of argus.Task so this file's helpers track the
// real struct exactly. The previous STRUCTURAL re-declaration silently broke
// every time argus.Task gained a field (Elapsed / Idle / NeedsInput /
// Archived) — a drift class a real alias cannot have.
type argusTask = argus.Task
