package ops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	arguspkg "github.com/anutron/hera/internal/argus"
)

// makeSimpleTempDir creates a temp directory that looks like a healthy linked
// worktree: a .git FILE pointing at an admin entry that actually exists on
// disk. removeWorktree skips paths that are missing, have no .git file, OR
// whose .git points at a now-gone admin entry (BUG-018), so all three
// conditions must be satisfied for the fake remover to receive the call.
func makeSimpleTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// A real admin entry so worktreeAdminEntryExists reports the worktree is
	// still attached (the healthy case the remover should act on).
	adminDir := filepath.Join(t.TempDir(), "worktrees", "linked")
	if err := os.MkdirAll(adminDir, 0o755); err != nil {
		t.Fatalf("makeSimpleTempDir: mkdir admin entry: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: "+adminDir+"\n"), 0o644); err != nil {
		t.Fatalf("makeSimpleTempDir: write .git: %v", err)
	}
	return dir
}

// makeStaleWorktreeDir creates a worktree directory whose .git FILE points at
// an admin entry that does NOT exist — the BUG-018 state left behind when an
// earlier cleanup pruned .git/worktrees/<name> but the worktree dir lingered.
// `git worktree remove` would exit 128 on such a dir, so removeWorktree must
// soft-skip it.
func makeStaleWorktreeDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	missingAdmin := filepath.Join(t.TempDir(), "worktrees", "gone")
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: "+missingAdmin+"\n"), 0o644); err != nil {
		t.Fatalf("makeStaleWorktreeDir: write .git: %v", err)
	}
	return dir
}

func TestListCompletedAgents_OnlyCompleted(t *testing.T) {
	db := newFakeDB()
	orch := db.seedOrchestrator("foo", false)
	done := db.seedRole(orch.ID, "done-1", KindWorker, "foo", false)
	working := db.seedRole(orch.ID, "working-1", KindWorker, "foo", false)
	pending := db.seedRole(orch.ID, "pending-1", KindWorker, "foo", false)
	db.seedBinding(done.ID, "Tdone", "")
	db.seedBinding(working.ID, "Twork", "")
	db.seedBinding(pending.ID, "Tpend", "")

	a := &fakeArgus{statuses: map[string]string{
		"Tdone": "complete",
		"Twork": "in_progress",
		"Tpend": "pending",
	}}
	s := NewService(db, a, &fakeWorktreeRemover{}, &fakeLogger{})

	got, err := s.ListCompletedAgents(context.Background())
	if err != nil {
		t.Fatalf("ListCompletedAgents: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want exactly 1 completed agent, got %d: %+v", len(got), got)
	}
	if got[0].ArgusTaskID != "Tdone" || got[0].Name != "done-1" {
		t.Fatalf("completed agent = %+v, want Tdone/done-1", got[0])
	}
}

func TestPruneCompleted_DestroysOnlyGivenAgents(t *testing.T) {
	db := newFakeDB()
	orch := db.seedOrchestrator("foo", false)
	done := db.seedRole(orch.ID, "done-1", KindWorker, "foo", false)
	working := db.seedRole(orch.ID, "working-1", KindWorker, "foo", false)
	db.seedBinding(done.ID, "Tdone", "")
	wbnd := db.seedBinding(working.ID, "Twork", "")

	a := &fakeArgus{statuses: map[string]string{"Tdone": "complete", "Twork": "in_progress"}}
	s := NewService(db, a, &fakeWorktreeRemover{}, &fakeLogger{})

	completed, err := s.ListCompletedAgents(context.Background())
	if err != nil {
		t.Fatalf("ListCompletedAgents: %v", err)
	}
	n, err := s.PruneCompleted(context.Background(), completed)
	if err != nil {
		t.Fatalf("PruneCompleted: %v", err)
	}
	if n != 1 {
		t.Fatalf("pruned = %d, want 1", n)
	}
	// Only the completed task destroyed.
	if len(a.deleteCalls) != 1 || a.deleteCalls[0] != "Tdone" {
		t.Fatalf("argus DeleteTask calls = %v, want [Tdone]", a.deleteCalls)
	}
	// Completed role archived; binding ended.
	doneRole, _ := db.GetRoleByID(context.Background(), done.ID)
	if !doneRole.Archived {
		t.Fatalf("completed role should be archived")
	}
	// Working agent untouched: still live, not archived.
	workRole, _ := db.GetRoleByID(context.Background(), working.ID)
	if workRole.Archived {
		t.Fatalf("working role must NOT be archived")
	}
	if _, err := db.GetLiveBindingByRole(context.Background(), working.ID); err != nil {
		t.Fatalf("working binding %d should still be live", wbnd.ID)
	}
}

func TestPruneCompleted_EmptyList_NoDestruction(t *testing.T) {
	db := newFakeDB()
	a := &fakeArgus{}
	s := NewService(db, a, &fakeWorktreeRemover{}, &fakeLogger{})

	n, err := s.PruneCompleted(context.Background(), nil)
	if err != nil {
		t.Fatalf("PruneCompleted(nil): %v", err)
	}
	if n != 0 || len(a.deleteCalls) != 0 {
		t.Fatalf("empty prune must destroy nothing; n=%d calls=%v", n, a.deleteCalls)
	}
}

// Spec (live-coord-never-complete): ListCompletedAgents MUST NOT include
// coordinator roles even when their argus task reports "complete". A sibling
// worker with a complete task MUST still be listed (guard is coord-specific).
// --- PruneArchivedRole ---

func TestPruneArchivedRole_RemovesRowAndWorktree(t *testing.T) {
	s, db, _, wt, _ := newTestService()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", true)
	// Use a real directory so removeWorktree passes the path to WorktreeRemover.
	wtPath := makeSimpleTempDir(t)
	db.seedEndedBinding(role.ID, "T1", wtPath)

	if err := s.PruneArchivedRole(context.Background(), role.ID); err != nil {
		t.Fatalf("PruneArchivedRole: %v", err)
	}
	// Role physically deleted from DB.
	if _, err := db.GetRoleByID(context.Background(), role.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("role should be gone from DB after prune, got err=%v", err)
	}
	// Worktree removal attempted with the binding's path.
	if len(wt.calls) != 1 || wt.calls[0] != wtPath {
		t.Fatalf("worktree remove calls = %v, want [%s]", wt.calls, wtPath)
	}
}

func TestPruneArchivedRole_NoBinding_SkipsWorktree(t *testing.T) {
	s, db, _, wt, _ := newTestService()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", true)
	// No binding seeded.

	if err := s.PruneArchivedRole(context.Background(), role.ID); err != nil {
		t.Fatalf("PruneArchivedRole with no binding: %v", err)
	}
	if _, err := db.GetRoleByID(context.Background(), role.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("role should be gone from DB")
	}
	if len(wt.calls) != 0 {
		t.Fatalf("no worktree should be attempted when binding is absent, got calls=%v", wt.calls)
	}
}

func TestPruneArchivedRole_NotArchived_ReturnsError(t *testing.T) {
	s, db, _, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", false) // NOT archived

	err := s.PruneArchivedRole(context.Background(), role.ID)
	if err == nil {
		t.Fatalf("expected error pruning a non-archived role")
	}
}

func TestPruneArchivedRole_NotFound_ReturnsErrNotFound(t *testing.T) {
	s, _, _, _, _ := newTestService()

	err := s.PruneArchivedRole(context.Background(), 999)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for unknown role, got: %v", err)
	}
}

// --- CompleteArchivedDescendants with prune ---

func TestCompleteArchivedDescendants_CompletesAndPrunesArchivedWorkers(t *testing.T) {
	s, db, argus, wt, _ := newTestService()
	orch := db.seedOrchestrator("proj", false)
	coord := db.seedRole(orch.ID, "coord", KindCoordinator, "proj", true)
	w1 := db.seedRole(orch.ID, "w1", KindWorker, "proj", true)
	w2 := db.seedRole(orch.ID, "w2", KindWorker, "proj", false) // still active
	w1Path := makeSimpleTempDir(t)
	db.seedEndedBinding(coord.ID, "Tcoord", "")
	db.seedEndedBinding(w1.ID, "Tw1", w1Path)
	db.seedBinding(w2.ID, "Tw2", "")

	summary, err := s.CompleteArchivedDescendants(context.Background(), orch.ID)
	if err != nil {
		t.Fatalf("CompleteArchivedDescendants: %v", err)
	}
	if summary.Pruned != 1 {
		t.Fatalf("completed+pruned = %d, want 1", summary.Pruned)
	}
	// w1 completed in argus.
	if len(argus.setStatusCalls) != 1 || argus.setStatusCalls[0].TaskID != "Tw1" || argus.setStatusCalls[0].Status != "complete" {
		t.Fatalf("SetTaskStatus calls = %+v, want Tw1→complete", argus.setStatusCalls)
	}
	// BUG-029: the underlying argus task is DELETED so it never resurfaces as a
	// freelancer. Only w1's task (the archived worker) is destroyed; the active
	// w2 and the coord are left alone.
	if len(argus.deleteCalls) != 1 || argus.deleteCalls[0] != "Tw1" {
		t.Fatalf("argus DeleteTask calls = %v, want [Tw1]", argus.deleteCalls)
	}
	// w1 physically deleted.
	if _, err := db.GetRoleByID(context.Background(), w1.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("w1 should be gone from DB after prune")
	}
	// Worktree removed for w1 only (coord and w2 use empty paths → skipped).
	if len(wt.calls) != 1 || wt.calls[0] != w1Path {
		t.Fatalf("worktree calls = %v, want [%s]", wt.calls, w1Path)
	}
	// Coord and w2 untouched.
	if _, err := db.GetRoleByID(context.Background(), coord.ID); err != nil {
		t.Fatalf("coord should still exist")
	}
	if _, err := db.GetRoleByID(context.Background(), w2.ID); err != nil {
		t.Fatalf("w2 (active) should still exist")
	}
}

func TestCompleteArchivedDescendants_PrunedArgusTask_StillPrunesHera(t *testing.T) {
	// If the argus task is already pruned (404), the hera row + worktree
	// should still be cleaned up.
	s, db, argus, wt, _ := newTestService()
	orch := db.seedOrchestrator("proj", false)
	w1 := db.seedRole(orch.ID, "w1", KindWorker, "proj", true)
	prunedPath := makeSimpleTempDir(t)
	db.seedEndedBinding(w1.ID, "PRUNED", prunedPath)
	// SetTaskStatus returns ErrArgusTaskGone for the pruned task.
	argus.setStatusErr = fmt.Errorf("argus task gone: %w", ErrArgusTaskGone)

	summary, err := s.CompleteArchivedDescendants(context.Background(), orch.ID)
	if err != nil {
		t.Fatalf("CompleteArchivedDescendants: %v", err)
	}
	if summary.Pruned != 1 {
		t.Fatalf("Pruned = %d, want 1 (hera row pruned even though argus task was gone)", summary.Pruned)
	}
	if _, err := db.GetRoleByID(context.Background(), w1.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("w1 should be gone from DB even when argus task was pruned")
	}
	if len(wt.calls) != 1 {
		t.Fatalf("worktree should still be removed, calls=%v", wt.calls)
	}
}

// BUG-018: a worktree dir that exists but whose git admin entry was already
// pruned (`.git` points at a missing gitdir) must be soft-skipped — `git
// worktree remove` would exit 128. The role row is still deleted, no error.
func TestPruneArchivedRole_StaleAdminEntryIsSoftNoop(t *testing.T) {
	s, db, _, wt, logger := newTestService()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", true)
	stale := makeStaleWorktreeDir(t)
	db.seedEndedBinding(role.ID, "T1", stale)

	if err := s.PruneArchivedRole(context.Background(), role.ID); err != nil {
		t.Fatalf("PruneArchivedRole over stale worktree: %v", err)
	}
	// git must NOT be invoked — it would exit 128.
	if len(wt.calls) != 0 {
		t.Fatalf("WorktreeRemover should not fire on a detached worktree: %v", wt.calls)
	}
	// Role row gone regardless.
	if _, err := db.GetRoleByID(context.Background(), role.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("role should be pruned from DB even with a stale worktree")
	}
	found := false
	for _, m := range logger.messages {
		if strings.Contains(m, "already detached") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected audit log for detached-worktree skip, got %v", logger.messages)
	}
}

// BUG-023: `C` over a coordinator whose archived workers are ALL already
// complete must prune every one — WITHOUT re-issuing a redundant status write
// (argus may reject a no-op complete→complete transition). The old behavior
// excluded already-complete workers from the work list and short-circuited.
func TestCompleteArchivedDescendants_AllAlreadyComplete_PrunesWithoutReCompleting(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("proj", false)
	db.seedRole(orch.ID, "coord", KindCoordinator, "proj", true)

	const n = 4
	var roleIDs []int64
	argus.statuses = map[string]string{}
	for i := 0; i < n; i++ {
		r := db.seedRole(orch.ID, fmt.Sprintf("w%d", i), KindWorker, "proj", true)
		roleIDs = append(roleIDs, r.ID)
		taskID := fmt.Sprintf("T%d", i)
		db.seedEndedBinding(r.ID, taskID, makeSimpleTempDir(t))
		argus.statuses[taskID] = "complete" // already :checked:
	}

	summary, err := s.CompleteArchivedDescendants(context.Background(), orch.ID)
	if err != nil {
		t.Fatalf("sweep over already-complete workers must not error: %v", err)
	}
	if summary.Found != n {
		t.Fatalf("Found = %d, want %d (all archived descendants are prune candidates)", summary.Found, n)
	}
	if summary.Pruned != n {
		t.Fatalf("Pruned = %d, want %d (already-complete workers still pruned)", summary.Pruned, n)
	}
	// No status writes: every worker was already complete.
	if len(argus.setStatusCalls) != 0 {
		t.Fatalf("SetTaskStatus must not fire for already-complete workers; got %+v", argus.setStatusCalls)
	}
	for _, id := range roleIDs {
		if _, err := db.GetRoleByID(context.Background(), id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("role %d should be pruned from DB", id)
		}
	}
}

// BUG-023: a mixed fleet — some archived workers complete, some not — completes
// only the incomplete ones and prunes ALL of them.
func TestCompleteArchivedDescendants_MixedCompletion_CompletesOnlyIncomplete(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("proj", false)

	done := db.seedRole(orch.ID, "done", KindWorker, "proj", true)
	todo := db.seedRole(orch.ID, "todo", KindWorker, "proj", true)
	db.seedEndedBinding(done.ID, "Tdone", makeSimpleTempDir(t))
	db.seedEndedBinding(todo.ID, "Ttodo", makeSimpleTempDir(t))
	argus.statuses = map[string]string{"Tdone": "complete", "Ttodo": "in_progress"}

	summary, err := s.CompleteArchivedDescendants(context.Background(), orch.ID)
	if err != nil {
		t.Fatalf("CompleteArchivedDescendants: %v", err)
	}
	if summary.Found != 2 || summary.Pruned != 2 {
		t.Fatalf("Found/Pruned = %d/%d, want 2/2", summary.Found, summary.Pruned)
	}
	// Only the incomplete worker is completed.
	if len(argus.setStatusCalls) != 1 || argus.setStatusCalls[0].TaskID != "Ttodo" || argus.setStatusCalls[0].Status != "complete" {
		t.Fatalf("SetTaskStatus calls = %+v, want only Ttodo→complete", argus.setStatusCalls)
	}
	for _, id := range []int64{done.ID, todo.ID} {
		if _, err := db.GetRoleByID(context.Background(), id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("role %d should be pruned", id)
		}
	}
}

// BUG-023: a coordinator with NO archived descendants reports Found==0 so the
// view can fire "nothing to do" — distinct from a coordinator whose archived
// workers were merely already complete (Found>0, pruned).
func TestCompleteArchivedDescendants_NoArchivedDescendants_FoundZero(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("proj", false)
	db.seedRole(orch.ID, "coord", KindCoordinator, "proj", true) // archived coord is skipped
	active := db.seedRole(orch.ID, "w1", KindWorker, "proj", false)
	db.seedBinding(active.ID, "Tw1", "")

	summary, err := s.CompleteArchivedDescendants(context.Background(), orch.ID)
	if err != nil {
		t.Fatalf("CompleteArchivedDescendants: %v", err)
	}
	if summary.Found != 0 {
		t.Fatalf("Found = %d, want 0 (no archived descendants)", summary.Found)
	}
	if summary.Pruned != 0 {
		t.Fatalf("Pruned = %d, want 0", summary.Pruned)
	}
	if len(argus.setStatusCalls) != 0 {
		t.Fatalf("no status writes expected; got %+v", argus.setStatusCalls)
	}
	// The active worker and coord are untouched.
	if _, err := db.GetRoleByID(context.Background(), active.ID); err != nil {
		t.Fatalf("active worker should still exist: %v", err)
	}
}

// BUG-018: even a genuine worktree-removal failure must not block pruning the
// DB row. PruneArchivedRole logs the failure and deletes the row anyway.
func TestPruneArchivedRole_WorktreeRemoveError_StillPrunesRow(t *testing.T) {
	s, db, _, wt, _ := newTestService()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", true)
	wtPath := makeSimpleTempDir(t) // healthy fixture → remover is invoked
	db.seedEndedBinding(role.ID, "T1", wtPath)
	wt.err = fmt.Errorf("git worktree remove: exit status 128")

	if err := s.PruneArchivedRole(context.Background(), role.ID); err != nil {
		t.Fatalf("PruneArchivedRole should swallow worktree errors: %v", err)
	}
	if len(wt.calls) != 1 {
		t.Fatalf("remover should have been attempted once, got %v", wt.calls)
	}
	if _, err := db.GetRoleByID(context.Background(), role.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("role should be pruned from DB despite worktree remove failure")
	}
}

// BUG-018: the headline scenario — `C` over a coordinator whose archived
// workers ALL have already-removed/stale worktrees. Every role must clear from
// the rail, the sweep must not abort, and no error is returned.
func TestCompleteArchivedDescendants_StaleWorktrees_NoAbort(t *testing.T) {
	s, db, _, wt, _ := newTestService()
	orch := db.seedOrchestrator("proj", false)
	db.seedRole(orch.ID, "coord", KindCoordinator, "proj", true)

	const n = 5
	var roleIDs []int64
	for i := 0; i < n; i++ {
		r := db.seedRole(orch.ID, fmt.Sprintf("w%d", i), KindWorker, "proj", true)
		roleIDs = append(roleIDs, r.ID)
		// All workers' worktree dirs are gone entirely (the most common case).
		missing := filepath.Join(t.TempDir(), "gone")
		db.seedEndedBinding(r.ID, fmt.Sprintf("T%d", i), missing)
	}

	summary, err := s.CompleteArchivedDescendants(context.Background(), orch.ID)
	if err != nil {
		t.Fatalf("sweep over stale worktrees must not error: %v", err)
	}
	if summary.Pruned != n {
		t.Fatalf("Pruned = %d, want %d (all cleared from rail)", summary.Pruned, n)
	}
	if len(wt.calls) != 0 {
		t.Fatalf("no git removal should run on missing worktrees: %v", wt.calls)
	}
	for _, id := range roleIDs {
		if _, err := db.GetRoleByID(context.Background(), id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("role %d should be pruned from DB", id)
		}
	}
}

// BUG-018: mixed fleet — some worktrees still exist (removed), others are
// stale/gone (soft-skipped). All roles prune; the summary counts the skips.
func TestCompleteArchivedDescendants_MixedWorktrees_Summary(t *testing.T) {
	s, db, _, wt, _ := newTestService()
	orch := db.seedOrchestrator("proj", false)

	live1 := db.seedRole(orch.ID, "live1", KindWorker, "proj", true)
	live2 := db.seedRole(orch.ID, "live2", KindWorker, "proj", true)
	stale1 := db.seedRole(orch.ID, "stale1", KindWorker, "proj", true)
	gone1 := db.seedRole(orch.ID, "gone1", KindWorker, "proj", true)

	live1Path := makeSimpleTempDir(t)
	live2Path := makeSimpleTempDir(t)
	db.seedEndedBinding(live1.ID, "Tl1", live1Path)
	db.seedEndedBinding(live2.ID, "Tl2", live2Path)
	db.seedEndedBinding(stale1.ID, "Ts1", makeStaleWorktreeDir(t))
	db.seedEndedBinding(gone1.ID, "Tg1", filepath.Join(t.TempDir(), "gone"))

	summary, err := s.CompleteArchivedDescendants(context.Background(), orch.ID)
	if err != nil {
		t.Fatalf("mixed sweep must not error: %v", err)
	}
	if summary.Pruned != 4 {
		t.Fatalf("Pruned = %d, want 4", summary.Pruned)
	}
	// Only the two healthy worktrees reach the remover; stale/gone are skipped
	// before git is invoked, so WorktreeSkipped stays 0 (soft-skip, not error).
	if len(wt.calls) != 2 {
		t.Fatalf("remover calls = %v, want the 2 healthy worktrees", wt.calls)
	}
	if summary.WorktreeSkipped != 0 {
		t.Fatalf("WorktreeSkipped = %d, want 0 (guard soft-skips before git)", summary.WorktreeSkipped)
	}
	for _, id := range []int64{live1.ID, live2.ID, stale1.ID, gone1.ID} {
		if _, err := db.GetRoleByID(context.Background(), id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("role %d should be pruned from DB", id)
		}
	}
}

// BUG-018: when a worktree removal genuinely FAILS (not a guarded soft-skip),
// the role is still pruned and the failure is counted in WorktreeSkipped
// rather than aborting the sweep.
func TestCompleteArchivedDescendants_WorktreeRemoveError_CountedNotAborted(t *testing.T) {
	s, db, _, wt, _ := newTestService()
	orch := db.seedOrchestrator("proj", false)
	w1 := db.seedRole(orch.ID, "w1", KindWorker, "proj", true)
	w2 := db.seedRole(orch.ID, "w2", KindWorker, "proj", true)
	db.seedEndedBinding(w1.ID, "Tw1", makeSimpleTempDir(t))
	db.seedEndedBinding(w2.ID, "Tw2", makeSimpleTempDir(t))
	wt.err = fmt.Errorf("git worktree remove: permission denied")

	summary, err := s.CompleteArchivedDescendants(context.Background(), orch.ID)
	if err != nil {
		t.Fatalf("worktree errors must not abort the sweep: %v", err)
	}
	if summary.Pruned != 2 {
		t.Fatalf("Pruned = %d, want 2 (rows cleared despite worktree failure)", summary.Pruned)
	}
	if summary.WorktreeSkipped != 2 {
		t.Fatalf("WorktreeSkipped = %d, want 2", summary.WorktreeSkipped)
	}
	for _, id := range []int64{w1.ID, w2.ID} {
		if _, err := db.GetRoleByID(context.Background(), id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("role %d should be pruned from DB despite worktree failure", id)
		}
	}
}

// BUG-029 (freelancer spray): `C` must DELETE the underlying argus task for
// EVERY archived descendant — complete, incomplete, AND ○ fully-detached.
// Pruning only the hera role row leaves the argus task alive, and it resurfaces
// as a freelancer (the bug). Here all three states clear and all three tasks
// are destroyed argus-side, with no abort.
func TestCompleteArchivedDescendants_DeletesArgusTasks_AllStates(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("proj", false)
	db.seedRole(orch.ID, "coord", KindCoordinator, "proj", true)

	done := db.seedRole(orch.ID, "done", KindWorker, "proj", true)         // complete
	todo := db.seedRole(orch.ID, "todo", KindWorker, "proj", true)         // incomplete
	detached := db.seedRole(orch.ID, "detached", KindWorker, "proj", true) // ○ detached
	db.seedEndedBinding(done.ID, "Tdone", makeSimpleTempDir(t))
	db.seedEndedBinding(todo.ID, "Ttodo", makeSimpleTempDir(t))
	// ○ detached: no live session, worktree gone entirely.
	db.seedEndedBinding(detached.ID, "Tdetached", filepath.Join(t.TempDir(), "gone"))
	argus.statuses = map[string]string{"Tdone": "complete", "Ttodo": "in_progress"}
	// Tdetached has no status entry → GetTaskStatus returns "" (not complete).

	summary, err := s.CompleteArchivedDescendants(context.Background(), orch.ID)
	if err != nil {
		t.Fatalf("sweep over all states must not error: %v", err)
	}
	if summary.Found != 3 || summary.Pruned != 3 || summary.Errors != 0 {
		t.Fatalf("Found/Pruned/Errors = %d/%d/%d, want 3/3/0", summary.Found, summary.Pruned, summary.Errors)
	}
	// Every archived worker's argus task is destroyed — no freelancer spray.
	gotDeletes := map[string]bool{}
	for _, id := range argus.deleteCalls {
		gotDeletes[id] = true
	}
	for _, want := range []string{"Tdone", "Ttodo", "Tdetached"} {
		if !gotDeletes[want] {
			t.Fatalf("argus task %q must be DELETED (else it sprays into freelance); deleteCalls=%v", want, argus.deleteCalls)
		}
	}
	for _, id := range []int64{done.ID, todo.ID, detached.ID} {
		if _, err := db.GetRoleByID(context.Background(), id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("role %d should be pruned from DB", id)
		}
	}
}

// BUG-029 (○ detached, no abort): a fully-detached archived worker whose argus
// delete ERRORS with a worktree-missing failure (the dead-task case BUG-018
// resilience was supposed to cover end-to-end) must NOT halt the sweep. The
// detached role and every sibling still clear; the delete is tolerated as a
// soft skip (not counted as an error).
func TestCompleteArchivedDescendants_DetachedDeleteWorktreeMissing_NoAbort(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("proj", false)

	detached := db.seedRole(orch.ID, "detached", KindWorker, "proj", true)
	sibling := db.seedRole(orch.ID, "sibling", KindWorker, "proj", true)
	db.seedEndedBinding(detached.ID, "Tdetached", filepath.Join(t.TempDir(), "gone"))
	db.seedEndedBinding(sibling.ID, "Tsibling", makeSimpleTempDir(t))
	// argus's DELETE for the detached task fails because its worktree is gone
	// (BUG-020 marker) — exactly the failure that used to abort the batch.
	argus.deleteErrByTask = map[string]error{
		"Tdetached": &arguspkg.HTTPError{
			Method: "DELETE", Path: "/api/tasks/Tdetached", StatusCode: 500,
			Body: "worktree path missing: /gone (delete the task or recreate the worktree)",
		},
	}

	summary, err := s.CompleteArchivedDescendants(context.Background(), orch.ID)
	if err != nil {
		t.Fatalf("○ detached delete failure must not abort the sweep: %v", err)
	}
	if summary.Pruned != 2 {
		t.Fatalf("Pruned = %d, want 2 (detached + sibling both cleared)", summary.Pruned)
	}
	// worktree-missing is a clean skip, NOT a counted error.
	if summary.Errors != 0 {
		t.Fatalf("Errors = %d, want 0 (worktree-missing delete is a soft skip)", summary.Errors)
	}
	for _, id := range []int64{detached.ID, sibling.ID} {
		if _, err := db.GetRoleByID(context.Background(), id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("role %d should be pruned from DB despite the detached delete failure", id)
		}
	}
}

// BUG-029: a genuine (non-already-gone) argus delete failure is counted in
// summary.Errors but still does NOT abort the sweep — the hera row is pruned
// regardless and every sibling clears.
func TestCompleteArchivedDescendants_ArgusDeleteError_CountedNotAborted(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("proj", false)
	w1 := db.seedRole(orch.ID, "w1", KindWorker, "proj", true)
	w2 := db.seedRole(orch.ID, "w2", KindWorker, "proj", true)
	db.seedEndedBinding(w1.ID, "Tw1", makeSimpleTempDir(t))
	db.seedEndedBinding(w2.ID, "Tw2", makeSimpleTempDir(t))
	argus.deleteErrByTask = map[string]error{
		"Tw1": fmt.Errorf("argus delete: connection refused"),
	}

	summary, err := s.CompleteArchivedDescendants(context.Background(), orch.ID)
	if err != nil {
		t.Fatalf("argus delete errors must not abort the sweep: %v", err)
	}
	if summary.Pruned != 2 {
		t.Fatalf("Pruned = %d, want 2 (rows cleared despite argus delete failure)", summary.Pruned)
	}
	if summary.Errors != 1 {
		t.Fatalf("Errors = %d, want 1 (Tw1 delete failed)", summary.Errors)
	}
	for _, id := range []int64{w1.ID, w2.ID} {
		if _, err := db.GetRoleByID(context.Background(), id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("role %d should be pruned from DB despite argus delete failure", id)
		}
	}
}

// BUG-031: `C` confirm counts N but prune finds 0 — task-less archived roles
// were excluded from the prune list. The pre-BUG-029 sweep `continue`d past any
// role whose argus task couldn't be resolved/completed (counting it in Found
// but never pruning it), so a coordinator whose archived workers' argus tasks
// were already cleaned out-of-band reported "N found, 0 pruned" → the bridge
// fired "no archived workers to prune" with a non-empty archive. The fix
// (BUG-029 rewrite) prunes the hera row for EVERY archived descendant regardless
// of task-resolvability. This test pins the headline scenario: ALL archived
// workers' argus tasks are already gone (some with an ended binding pointing at
// a 404'd task, one with NO binding at all). Every row must clear, Found must
// equal Pruned, and there must be no error.
func TestCompleteArchivedDescendants_AllTasksGone_PrunesAllCountMatches(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("hera-1.0-ftw", false)
	db.seedRole(orch.ID, "coord", KindCoordinator, "hera-1.0-ftw", true) // skipped

	// Two archived workers whose ended bindings still name an argus task, but
	// that task was pruned out-of-band — argus DELETE returns task-gone (404).
	gone1 := db.seedRole(orch.ID, "gone1", KindWorker, "hera-1.0-ftw", true)
	gone2 := db.seedRole(orch.ID, "gone2", KindWorker, "hera-1.0-ftw", true)
	db.seedEndedBinding(gone1.ID, "Tgone1", makeSimpleTempDir(t))
	db.seedEndedBinding(gone2.ID, "Tgone2", makeSimpleTempDir(t))
	argus.deleteErrByTask = map[string]error{
		"Tgone1": fmt.Errorf("argus task gone: %w", ErrArgusTaskGone),
		"Tgone2": fmt.Errorf("argus task gone: %w", ErrArgusTaskGone),
	}

	// A third archived worker with NO binding at all — resolveBinding returns
	// ErrNotFound, so there is no argus task id to resolve. The pre-fix sweep
	// skipped these too; the fix prunes the hera row anyway. (No prior test
	// covered the never-bound archived role.)
	nobind := db.seedRole(orch.ID, "nobind", KindWorker, "hera-1.0-ftw", true)

	summary, err := s.CompleteArchivedDescendants(context.Background(), orch.ID)
	if err != nil {
		t.Fatalf("sweep over all-task-gone archive must not error: %v", err)
	}
	// The count (Found) and the prune (Pruned) operate on the IDENTICAL list:
	// a 3-count archive prunes exactly 3.
	if summary.Found != 3 || summary.Pruned != 3 {
		t.Fatalf("Found/Pruned = %d/%d, want 3/3 (count must equal pruned)", summary.Found, summary.Pruned)
	}
	// An already-gone argus task is a clean skip, not a counted error.
	if summary.Errors != 0 {
		t.Fatalf("Errors = %d, want 0 (already-gone tasks are clean skips)", summary.Errors)
	}
	for _, id := range []int64{gone1.ID, gone2.ID, nobind.ID} {
		if _, err := db.GetRoleByID(context.Background(), id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("role %d should be pruned from DB even though its argus task was gone", id)
		}
	}
}

// BUG-031: a mixed archive — one worker's argus task is still resolvable, one's
// is already gone (404 on delete), one has no binding at all — must prune ALL
// three. No role is excluded on task-resolvability grounds; Found == Pruned.
func TestCompleteArchivedDescendants_MixedTaskResolvability_AllPruned(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("hera-1.0-ftw", false)

	live := db.seedRole(orch.ID, "live", KindWorker, "hera-1.0-ftw", true)
	gone := db.seedRole(orch.ID, "gone", KindWorker, "hera-1.0-ftw", true)
	nobind := db.seedRole(orch.ID, "nobind", KindWorker, "hera-1.0-ftw", true)
	db.seedEndedBinding(live.ID, "Tlive", makeSimpleTempDir(t))
	db.seedEndedBinding(gone.ID, "Tgone", makeSimpleTempDir(t))
	argus.statuses = map[string]string{"Tlive": "in_progress"}
	argus.deleteErrByTask = map[string]error{
		"Tgone": fmt.Errorf("argus task gone: %w", ErrArgusTaskGone),
	}

	summary, err := s.CompleteArchivedDescendants(context.Background(), orch.ID)
	if err != nil {
		t.Fatalf("mixed-resolvability sweep must not error: %v", err)
	}
	if summary.Found != 3 || summary.Pruned != 3 || summary.Errors != 0 {
		t.Fatalf("Found/Pruned/Errors = %d/%d/%d, want 3/3/0", summary.Found, summary.Pruned, summary.Errors)
	}
	// The resolvable task is deleted argus-side; the gone task's delete is
	// attempted (and tolerated); the never-bound role issues no argus call.
	gotDeletes := map[string]bool{}
	for _, id := range argus.deleteCalls {
		gotDeletes[id] = true
	}
	if !gotDeletes["Tlive"] || !gotDeletes["Tgone"] {
		t.Fatalf("both bound tasks should see a delete attempt; deleteCalls=%v", argus.deleteCalls)
	}
	for _, id := range []int64{live.ID, gone.ID, nobind.ID} {
		if _, err := db.GetRoleByID(context.Background(), id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("role %d should be pruned regardless of task-resolvability", id)
		}
	}
}

// BUG-032 (THE REPRO): the live smoking gun. A worker whose hera archived_at is
// NULL (role NOT hera-archived) but whose argus task RECORD was pruned
// out-of-band shows in the rail's Archive section (rail_list.roleArchived buckets
// it via Dead), yet the pre-fix `C` filtered on archived_at alone and skipped it
// — "17 visible archived, 0 pruned". The classification must now mirror the rail:
// a gone (Dead) argus task makes the role a prune candidate regardless of
// archived_at. The active sibling (live, existing task, archived_at NULL) must
// be left untouched — `C` clears the archive, not live work.
func TestCompleteArchivedDescendants_DeadTaskArchivedAtNull_Pruned(t *testing.T) {
	s, db, argus, wt, _ := newTestService()
	orch := db.seedOrchestrator("hera-1.0-ftw", false)
	db.seedRole(orch.ID, "coord", KindCoordinator, "hera-1.0-ftw", false)

	// Three workers the operator pruned argus-side: hera archived_at NULL, but
	// the argus task RECORD is gone (GetTaskState → ErrArgusTaskGone = Dead).
	const dead = 3
	var deadIDs []int64
	argus.goneTasks = map[string]bool{}
	for i := 0; i < dead; i++ {
		r := db.seedRole(orch.ID, fmt.Sprintf("dead%d", i), KindWorker, "hera-1.0-ftw", false) // NOT archived
		deadIDs = append(deadIDs, r.ID)
		taskID := fmt.Sprintf("Tdead%d", i)
		db.seedEndedBinding(r.ID, taskID, makeSimpleTempDir(t))
		argus.goneTasks[taskID] = true
	}
	// An active worker: archived_at NULL, live binding, task exists & in progress.
	// The rail renders it ACTIVE — `C` must not touch it.
	active := db.seedRole(orch.ID, "active", KindWorker, "hera-1.0-ftw", false)
	db.seedBinding(active.ID, "Tactive", makeSimpleTempDir(t))
	argus.statuses = map[string]string{"Tactive": "in_progress"}

	summary, err := s.CompleteArchivedDescendants(context.Background(), orch.ID)
	if err != nil {
		t.Fatalf("CompleteArchivedDescendants over dead-task archive must not error: %v", err)
	}
	// Found counts ONLY the rail-archived (Dead) workers; the active worker is
	// excluded. The pre-fix code reported Found==0 here (none hera-archived).
	if summary.Found != dead || summary.Pruned != dead {
		t.Fatalf("Found/Pruned = %d/%d, want %d/%d (dead workers pruned despite archived_at NULL)", summary.Found, summary.Pruned, dead, dead)
	}
	for _, id := range deadIDs {
		if _, err := db.GetRoleByID(context.Background(), id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("dead worker %d (archived_at NULL, argus task gone) should be pruned", id)
		}
	}
	// The active worker survives — its role row AND its live binding.
	if _, err := db.GetRoleByID(context.Background(), active.ID); err != nil {
		t.Fatalf("active worker must NOT be pruned: %v", err)
	}
	if _, err := db.GetLiveBindingByRole(context.Background(), active.ID); err != nil {
		t.Fatalf("active worker's live binding must survive: %v", err)
	}
	// The active worker's task is never deleted argus-side.
	for _, id := range argus.deleteCalls {
		if id == "Tactive" {
			t.Fatalf("active worker's argus task must never be deleted; deleteCalls=%v", argus.deleteCalls)
		}
	}
	// No worktree removal nor status write touches the active worker.
	for _, c := range argus.setStatusCalls {
		if c.TaskID == "Tactive" {
			t.Fatalf("active worker's status must never be stepped; setStatusCalls=%+v", argus.setStatusCalls)
		}
	}
	_ = wt
}

// BUG-032: a worker that is argus-side archived (the argus task's archived bit
// is set) but whose hera archived_at is NULL — the "mixed" state the rail also
// buckets into the Archive section via roleArchived's ArgusArchived clause. `C`
// must prune it too, mirroring the rail.
func TestCompleteArchivedDescendants_ArgusArchivedNotHera_Pruned(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("proj", false)
	db.seedRole(orch.ID, "coord", KindCoordinator, "proj", false)

	w := db.seedRole(orch.ID, "argus-archived", KindWorker, "proj", false) // hera NOT archived
	db.seedEndedBinding(w.ID, "Tarch", makeSimpleTempDir(t))
	argus.statuses = map[string]string{"Tarch": "in_progress"}
	argus.archivedTasks = map[string]bool{"Tarch": true} // argus-side archived

	summary, err := s.CompleteArchivedDescendants(context.Background(), orch.ID)
	if err != nil {
		t.Fatalf("CompleteArchivedDescendants: %v", err)
	}
	if summary.Found != 1 || summary.Pruned != 1 {
		t.Fatalf("Found/Pruned = %d/%d, want 1/1 (argus-archived worker is rail-archived)", summary.Found, summary.Pruned)
	}
	if _, err := db.GetRoleByID(context.Background(), w.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("argus-side-archived worker (archived_at NULL) should be pruned")
	}
}

// BUG-032: a coordinator whose ONLY non-coordinator descendant is an active,
// live worker (archived_at NULL, existing task) has NOTHING rail-archived —
// Found must be 0 so the bridge fires "nothing to prune", and the worker is
// untouched. Guards the new classification against over-pruning live agents.
func TestCompleteArchivedDescendants_OnlyActiveWorkers_FoundZero(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("proj", false)
	db.seedRole(orch.ID, "coord", KindCoordinator, "proj", false)
	active := db.seedRole(orch.ID, "w1", KindWorker, "proj", false)
	db.seedBinding(active.ID, "Tw1", makeSimpleTempDir(t))
	argus.statuses = map[string]string{"Tw1": "in_progress"}

	summary, err := s.CompleteArchivedDescendants(context.Background(), orch.ID)
	if err != nil {
		t.Fatalf("CompleteArchivedDescendants: %v", err)
	}
	if summary.Found != 0 || summary.Pruned != 0 {
		t.Fatalf("Found/Pruned = %d/%d, want 0/0 (no rail-archived descendants)", summary.Found, summary.Pruned)
	}
	if _, err := db.GetRoleByID(context.Background(), active.ID); err != nil {
		t.Fatalf("active worker must survive: %v", err)
	}
	if len(argus.deleteCalls) != 0 {
		t.Fatalf("no argus task should be deleted; deleteCalls=%v", argus.deleteCalls)
	}
}

func TestListCompletedAgents_SkipsCoordinatorRoles(t *testing.T) {
	db := newFakeDB()
	orch := db.seedOrchestrator("live-proj", false)
	coord := db.seedRole(orch.ID, "live-proj-coord", KindCoordinator, "live-proj", false)
	worker := db.seedRole(orch.ID, "done-worker", KindWorker, "live-proj", false)
	db.seedBinding(coord.ID, "Tcoord", "")
	db.seedBinding(worker.ID, "Tworker", "")

	a := &fakeArgus{statuses: map[string]string{
		"Tcoord":  "complete",
		"Tworker": "complete",
	}}
	s := NewService(db, a, &fakeWorktreeRemover{}, &fakeLogger{})

	got, err := s.ListCompletedAgents(context.Background())
	if err != nil {
		t.Fatalf("ListCompletedAgents: %v", err)
	}
	for _, ag := range got {
		if ag.ArgusTaskID == "Tcoord" {
			t.Fatalf("coordinator role must NOT appear in prune list; got %+v", ag)
		}
	}
	found := false
	for _, ag := range got {
		if ag.ArgusTaskID == "Tworker" {
			found = true
		}
	}
	if !found {
		t.Fatalf("worker role with complete task must appear in prune list; got %v", got)
	}
}
