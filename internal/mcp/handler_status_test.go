package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/anutron/hera/internal/argus"
	"github.com/anutron/hera/internal/db"
)

// statusFixture provisions a worker role with a live binding so hera_status
// has a valid caller to resolve. The argus task_meta mirror is observable
// on the fake argus stub.
func statusFixture(t *testing.T) (*handlerFixture, *db.Role) {
	t.Helper()
	ctx := context.Background()
	e := setupHandlers(t)
	orch, _ := e.db.Orchestrators.Create(ctx, "foo")
	role, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w", Kind: db.KindWorker, ArgusProject: "p",
	})
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: role.ID, ArgusTaskID: "t-w", WorktreePath: "/tmp/w",
	})
	e.fake.addTask(argus.Task{ID: "t-w", Name: "w", Project: "p", WorktreePath: "/tmp/w"})
	return e, role
}

// decodeStatusOutput is the StatusHandler analogue of decodeJoinOutput.
// Returns the JSON object verbatim so tests can assert presence/absence
// of optional fields like argus_link_error.
func decodeStatusOutput(t *testing.T, r Response) map[string]any {
	t.Helper()
	if r.IsError {
		t.Fatalf("got error response: %s", r.Content[0].Text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(r.Content[0].Text), &out); err != nil {
		t.Fatalf("decode StatusOutput: %v", err)
	}
	return out
}

func TestStatus_ArgusLink_Healthy(t *testing.T) {
	resetLink(t)
	argus.SetLinkState(argus.LinkHealthy)
	e, _ := statusFixture(t)

	h := NewStatusHandler(e.resolver, e.db, e.client)
	resp := h.Handle(context.Background(), mustMarshal(t, StatusInput{Cwd: "/tmp/w", Status: "working"}))
	out := decodeStatusOutput(t, resp)

	if got, _ := out["argus_link"].(string); got != "healthy" {
		t.Fatalf("argus_link = %v, want %q", out["argus_link"], "healthy")
	}
	if _, present := out["argus_link_error"]; present {
		t.Fatalf("argus_link_error must be absent when state is healthy, got %v", out["argus_link_error"])
	}
}

func TestStatus_ArgusLink_Recovering(t *testing.T) {
	resetLink(t)
	argus.SetLinkState(argus.LinkRecovering)
	e, _ := statusFixture(t)

	h := NewStatusHandler(e.resolver, e.db, e.client)
	resp := h.Handle(context.Background(), mustMarshal(t, StatusInput{Cwd: "/tmp/w", Status: "working"}))
	out := decodeStatusOutput(t, resp)

	if got, _ := out["argus_link"].(string); got != "recovering" {
		t.Fatalf("argus_link = %v, want %q", out["argus_link"], "recovering")
	}
	if _, present := out["argus_link_error"]; present {
		t.Fatalf("argus_link_error must be absent when state is recovering, got %v", out["argus_link_error"])
	}
}

func TestStatus_Done_AutoArchivesWorker(t *testing.T) {
	resetLink(t)
	e, role := statusFixture(t)
	ctx := context.Background()

	h := NewStatusHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, StatusInput{Cwd: "/tmp/w", Status: "done"}))
	out := decodeStatusOutput(t, resp)

	if got, _ := out["auto_archived"].(bool); !got {
		t.Fatalf("auto_archived = %v, want true for worker+done", out["auto_archived"])
	}

	// Role must now be archived in hera DB.
	updated, err := e.db.Roles.GetByID(ctx, role.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if updated.ArchivedAt == nil {
		t.Fatal("worker role must be archived after hera_status(done)")
	}
}

func TestStatus_Done_CoordinatorNotAutoArchived(t *testing.T) {
	resetLink(t)
	ctx := context.Background()
	e := setupHandlers(t)
	orch, _ := e.db.Orchestrators.Create(ctx, "bar")
	role, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "p",
	})
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: role.ID, ArgusTaskID: "t-c", WorktreePath: "/tmp/c",
	})
	e.fake.addTask(argus.Task{ID: "t-c", Name: "coord", Project: "p", WorktreePath: "/tmp/c"})

	h := NewStatusHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, StatusInput{Cwd: "/tmp/c", Status: "done"}))
	out := decodeStatusOutput(t, resp)

	if got, _ := out["auto_archived"].(bool); got {
		t.Fatal("auto_archived must be false for coordinator roles")
	}
	updated, _ := e.db.Roles.GetByID(ctx, role.ID)
	if updated.ArchivedAt != nil {
		t.Fatal("coordinator role must NOT be auto-archived on done")
	}
}

func TestStatus_ArgusLink_Down_IncludesLastError(t *testing.T) {
	resetLink(t)
	wantErr := "socket Ports call: dial unix /Users/aaron/.argus/daemon.sock: connect: no such file or directory"
	argus.SetLinkError(errors.New(wantErr))
	argus.SetLinkState(argus.LinkDown)
	e, _ := statusFixture(t)

	h := NewStatusHandler(e.resolver, e.db, e.client)
	resp := h.Handle(context.Background(), mustMarshal(t, StatusInput{Cwd: "/tmp/w", Status: "blocked"}))
	out := decodeStatusOutput(t, resp)

	if got, _ := out["argus_link"].(string); got != "down" {
		t.Fatalf("argus_link = %v, want %q", out["argus_link"], "down")
	}
	gotErr, present := out["argus_link_error"].(string)
	if !present {
		t.Fatalf("argus_link_error must be present when state is down")
	}
	if gotErr != wantErr {
		t.Fatalf("argus_link_error = %q, want %q", gotErr, wantErr)
	}
}
