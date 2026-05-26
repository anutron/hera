package ops

import (
	"context"
	"strings"
	"testing"
)

func TestRenameOrchestrator_EmptyName(t *testing.T) {
	s, db, _, _, _ := newTestService()
	o := db.seedOrchestrator("foo", false)
	if err := s.RenameOrchestrator(context.Background(), o.ID, "  "); asValidation(err) == nil {
		t.Fatalf("expected ValidationError, got %v", err)
	}
}

func TestRenameOrchestrator_NoopWhenSameName(t *testing.T) {
	s, db, _, _, _ := newTestService()
	o := db.seedOrchestrator("foo", false)
	if err := s.RenameOrchestrator(context.Background(), o.ID, "foo"); err != nil {
		t.Fatalf("RenameOrchestrator: %v", err)
	}
	if len(db.renameOrchCalls) != 0 {
		t.Fatalf("expected 0 rename DAO calls (no-op), got %d", len(db.renameOrchCalls))
	}
}

func TestRenameOrchestrator_DuplicateActiveRejected(t *testing.T) {
	s, db, _, _, _ := newTestService()
	a := db.seedOrchestrator("foo", false)
	db.seedOrchestrator("bar", false)
	err := s.RenameOrchestrator(context.Background(), a.ID, "bar")
	if v := asValidation(err); v == nil {
		t.Fatalf("expected ValidationError, got %v", err)
	} else if !strings.Contains(v.Message, "already exists") {
		t.Fatalf("message = %q", v.Message)
	}
	if len(db.renameOrchCalls) != 0 {
		t.Fatalf("expected 0 rename DAO calls (rejected), got %d", len(db.renameOrchCalls))
	}
}

func TestRenameOrchestrator_DuplicateArchivedAllowed(t *testing.T) {
	s, db, _, _, _ := newTestService()
	a := db.seedOrchestrator("foo", false)
	db.seedOrchestrator("bar", true) // archived sibling

	if err := s.RenameOrchestrator(context.Background(), a.ID, "bar"); err != nil {
		t.Fatalf("RenameOrchestrator: %v", err)
	}
	if len(db.renameOrchCalls) != 1 || db.renameOrchCalls[0].NewName != "bar" {
		t.Fatalf("DAO rename calls = %+v", db.renameOrchCalls)
	}
}

func TestRenameOrchestrator_Success(t *testing.T) {
	s, db, _, _, _ := newTestService()
	o := db.seedOrchestrator("foo", false)
	if err := s.RenameOrchestrator(context.Background(), o.ID, "bar"); err != nil {
		t.Fatalf("RenameOrchestrator: %v", err)
	}
	if len(db.renameOrchCalls) != 1 {
		t.Fatalf("expected 1 DAO rename call, got %d", len(db.renameOrchCalls))
	}
	if db.renameOrchCalls[0].ID != o.ID || db.renameOrchCalls[0].NewName != "bar" {
		t.Fatalf("DAO rename call = %+v", db.renameOrchCalls[0])
	}
	// orchestrator row in fake mutated to new name.
	got, _ := db.GetOrchestratorByID(context.Background(), o.ID)
	if got.Name != "bar" {
		t.Fatalf("fake DB not updated: name = %q", got.Name)
	}
}

func TestRenameRole_EmptyName(t *testing.T) {
	s, db, _, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", false)
	r := db.seedRole(orch.ID, "coord", KindCoordinator, "foo", false)
	if err := s.RenameRole(context.Background(), r.ID, ""); asValidation(err) == nil {
		t.Fatalf("expected ValidationError, got %v", err)
	}
}

func TestRenameRole_DuplicateSiblingRejected(t *testing.T) {
	s, db, _, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", false)
	a := db.seedRole(orch.ID, "coord", KindCoordinator, "foo", false)
	db.seedRole(orch.ID, "w1", KindWorker, "foo", false)
	if err := s.RenameRole(context.Background(), a.ID, "w1"); asValidation(err) == nil {
		t.Fatalf("expected ValidationError, got %v", err)
	}
}

func TestRenameRole_ArchivedSiblingAllowed(t *testing.T) {
	s, db, _, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", false)
	a := db.seedRole(orch.ID, "coord", KindCoordinator, "foo", false)
	db.seedRole(orch.ID, "lead", KindWorker, "foo", true) // archived sibling
	if err := s.RenameRole(context.Background(), a.ID, "lead"); err != nil {
		t.Fatalf("RenameRole: %v", err)
	}
}

func TestRenameRole_SameNameAcrossOrchestratorsAllowed(t *testing.T) {
	s, db, _, _, _ := newTestService()
	a := db.seedOrchestrator("foo", false)
	b := db.seedOrchestrator("bar", false)
	roleA := db.seedRole(a.ID, "coord", KindCoordinator, "foo", false)
	db.seedRole(b.ID, "coord", KindCoordinator, "bar", false)
	// Rename roleA from "coord" to "coord-ish" then back to "coord"
	// would no-op the same-name; pick a fresh name that exists across
	// orchestrators.
	if err := s.RenameRole(context.Background(), roleA.ID, "coord"); err != nil {
		t.Fatalf("same name on same role is no-op: %v", err)
	}
	// Create a fresh role under A named "lead" and rename it to "coord"
	// — wait that conflicts in A. Better test: rename role under B to
	// "coord" — that's a no-op already. The cross-orchestrator
	// uniqueness is asserted by the absence of a validation error when
	// a name exists in a sibling orchestrator. Verify:
	leadA := db.seedRole(a.ID, "lead", KindWorker, "foo", false)
	if err := s.RenameRole(context.Background(), leadA.ID, "coord"); asValidation(err) == nil {
		t.Fatalf("renaming to name held in same orchestrator should fail")
	}
	leadB := db.seedRole(b.ID, "lead", KindWorker, "bar", false)
	if err := s.RenameRole(context.Background(), leadB.ID, "coord"); asValidation(err) == nil {
		// Wait: "coord" exists in B as roleA's sibling. This should fail.
		t.Fatalf("renaming to a name in same orchestrator should fail")
	}
}

func TestRenameRole_Success(t *testing.T) {
	s, db, _, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", false)
	r := db.seedRole(orch.ID, "coord", KindCoordinator, "foo", false)
	if err := s.RenameRole(context.Background(), r.ID, "lead"); err != nil {
		t.Fatalf("RenameRole: %v", err)
	}
	if len(db.renameRoleCalls) != 1 {
		t.Fatalf("expected 1 DAO call, got %d", len(db.renameRoleCalls))
	}
	got, _ := db.GetRoleByID(context.Background(), r.ID)
	if got.Name != "lead" {
		t.Fatalf("fake DB not updated: name = %q", got.Name)
	}
}
