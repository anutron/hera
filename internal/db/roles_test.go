package db

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Tests for archive / unarchive / rename DAO methods on RolesDAO and for
// the new active-only default of List / Create / GetByOrchestratorAndName.

func roleFixture(t *testing.T, d *DB, orchName, roleName string, kind RoleKind, project string) (*Orchestrator, *Role) {
	t.Helper()
	ctx := context.Background()
	orch, err := d.Orchestrators.Create(ctx, orchName)
	if err != nil {
		t.Fatalf("Orchestrators.Create: %v", err)
	}
	role, err := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID,
		Name:           roleName,
		Kind:           kind,
		ArgusProject:   project,
		Mission:        "ship F",
		Constraints:    "ship by friday",
	})
	if err != nil {
		t.Fatalf("Roles.Create: %v", err)
	}
	return orch, role
}

func TestRoles_Archive_SetsArchivedAtAndPreservesIdentity(t *testing.T) {
	// Spec scenario: "Archive role preserves identity columns".
	ctx := context.Background()
	d := openTestDB(t)
	_, role := roleFixture(t, d, "foo", "w1", KindWorker, "foo-frontend")

	before := time.Now().UTC()
	if err := d.Roles.Archive(ctx, role.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	got, err := d.Roles.GetByID(ctx, role.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ArchivedAt == nil {
		t.Fatalf("archived_at nil after Archive")
	}
	if got.ArchivedAt.Before(before.Add(-time.Second)) || got.ArchivedAt.After(time.Now().UTC().Add(time.Second)) {
		t.Fatalf("archived_at = %v, expected near now", got.ArchivedAt)
	}
	if got.Mission != "ship F" || got.Constraints != "ship by friday" || got.ArgusProject != "foo-frontend" {
		t.Fatalf("identity columns mutated: %+v", got)
	}
}

func TestRoles_Unarchive_ClearsArchivedAt(t *testing.T) {
	// Spec scenario: "Unarchive role clears archived_at".
	ctx := context.Background()
	d := openTestDB(t)
	_, role := roleFixture(t, d, "foo", "w1", KindWorker, "p")
	if err := d.Roles.Archive(ctx, role.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if err := d.Roles.Unarchive(ctx, role.ID); err != nil {
		t.Fatalf("Unarchive: %v", err)
	}
	got, _ := d.Roles.GetByID(ctx, role.ID)
	if got.ArchivedAt != nil {
		t.Fatalf("archived_at non-nil after Unarchive: %v", got.ArchivedAt)
	}
}

func TestRoles_Archive_IdempotentTimestamp(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	_, role := roleFixture(t, d, "foo", "w1", KindWorker, "p")
	if err := d.Roles.Archive(ctx, role.ID); err != nil {
		t.Fatalf("first Archive: %v", err)
	}
	first, _ := d.Roles.GetByID(ctx, role.ID)
	original := *first.ArchivedAt

	time.Sleep(10 * time.Millisecond)
	if err := d.Roles.Archive(ctx, role.ID); err != nil {
		t.Fatalf("re-Archive: %v", err)
	}
	again, _ := d.Roles.GetByID(ctx, role.ID)
	if !again.ArchivedAt.Equal(original) {
		t.Fatalf("re-Archive changed archived_at: was %v, now %v", original, again.ArchivedAt)
	}
}

func TestRoles_Archive_NonExistentReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	err := d.Roles.Archive(ctx, 9999)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Archive(missing) = %v, want ErrNotFound", err)
	}
}

func TestRoles_Unarchive_NonExistentReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	err := d.Roles.Unarchive(ctx, 9999)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Unarchive(missing) = %v, want ErrNotFound", err)
	}
}

func TestRoles_Rename_UpdatesNameOnly(t *testing.T) {
	// Spec scenario: "Rename role updates only the name column".
	ctx := context.Background()
	d := openTestDB(t)
	_, role := roleFixture(t, d, "foo", "w1", KindWorker, "p")
	originalCreated := role.CreatedAt

	if err := d.Roles.Rename(ctx, role.ID, "lead"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	got, err := d.Roles.GetByID(ctx, role.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "lead" {
		t.Fatalf("name = %q, want %q", got.Name, "lead")
	}
	if !got.CreatedAt.Equal(originalCreated) {
		t.Fatalf("created_at changed: was %v, now %v", originalCreated, got.CreatedAt)
	}
	if got.Mission != "ship F" || got.Constraints != "ship by friday" || got.ArgusProject != "p" {
		t.Fatalf("identity columns mutated: %+v", got)
	}
}

func TestRoles_Rename_RejectsActiveSiblingDuplicate(t *testing.T) {
	// Spec scenario: "Rename role to existing non-archived sibling name rejected".
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")
	_, _ = d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coord", Kind: KindCoordinator, ArgusProject: "p",
	})
	w, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w1", Kind: KindWorker, ArgusProject: "p",
	})

	err := d.Roles.Rename(ctx, w.ID, "coord")
	if !errors.Is(err, ErrNameConflict) {
		t.Fatalf("Rename to active sibling = %v, want ErrNameConflict", err)
	}
	got, _ := d.Roles.GetByID(ctx, w.ID)
	if got.Name != "w1" {
		t.Fatalf("name after rejected rename = %q", got.Name)
	}
}

func TestRoles_Rename_AllowsSameNameAcrossOrchestrators(t *testing.T) {
	// Spec scenario: "Same role name allowed across different orchestrators".
	ctx := context.Background()
	d := openTestDB(t)
	orch1, _ := d.Orchestrators.Create(ctx, "foo")
	orch2, _ := d.Orchestrators.Create(ctx, "bar")
	_, _ = d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch1.ID, Name: "coord", Kind: KindCoordinator, ArgusProject: "p",
	})
	w, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch2.ID, Name: "w1", Kind: KindWorker, ArgusProject: "p",
	})

	if err := d.Roles.Rename(ctx, w.ID, "coord"); err != nil {
		t.Fatalf("Rename across orchestrators: %v", err)
	}
}

func TestRoles_Rename_AllowedAcrossArchivedSibling(t *testing.T) {
	// Active role under foo can take the name of an archived sibling.
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")
	archived, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "lead", Kind: KindWorker, ArgusProject: "p",
	})
	if err := d.Roles.Archive(ctx, archived.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	w, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w1", Kind: KindWorker, ArgusProject: "p",
	})

	if err := d.Roles.Rename(ctx, w.ID, "lead"); err != nil {
		t.Fatalf("Rename to archived sibling's name: %v", err)
	}
	gotArchived, _ := d.Roles.GetByID(ctx, archived.ID)
	if gotArchived.Name != "lead" {
		t.Fatalf("archived sibling name changed: %q", gotArchived.Name)
	}
	if gotArchived.ArchivedAt == nil {
		t.Fatalf("archived sibling should still be archived")
	}
}

func TestRoles_Rename_NonExistentReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	err := d.Roles.Rename(ctx, 9999, "anything")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Rename(missing) = %v, want ErrNotFound", err)
	}
}

func TestRoles_GetByID_ResolvesArchivedRow(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	_, role := roleFixture(t, d, "foo", "w1", KindWorker, "p")
	if err := d.Roles.Archive(ctx, role.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	got, err := d.Roles.GetByID(ctx, role.ID)
	if err != nil {
		t.Fatalf("GetByID on archived: %v", err)
	}
	if got.ArchivedAt == nil {
		t.Fatalf("expected archived row")
	}
}

func TestRoles_GetByOrchestratorAndName_FiltersArchived(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orch, role := roleFixture(t, d, "foo", "w1", KindWorker, "p")
	if err := d.Roles.Archive(ctx, role.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	_, err := d.Roles.GetByOrchestratorAndName(ctx, orch.ID, "w1")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetByOrchestratorAndName on archived = %v, want ErrNotFound", err)
	}
}

func TestRoles_ListByOrchestrator_FiltersArchivedByDefault(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")
	active, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w1", Kind: KindWorker, ArgusProject: "p",
	})
	archived, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w2", Kind: KindWorker, ArgusProject: "p",
	})
	if err := d.Roles.Archive(ctx, archived.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}

	list, err := d.Roles.ListByOrchestrator(ctx, orch.ID)
	if err != nil {
		t.Fatalf("ListByOrchestrator: %v", err)
	}
	if len(list) != 1 || list[0].ID != active.ID {
		t.Fatalf("ListByOrchestrator returned %+v", list)
	}
}

func TestRoles_ListByOrchestratorInclusive_ReturnsAll(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")
	_, _ = d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w1", Kind: KindWorker, ArgusProject: "p",
	})
	archived, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w2", Kind: KindWorker, ArgusProject: "p",
	})
	if err := d.Roles.Archive(ctx, archived.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}

	list, err := d.Roles.ListByOrchestratorInclusive(ctx, orch.ID)
	if err != nil {
		t.Fatalf("ListByOrchestratorInclusive: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("ListByOrchestratorInclusive len = %d, want 2", len(list))
	}
}

func TestRoles_Create_ArchivedSameNameDoesNotBlock(t *testing.T) {
	// Same-name archived role does NOT block creation of a fresh active role.
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")
	first, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w1", Kind: KindWorker, ArgusProject: "first",
	})
	if err := d.Roles.Archive(ctx, first.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}

	second, err := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w1", Kind: KindWorker, ArgusProject: "second",
	})
	if err != nil {
		t.Fatalf("Create after archive of same-name role: %v", err)
	}
	if second.ID == first.ID {
		t.Fatalf("Create returned the archived row instead of a fresh one")
	}
	if second.ArchivedAt != nil {
		t.Fatalf("second row should be active")
	}
	if second.ArgusProject != "second" {
		t.Fatalf("argus_project = %q, want 'second'", second.ArgusProject)
	}
	gotFirst, _ := d.Roles.GetByID(ctx, first.ID)
	if gotFirst.ArchivedAt == nil {
		t.Fatalf("first row should still be archived")
	}
	if gotFirst.ArgusProject != "first" {
		t.Fatalf("archived row's argus_project changed: %q", gotFirst.ArgusProject)
	}
}
