package ops

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
	for _, id := range []int64{coord.ID, w1.ID, w2.ID} {
		got, _ := db.GetRoleByID(context.Background(), id)
		if !got.Archived {
			t.Fatalf("role %d should be archived", id)
		}
	}
	gotOrch, _ := db.GetOrchestratorByID(context.Background(), orch.ID)
	if !gotOrch.Archived {
		t.Fatalf("orchestrator should be archived")
	}
}
