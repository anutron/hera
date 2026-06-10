package ops

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anutron/hera/internal/argus"
)

// makeGitWorktreeFixture initialises a main git repo + one linked
// worktree under a t.TempDir() root and returns the linked worktree's
// path. Used by tests that exercise the real `git worktree remove`
// command via ExecWorktreeRemover.
//
// Layout:
//
//	<root>/
//	  main/      <- main repo (initial commit, default branch)
//	  linked/    <- linked worktree on branch "wt"
func makeGitWorktreeFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	main := filepath.Join(root, "main")
	linked := filepath.Join(root, "linked")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatalf("mkdir main: %v", err)
	}

	run := func(dir string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		// Force a stable identity so git commit works without a global
		// gitconfig in CI / fresh-machine cases.
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=ops-test",
			"GIT_AUTHOR_EMAIL=ops-test@example.com",
			"GIT_COMMITTER_NAME=ops-test",
			"GIT_COMMITTER_EMAIL=ops-test@example.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v in %s: %v (output: %s)", args, dir, err, string(out))
		}
	}

	run(main, "init", "-q", "-b", "trunk")
	if err := os.WriteFile(filepath.Join(main, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	run(main, "add", "README.md")
	run(main, "commit", "-q", "-m", "init")
	run(main, "worktree", "add", "-q", "-b", "wt", linked)

	if _, err := os.Stat(linked); err != nil {
		t.Fatalf("linked worktree not created: %v", err)
	}
	return linked
}

func TestExecWorktreeRemover_RemovesRealWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not in PATH: %v", err)
	}
	wt := makeGitWorktreeFixture(t)

	if err := (ExecWorktreeRemover{}).Remove(context.Background(), wt); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("worktree should be gone, stat err = %v", err)
	}
}

func TestDeleteRole_HappyPath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not in PATH: %v", err)
	}
	wt := makeGitWorktreeFixture(t)

	db := newFakeDB()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", false)
	bnd := db.seedBinding(role.ID, "T1", wt)

	s := NewService(db, &fakeArgus{}, ExecWorktreeRemover{}, &fakeLogger{})

	if err := s.DeleteRole(context.Background(), role.ID); err != nil {
		t.Fatalf("DeleteRole: %v", err)
	}

	// Binding ended with end_reason=user_deleted.
	if len(db.endBindingCalls) != 1 || db.endBindingCalls[0].BindingID != bnd.ID ||
		db.endBindingCalls[0].Reason != EndReasonUserDeleted {
		t.Fatalf("EndBinding calls = %+v", db.endBindingCalls)
	}
	// Role archived.
	got, _ := db.GetRoleByID(context.Background(), role.ID)
	if !got.Archived {
		t.Fatalf("role should be archived")
	}
	// Worktree gone.
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("worktree should be removed, stat err = %v", err)
	}
}

func TestDeleteRole_NoLiveBinding(t *testing.T) {
	db := newFakeDB()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", false)
	// No binding seeded.

	wr := &fakeWorktreeRemover{}
	s := NewService(db, &fakeArgus{}, wr, &fakeLogger{})
	if err := s.DeleteRole(context.Background(), role.ID); err != nil {
		t.Fatalf("DeleteRole: %v", err)
	}
	if len(db.endBindingCalls) != 0 {
		t.Fatalf("EndBinding should not fire: %+v", db.endBindingCalls)
	}
	if len(wr.calls) != 0 {
		t.Fatalf("WorktreeRemover should not fire on empty path: %v", wr.calls)
	}
	got, _ := db.GetRoleByID(context.Background(), role.ID)
	if !got.Archived {
		t.Fatalf("role should be archived even without a binding")
	}
}

func TestDeleteRole_MissingWorktreeIsSoftNoop(t *testing.T) {
	db := newFakeDB()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", false)
	missingPath := filepath.Join(t.TempDir(), "nope")
	db.seedBinding(role.ID, "T1", missingPath)

	wr := &fakeWorktreeRemover{}
	logger := &fakeLogger{}
	s := NewService(db, &fakeArgus{}, wr, logger)
	if err := s.DeleteRole(context.Background(), role.ID); err != nil {
		t.Fatalf("DeleteRole: %v", err)
	}
	if len(wr.calls) != 0 {
		t.Fatalf("WorktreeRemover should not fire when directory is missing: %v", wr.calls)
	}
	// Audit log should record the skip.
	found := false
	for _, m := range logger.messages {
		if strings.Contains(m, "missing") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected audit log for missing path, got %v", logger.messages)
	}
}

// TestDeleteRole_WorktreeGitMissingIsSoftNoop covers BUG-054: the worktree
// directory exists on disk but argus already removed the .git file, so
// `git worktree remove` would exit 128. The op must skip git and still
// archive the role without returning an error.
func TestDeleteRole_WorktreeGitMissingIsSoftNoop(t *testing.T) {
	// Create a directory that exists but has no .git inside.
	dir := t.TempDir()

	db := newFakeDB()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", false)
	db.seedBinding(role.ID, "T1", dir)

	wr := &fakeWorktreeRemover{}
	logger := &fakeLogger{}
	s := NewService(db, &fakeArgus{}, wr, logger)
	if err := s.DeleteRole(context.Background(), role.ID); err != nil {
		t.Fatalf("DeleteRole: %v", err)
	}
	// WorktreeRemover must not be called — git would fail with exit 128.
	if len(wr.calls) != 0 {
		t.Fatalf("WorktreeRemover should not fire when .git is absent: %v", wr.calls)
	}
	// Role must still be archived.
	got, _ := db.GetRoleByID(context.Background(), role.ID)
	if !got.Archived {
		t.Fatalf("role should be archived even when worktree .git is missing")
	}
	// Audit log should mention the skip.
	found := false
	for _, m := range logger.messages {
		if strings.Contains(m, "already cleaned up") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected audit log for .git-missing skip, got %v", logger.messages)
	}
}

// TestDeleteRole_StaleAdminEntryIsSoftNoop covers BUG-018 on the `^d` path:
// the worktree dir + its .git FILE exist, but the admin entry the .git points
// at was already pruned. `git worktree remove` would exit 128, so the op must
// skip git and still archive the role without error.
func TestDeleteRole_StaleAdminEntryIsSoftNoop(t *testing.T) {
	dir := t.TempDir()
	missingAdmin := filepath.Join(t.TempDir(), "worktrees", "gone")
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: "+missingAdmin+"\n"), 0o644); err != nil {
		t.Fatalf("write .git: %v", err)
	}

	db := newFakeDB()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", false)
	db.seedBinding(role.ID, "T1", dir)

	wr := &fakeWorktreeRemover{}
	logger := &fakeLogger{}
	s := NewService(db, &fakeArgus{}, wr, logger)
	if err := s.DeleteRole(context.Background(), role.ID); err != nil {
		t.Fatalf("DeleteRole: %v", err)
	}
	if len(wr.calls) != 0 {
		t.Fatalf("WorktreeRemover should not fire when the admin entry is gone: %v", wr.calls)
	}
	got, _ := db.GetRoleByID(context.Background(), role.ID)
	if !got.Archived {
		t.Fatalf("role should be archived even when the git admin entry is gone")
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

func TestDeleteRole_EmptyWorktreePathIsSoftNoop(t *testing.T) {
	db := newFakeDB()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", false)
	db.seedBinding(role.ID, "T1", "")

	wr := &fakeWorktreeRemover{}
	logger := &fakeLogger{}
	s := NewService(db, &fakeArgus{}, wr, logger)
	if err := s.DeleteRole(context.Background(), role.ID); err != nil {
		t.Fatalf("DeleteRole: %v", err)
	}
	if len(wr.calls) != 0 {
		t.Fatalf("WorktreeRemover should not fire on empty path: %v", wr.calls)
	}
}

func TestDeleteRole_AuditLogIncludesPath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not in PATH: %v", err)
	}
	wt := makeGitWorktreeFixture(t)

	db := newFakeDB()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", false)
	db.seedBinding(role.ID, "T1", wt)

	logger := &fakeLogger{}
	s := NewService(db, &fakeArgus{}, ExecWorktreeRemover{}, logger)
	if err := s.DeleteRole(context.Background(), role.ID); err != nil {
		t.Fatalf("DeleteRole: %v", err)
	}
	found := false
	for _, m := range logger.messages {
		if strings.Contains(m, wt) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected audit log to mention worktree path; got %v", logger.messages)
	}
}

// Stage P: `^d` must ALSO destroy the argus task (which cleans the worktree +
// branch server-side), not only end the binding + remove the local worktree.
func TestDeleteRole_DestroysArgusTask(t *testing.T) {
	db := newFakeDB()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", false)
	db.seedBinding(role.ID, "T1", "")

	a := &fakeArgus{}
	s := NewService(db, a, &fakeWorktreeRemover{}, &fakeLogger{})
	if err := s.DeleteRole(context.Background(), role.ID); err != nil {
		t.Fatalf("DeleteRole: %v", err)
	}
	if len(a.deleteCalls) != 1 || a.deleteCalls[0] != "T1" {
		t.Fatalf("want argus DeleteTask(T1); got %v", a.deleteCalls)
	}
}

// A role with no live binding has no argus task to destroy.
func TestDeleteRole_NoBinding_NoArgusDelete(t *testing.T) {
	db := newFakeDB()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", false)

	a := &fakeArgus{}
	s := NewService(db, a, &fakeWorktreeRemover{}, &fakeLogger{})
	if err := s.DeleteRole(context.Background(), role.ID); err != nil {
		t.Fatalf("DeleteRole: %v", err)
	}
	if len(a.deleteCalls) != 0 {
		t.Fatalf("no binding => no argus DeleteTask; got %v", a.deleteCalls)
	}
}

// DeleteOrchestrator cascades the argus task destroy to every bound role.
func TestDeleteOrchestrator_DestroysEveryArgusTask(t *testing.T) {
	db := newFakeDB()
	orch := db.seedOrchestrator("foo", false)
	coord := db.seedRole(orch.ID, "coord", KindCoordinator, "foo", false)
	w1 := db.seedRole(orch.ID, "w1", KindWorker, "foo", false)
	w2 := db.seedRole(orch.ID, "w2", KindWorker, "foo", false)
	db.seedBinding(coord.ID, "Tc", "")
	db.seedBinding(w1.ID, "Tw1", "")
	db.seedBinding(w2.ID, "Tw2", "")

	a := &fakeArgus{}
	s := NewService(db, a, &fakeWorktreeRemover{}, &fakeLogger{})
	if err := s.DeleteOrchestrator(context.Background(), orch.ID); err != nil {
		t.Fatalf("DeleteOrchestrator: %v", err)
	}
	got := map[string]bool{}
	for _, id := range a.deleteCalls {
		got[id] = true
	}
	for _, want := range []string{"Tc", "Tw1", "Tw2"} {
		if !got[want] {
			t.Fatalf("expected argus DeleteTask(%s); got %v", want, a.deleteCalls)
		}
	}
}

func TestDeleteOrchestrator_Cascades(t *testing.T) {
	db := newFakeDB()
	orch := db.seedOrchestrator("foo", false)
	coord := db.seedRole(orch.ID, "coord", KindCoordinator, "foo", false)
	w1 := db.seedRole(orch.ID, "w1", KindWorker, "foo", false)
	w2 := db.seedRole(orch.ID, "w2", KindWorker, "foo", false)
	db.seedBinding(coord.ID, "Tc", "")
	db.seedBinding(w1.ID, "Tw1", "")
	db.seedBinding(w2.ID, "Tw2", "")

	wr := &fakeWorktreeRemover{}
	s := NewService(db, &fakeArgus{}, wr, &fakeLogger{})
	if err := s.DeleteOrchestrator(context.Background(), orch.ID); err != nil {
		t.Fatalf("DeleteOrchestrator: %v", err)
	}
	if len(db.endBindingCalls) != 3 {
		t.Fatalf("expected 3 EndBinding calls, got %d: %+v", len(db.endBindingCalls), db.endBindingCalls)
	}
	// Physical deletion: the orchestrator must be gone from the DB so the rail
	// does not show a ghost in the Archive section (BUG-004).
	if _, err := db.GetOrchestratorByID(context.Background(), orch.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("orchestrator should be physically deleted; got err=%v", err)
	}
	if len(db.deleteOrchCalls) != 1 || db.deleteOrchCalls[0] != orch.ID {
		t.Fatalf("want DeleteOrchestratorByID(%d); got %v", orch.ID, db.deleteOrchCalls)
	}
	// Roles are gone via cascade.
	for _, id := range []int64{coord.ID, w1.ID, w2.ID} {
		if _, err := db.GetRoleByID(context.Background(), id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("role %d should be gone after cascade delete; got err=%v", id, err)
		}
	}
}

// BUG-020: a coordinator whose worktree was deleted out-of-band is an orphan.
// argus's DeleteTask chokes on the missing worktree (HTTP 500 "worktree path
// missing"), but `^d` must still fully remove the orchestrator + roles +
// bindings from the DB so the orphan clears from the rail.
func TestDeleteOrchestrator_ArgusWorktreeMissing_StillDeletesDBRows(t *testing.T) {
	db := newFakeDB()
	orch := db.seedOrchestrator("orphan", false)
	coord := db.seedRole(orch.ID, "coord", KindCoordinator, "hera", false)
	missingPath := filepath.Join(t.TempDir(), "gone")
	db.seedBinding(coord.ID, "Torphan", missingPath)

	a := &fakeArgus{
		// argus refuses to delete because the worktree directory is gone.
		deleteErr: &argus.HTTPError{
			Method:     "DELETE",
			Path:       "/api/tasks/Torphan",
			StatusCode: 500,
			Body:       `{"error":"worktree path missing: ` + missingPath + ` (delete the task or recreate the worktree)"}`,
		},
	}
	logger := &fakeLogger{}
	s := NewService(db, a, &fakeWorktreeRemover{}, logger)
	if err := s.DeleteOrchestrator(context.Background(), orch.ID); err != nil {
		t.Fatalf("DeleteOrchestrator must tolerate argus worktree-missing; got %v", err)
	}

	// The argus delete was attempted (and failed) but did not abort the cascade.
	if len(a.deleteCalls) != 1 || a.deleteCalls[0] != "Torphan" {
		t.Fatalf("want a single argus DeleteTask(Torphan) attempt; got %v", a.deleteCalls)
	}
	// Orchestrator + role gone from the DB regardless of worktree state.
	if _, err := db.GetOrchestratorByID(context.Background(), orch.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("orchestrator should be physically deleted; got err=%v", err)
	}
	if _, err := db.GetRoleByID(context.Background(), coord.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("coordinator role should be gone after cascade; got err=%v", err)
	}
	// Skip should be recorded in the audit log.
	found := false
	for _, m := range logger.messages {
		if strings.Contains(m, "worktree already gone") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected audit log for argus worktree-missing skip; got %v", logger.messages)
	}
}

// A genuine argus delete failure (not worktree-missing) must still abort the
// cascade — we do not want to silently orphan DB rows on transient errors.
func TestDeleteOrchestrator_ArgusDeleteError_Aborts(t *testing.T) {
	db := newFakeDB()
	orch := db.seedOrchestrator("foo", false)
	coord := db.seedRole(orch.ID, "coord", KindCoordinator, "hera", false)
	db.seedBinding(coord.ID, "Tc", "")

	a := &fakeArgus{deleteErr: &argus.HTTPError{StatusCode: 500, Body: `{"error":"internal boom"}`}}
	s := NewService(db, a, &fakeWorktreeRemover{}, &fakeLogger{})
	if err := s.DeleteOrchestrator(context.Background(), orch.ID); err == nil {
		t.Fatalf("a non-worktree-missing argus error must abort DeleteOrchestrator")
	}
	// Orchestrator must NOT have been deleted (cascade aborted before the DB delete).
	if _, err := db.GetOrchestratorByID(context.Background(), orch.ID); err != nil {
		t.Fatalf("orchestrator must survive an aborted cascade; got err=%v", err)
	}
}

// TestDeleteOrchestrator_CoordOnly is the BUG-004 scenario: a coordinator
// created via `n` (one orchestrator + one coordinator role + one binding, no
// workers) must be physically deleted so the rail shows no ghost after `^d`.
func TestDeleteOrchestrator_CoordOnly(t *testing.T) {
	db := newFakeDB()
	orch := db.seedOrchestrator("test", false)
	coord := db.seedRole(orch.ID, "coord", KindCoordinator, "hera", false)
	db.seedBinding(coord.ID, "Tcoord", "")

	a := &fakeArgus{}
	s := NewService(db, a, &fakeWorktreeRemover{}, &fakeLogger{})
	if err := s.DeleteOrchestrator(context.Background(), orch.ID); err != nil {
		t.Fatalf("DeleteOrchestrator: %v", err)
	}

	// Argus task deleted.
	if len(a.deleteCalls) != 1 || a.deleteCalls[0] != "Tcoord" {
		t.Fatalf("want argus DeleteTask(Tcoord); got %v", a.deleteCalls)
	}
	// Orchestrator physically gone — no ghost in the Archive section.
	if _, err := db.GetOrchestratorByID(context.Background(), orch.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("orchestrator should be physically deleted (BUG-004); got err=%v", err)
	}
	// Coordinator role gone via cascade.
	if _, err := db.GetRoleByID(context.Background(), coord.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("coordinator role should be gone after cascade delete; got err=%v", err)
	}
}
