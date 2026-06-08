package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/anutron/hera/internal/argus"
	"github.com/anutron/hera/internal/db"
)

// Multi-binding handler scenarios: hera_send, hera_inbox, hera_status,
// hera_join (claim mode). Each scenario sets up a single argus task with
// two live bindings (worker in orch A, coord in orch B) and exercises
// the disambiguation rules from openspec/changes/add-multi-binding.

func setupMultiBindingFixture(t *testing.T) (*handlerFixture, *db.Role, *db.Role) {
	t.Helper()
	ctx := context.Background()
	e := setupHandlers(t)

	orchA, _ := e.db.Orchestrators.Create(ctx, "A")
	workerA, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orchA.ID, Name: "worker", Kind: db.KindWorker, ArgusProject: "p",
	})
	// Coordinator in A so a default-route hera_send from worker has a target.
	_, _ = e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orchA.ID, Name: "coord-A", Kind: db.KindCoordinator, ArgusProject: "p",
	})

	orchB, _ := e.db.Orchestrators.Create(ctx, "B")
	coordB, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orchB.ID, Name: "coord-B", Kind: db.KindCoordinator, ArgusProject: "p",
	})

	// Same argus task incarnates worker in A AND coord in B.
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: workerA.ID, ArgusTaskID: "t-multi", WorktreePath: "/wt",
	})
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: coordB.ID, ArgusTaskID: "t-multi", WorktreePath: "/wt",
	})
	e.fake.addTask(argus.Task{ID: "t-multi", Project: "p", WorktreePath: "/wt"})
	return e, workerA, coordB
}

func TestMultiBinding_HeraJoinClaimAmbiguous(t *testing.T) {
	ctx := context.Background()
	e, _, _ := setupMultiBindingFixture(t)

	h := NewJoinHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, JoinInput{Cwd: "/wt"}))
	if !resp.IsError {
		t.Fatalf("expected ambiguous error, got success: %q", resp.Content[0].Text)
	}
	if !strings.Contains(resp.Content[0].Text, "multiple live hera bindings") {
		t.Fatalf("expected multi-binding hint, got: %q", resp.Content[0].Text)
	}
	if !strings.Contains(resp.Content[0].Text, "A/") || !strings.Contains(resp.Content[0].Text, "B/") {
		t.Fatalf("expected both orchestrators named, got: %q", resp.Content[0].Text)
	}
}

func TestMultiBinding_HeraJoinClaimWithOrchestrator(t *testing.T) {
	ctx := context.Background()
	e, _, _ := setupMultiBindingFixture(t)

	h := NewJoinHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, JoinInput{Cwd: "/wt", Orchestrator: "B"}))
	out := decodeJoinOutput(t, resp)
	if out.Orchestrator != "B" || out.RoleName != "coord-B" || out.Kind != "coordinator" {
		t.Fatalf("expected to claim coord-B in B, got %+v", out)
	}
}

func TestMultiBinding_HeraJoinClaimUnknownOrchestrator(t *testing.T) {
	ctx := context.Background()
	e, _, _ := setupMultiBindingFixture(t)
	h := NewJoinHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, JoinInput{Cwd: "/wt", Orchestrator: "Z"}))
	if !resp.IsError {
		t.Fatalf("expected error claiming nonexistent orchestrator Z")
	}
}

type sendOutDecoded struct {
	MessageID     int64  `json:"message_id"`
	RecipientRole string `json:"recipient_role"`
	DeliveryMode  string `json:"delivery_mode"`
}

func decodeSendOut(t *testing.T, r Response) sendOutDecoded {
	t.Helper()
	if r.IsError {
		t.Fatalf("got error response: %s", r.Content[0].Text)
	}
	var out sendOutDecoded
	if err := json.Unmarshal([]byte(r.Content[0].Text), &out); err != nil {
		t.Fatalf("decode SendOutput: %v", err)
	}
	return out
}

func TestMultiBinding_HeraSendAmbiguousWithoutOrchestrator(t *testing.T) {
	ctx := context.Background()
	e, _, _ := setupMultiBindingFixture(t)

	h := NewSendHandler(e.resolver, e.db, e.client, true, 300000)
	// Worker-default route would target coord-A; coordinator senders
	// require explicit to=. Either way, the ambiguity should fire FIRST
	// because the sender role isn't yet resolved.
	resp := h.Handle(ctx, mustMarshal(t, SendInput{Cwd: "/wt", Body: "hello", Tldr: "hello"}))
	if !resp.IsError {
		t.Fatalf("expected ambiguous error, got success: %q", resp.Content[0].Text)
	}
	if !strings.Contains(resp.Content[0].Text, "multiple live hera bindings") {
		t.Fatalf("expected multi-binding hint, got: %q", resp.Content[0].Text)
	}
}

func TestMultiBinding_HeraSendWithOrchestratorRoutesToThatOrchestratorsCoord(t *testing.T) {
	ctx := context.Background()
	e, _, _ := setupMultiBindingFixture(t)

	// Sender is worker in A -> default route should go to coord-A,
	// not coord-B (cross-orchestrator routing is forbidden).
	h := NewSendHandler(e.resolver, e.db, e.client, true, 300000)
	resp := h.Handle(ctx, mustMarshal(t, SendInput{
		Cwd: "/wt", Body: "hi from A worker", Tldr: "hi from A worker", Orchestrator: "A",
	}))
	out := decodeSendOut(t, resp)
	if out.RecipientRole != "coord-A" {
		t.Fatalf("expected default route to coord-A, got %q", out.RecipientRole)
	}
}

func TestMultiBinding_HeraSendCoordinatorWithToAndOrchestrator(t *testing.T) {
	ctx := context.Background()
	e, _, _ := setupMultiBindingFixture(t)

	// Pre-seed a worker under B so coord-B can address them.
	ctxB := context.Background()
	orchB, _ := e.db.Orchestrators.GetByName(ctxB, "B")
	_, _ = e.db.Roles.Create(ctxB, db.CreateRoleInput{
		OrchestratorID: orchB.ID, Name: "impl-B", Kind: db.KindWorker, ArgusProject: "p",
	})

	h := NewSendHandler(e.resolver, e.db, e.client, true, 300000)
	resp := h.Handle(ctx, mustMarshal(t, SendInput{
		Cwd: "/wt", Body: "review needed", Tldr: "review needed", Orchestrator: "B", To: "impl-B",
	}))
	out := decodeSendOut(t, resp)
	if out.RecipientRole != "impl-B" {
		t.Fatalf("expected recipient impl-B, got %q", out.RecipientRole)
	}
}

func TestMultiBinding_HeraStatusWithOrchestratorScopesToRoleOnly(t *testing.T) {
	ctx := context.Background()
	e, workerA, coordB := setupMultiBindingFixture(t)

	h := NewStatusHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, StatusInput{
		Cwd: "/wt", Status: "working", Orchestrator: "B",
	}))
	if resp.IsError {
		t.Fatalf("hera_status: %q", resp.Content[0].Text)
	}
	// coord-B should be working; workerA should still have no status row.
	rs, err := e.db.RoleStatus.Get(ctx, coordB.ID)
	if err != nil {
		t.Fatalf("RoleStatus.Get coord-B: %v", err)
	}
	if rs.Status != db.StatusWorking {
		t.Fatalf("coord-B status = %v, want working", rs.Status)
	}
	if _, err := e.db.RoleStatus.Get(ctx, workerA.ID); err == nil {
		t.Fatalf("workerA should not have a status row after scoped hera_status")
	}
}

func TestMultiBinding_HeraInboxAmbiguous(t *testing.T) {
	ctx := context.Background()
	e, _, _ := setupMultiBindingFixture(t)
	h := NewInboxHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, InboxInput{Cwd: "/wt"}))
	if !resp.IsError {
		t.Fatalf("expected ambiguous error")
	}
}

func TestMultiBinding_HeraInboxWithOrchestratorScopesCorrectly(t *testing.T) {
	ctx := context.Background()
	e, workerA, coordB := setupMultiBindingFixture(t)
	// Send a message addressed to coord-B by inserting directly.
	_, _ = e.db.Messages.Create(ctx, db.CreateMessageInput{
		FromRoleID: workerA.ID, ToRoleID: coordB.ID, Body: "from worker-A to coord-B",
	})
	h := NewInboxHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, InboxInput{Cwd: "/wt", Orchestrator: "B"}))
	if resp.IsError {
		t.Fatalf("hera_inbox: %q", resp.Content[0].Text)
	}
	var out InboxOutput
	if err := json.Unmarshal([]byte(resp.Content[0].Text), &out); err != nil {
		t.Fatalf("decode InboxOutput: %v", err)
	}
	if out.RoleName != "coord-B" {
		t.Fatalf("expected inbox for coord-B, got role %q", out.RoleName)
	}
	if len(out.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(out.Messages))
	}
}

func TestMultiBinding_NewOrchestratorAddsThirdBinding(t *testing.T) {
	ctx := context.Background()
	e, _, _ := setupMultiBindingFixture(t)
	h := NewNewOrchestratorHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, NewOrchestratorInput{
		Cwd: "/wt", Name: "C", CoordinatorRoleName: "coord-C",
	}))
	if resp.IsError {
		t.Fatalf("expected success bootstrapping third orchestrator; got: %q", resp.Content[0].Text)
	}
	got, err := e.db.Bindings.ListLiveByTaskID(ctx, "t-multi")
	if err != nil {
		t.Fatalf("ListLiveByTaskID: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 live bindings, got %d", len(got))
	}
}
