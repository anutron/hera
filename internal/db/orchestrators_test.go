package db

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Tests for the archive / unarchive / rename DAO methods on
// OrchestratorsDAO and for the new active-only default of List / Create
// / GetByName.

func TestOrchestrators_Archive_SetsArchivedAt(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")

	before := time.Now().UTC()
	if err := d.Orchestrators.Archive(ctx, orch.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	got, err := d.Orchestrators.GetByID(ctx, orch.ID)
	if err != nil {
		t.Fatalf("GetByID after Archive: %v", err)
	}
	if got.ArchivedAt == nil {
		t.Fatalf("archived_at should be non-nil after Archive")
	}
	if got.ArchivedAt.Before(before.Add(-time.Second)) || got.ArchivedAt.After(time.Now().UTC().Add(time.Second)) {
		t.Fatalf("archived_at = %v, want within the last second", got.ArchivedAt)
	}
}

func TestOrchestrators_Unarchive_ClearsArchivedAt(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")
	if err := d.Orchestrators.Archive(ctx, orch.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}

	if err := d.Orchestrators.Unarchive(ctx, orch.ID); err != nil {
		t.Fatalf("Unarchive: %v", err)
	}
	got, err := d.Orchestrators.GetByID(ctx, orch.ID)
	if err != nil {
		t.Fatalf("GetByID after Unarchive: %v", err)
	}
	if got.ArchivedAt != nil {
		t.Fatalf("archived_at should be nil after Unarchive, got %v", got.ArchivedAt)
	}
}

func TestOrchestrators_Archive_PreservesIdempotencyTimestamp(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")
	if err := d.Orchestrators.Archive(ctx, orch.ID); err != nil {
		t.Fatalf("first Archive: %v", err)
	}
	firstSnap, _ := d.Orchestrators.GetByID(ctx, orch.ID)
	original := *firstSnap.ArchivedAt

	time.Sleep(10 * time.Millisecond)
	if err := d.Orchestrators.Archive(ctx, orch.ID); err != nil {
		t.Fatalf("re-Archive: %v", err)
	}
	again, _ := d.Orchestrators.GetByID(ctx, orch.ID)
	if !again.ArchivedAt.Equal(original) {
		t.Fatalf("re-Archive changed archived_at: was %v, now %v", original, again.ArchivedAt)
	}
}

func TestOrchestrators_Archive_NonExistentReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	err := d.Orchestrators.Archive(ctx, 9999)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Archive(missing) = %v, want ErrNotFound", err)
	}
}

func TestOrchestrators_Unarchive_NonExistentReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	err := d.Orchestrators.Unarchive(ctx, 9999)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Unarchive(missing) = %v, want ErrNotFound", err)
	}
}

func TestOrchestrators_Unarchive_AlreadyActiveIsNoOp(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")
	if err := d.Orchestrators.Unarchive(ctx, orch.ID); err != nil {
		t.Fatalf("Unarchive on already-active should be no-op, got %v", err)
	}
}

func TestOrchestrators_Rename_UpdatesNameOnly(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")
	originalCreated := orch.CreatedAt

	if err := d.Orchestrators.Rename(ctx, orch.ID, "bar"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	got, err := d.Orchestrators.GetByID(ctx, orch.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "bar" {
		t.Fatalf("name = %q, want %q", got.Name, "bar")
	}
	if !got.CreatedAt.Equal(originalCreated) {
		t.Fatalf("created_at changed: was %v, now %v", originalCreated, got.CreatedAt)
	}
}

func TestOrchestrators_Rename_RejectsActiveDuplicate(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	_, _ = d.Orchestrators.Create(ctx, "foo")
	bar, _ := d.Orchestrators.Create(ctx, "bar")

	err := d.Orchestrators.Rename(ctx, bar.ID, "foo")
	if !errors.Is(err, ErrNameConflict) {
		t.Fatalf("Rename to active duplicate = %v, want ErrNameConflict", err)
	}
	// Confirm bar row's name is unchanged.
	got, _ := d.Orchestrators.GetByID(ctx, bar.ID)
	if got.Name != "bar" {
		t.Fatalf("name after rejected rename = %q, want %q", got.Name, "bar")
	}
}

func TestOrchestrators_Rename_AllowedToArchivedName(t *testing.T) {
	// Spec scenario: "Rename to name of archived orchestrator allowed".
	ctx := context.Background()
	d := openTestDB(t)
	foo, _ := d.Orchestrators.Create(ctx, "foo")
	if err := d.Orchestrators.Archive(ctx, foo.ID); err != nil {
		t.Fatalf("Archive foo: %v", err)
	}
	bar, _ := d.Orchestrators.Create(ctx, "bar")

	if err := d.Orchestrators.Rename(ctx, bar.ID, "foo"); err != nil {
		t.Fatalf("Rename bar -> foo (archived foo exists): %v", err)
	}
	gotBar, _ := d.Orchestrators.GetByID(ctx, bar.ID)
	if gotBar.Name != "foo" {
		t.Fatalf("bar.name = %q, want %q", gotBar.Name, "foo")
	}
	gotFoo, _ := d.Orchestrators.GetByID(ctx, foo.ID)
	if gotFoo.Name != "foo" {
		t.Fatalf("archived foo.name changed: %q", gotFoo.Name)
	}
	if gotFoo.ArchivedAt == nil {
		t.Fatalf("archived foo should still be archived")
	}
}

func TestOrchestrators_Rename_SelfRenameIsNoop(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")
	if err := d.Orchestrators.Rename(ctx, orch.ID, "foo"); err != nil {
		t.Fatalf("self-rename: %v", err)
	}
}

func TestOrchestrators_Rename_NonExistentReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	err := d.Orchestrators.Rename(ctx, 9999, "anything")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Rename(missing) = %v, want ErrNotFound", err)
	}
}

func TestOrchestrators_GetByID_ResolvesArchivedRow(t *testing.T) {
	// Spec scenario: "Get by id resolves archived row".
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")
	if err := d.Orchestrators.Archive(ctx, orch.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	got, err := d.Orchestrators.GetByID(ctx, orch.ID)
	if err != nil {
		t.Fatalf("GetByID on archived row: %v", err)
	}
	if got.ArchivedAt == nil {
		t.Fatalf("expected archived row, archived_at nil")
	}
}

func TestOrchestrators_GetByName_FiltersArchived(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")
	if err := d.Orchestrators.Archive(ctx, orch.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	_, err := d.Orchestrators.GetByName(ctx, "foo")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetByName on archived row = %v, want ErrNotFound", err)
	}
}

func TestOrchestrators_List_FiltersArchivedByDefault(t *testing.T) {
	// Spec scenario: "ListActive returns non-archived only".
	ctx := context.Background()
	d := openTestDB(t)
	active, _ := d.Orchestrators.Create(ctx, "active")
	archived, _ := d.Orchestrators.Create(ctx, "archived")
	if err := d.Orchestrators.Archive(ctx, archived.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}

	list, err := d.Orchestrators.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List len = %d, want 1", len(list))
	}
	if list[0].ID != active.ID {
		t.Fatalf("List returned %d, want %d", list[0].ID, active.ID)
	}
}

func TestOrchestrators_ListInclusive_ReturnsAll(t *testing.T) {
	// Spec scenario: "ListInclusive returns all rows".
	ctx := context.Background()
	d := openTestDB(t)
	_, _ = d.Orchestrators.Create(ctx, "active")
	archived, _ := d.Orchestrators.Create(ctx, "archived")
	if err := d.Orchestrators.Archive(ctx, archived.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}

	list, err := d.Orchestrators.ListInclusive(ctx)
	if err != nil {
		t.Fatalf("ListInclusive: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("ListInclusive len = %d, want 2", len(list))
	}
}

func TestOrchestrators_Create_ArchivedSameNameDoesNotBlock(t *testing.T) {
	// Spec scenario: "Archived orchestrator with same name does not block creation".
	ctx := context.Background()
	d := openTestDB(t)
	first, _ := d.Orchestrators.Create(ctx, "foo")
	if err := d.Orchestrators.Archive(ctx, first.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}

	second, err := d.Orchestrators.Create(ctx, "foo")
	if err != nil {
		t.Fatalf("Create after archive of same-name row: %v", err)
	}
	if second.ID == first.ID {
		t.Fatalf("Create returned the archived row instead of a fresh one")
	}
	if second.ArchivedAt != nil {
		t.Fatalf("second row should be active, got archived_at = %v", second.ArchivedAt)
	}
	// The archived row should still exist and be unchanged.
	gotFirst, err := d.Orchestrators.GetByID(ctx, first.ID)
	if err != nil {
		t.Fatalf("GetByID(first): %v", err)
	}
	if gotFirst.ArchivedAt == nil {
		t.Fatalf("first row should still be archived")
	}
}

func TestOrchestrators_Rename_ScansArchivedAtAndAttachesPointer(t *testing.T) {
	// Defense: scanFromRows must populate ArchivedAt even on the List path.
	ctx := context.Background()
	d := openTestDB(t)
	a, _ := d.Orchestrators.Create(ctx, "active")
	archived, _ := d.Orchestrators.Create(ctx, "archived")
	if err := d.Orchestrators.Archive(ctx, archived.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}

	list, err := d.Orchestrators.ListInclusive(ctx)
	if err != nil {
		t.Fatalf("ListInclusive: %v", err)
	}
	for _, o := range list {
		switch o.ID {
		case a.ID:
			if o.ArchivedAt != nil {
				t.Fatalf("active row should have archived_at nil, got %v", o.ArchivedAt)
			}
		case archived.ID:
			if o.ArchivedAt == nil {
				t.Fatalf("archived row should have archived_at non-nil")
			}
		}
	}
}
