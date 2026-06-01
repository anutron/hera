package ops

import (
	"context"
	"errors"
	"testing"
)

func TestOpenPR_DelegatesToCreator(t *testing.T) {
	db := newFakeDB()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", false)
	db.seedBinding(role.ID, "T1", "/tmp/wt/foo/w1")

	pr := &fakePRCreator{url: "https://github.com/x/y/pull/1"}
	s := NewService(db, &fakeArgus{}, &fakeWorktreeRemover{}, &fakeLogger{})
	s.PR = pr

	url, err := s.OpenPR(context.Background(), role.ID)
	if err != nil {
		t.Fatalf("OpenPR: %v", err)
	}
	if url != "https://github.com/x/y/pull/1" {
		t.Fatalf("url = %q", url)
	}
	if len(pr.calls) != 1 || pr.calls[0] != "/tmp/wt/foo/w1" {
		t.Fatalf("CreatePR calls = %v, want [/tmp/wt/foo/w1]", pr.calls)
	}
}

func TestOpenPR_NoCreator_Errors(t *testing.T) {
	db := newFakeDB()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", false)
	db.seedBinding(role.ID, "T1", "/tmp/wt")

	s := NewService(db, &fakeArgus{}, &fakeWorktreeRemover{}, &fakeLogger{})
	// s.PR left nil
	if _, err := s.OpenPR(context.Background(), role.ID); err == nil {
		t.Fatalf("OpenPR with no creator must error")
	}
}

func TestOpenPR_NoBinding_Errors(t *testing.T) {
	db := newFakeDB()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", false)
	// no binding

	pr := &fakePRCreator{}
	s := NewService(db, &fakeArgus{}, &fakeWorktreeRemover{}, &fakeLogger{})
	s.PR = pr
	if _, err := s.OpenPR(context.Background(), role.ID); err == nil {
		t.Fatalf("OpenPR with no live binding must error")
	}
	if len(pr.calls) != 0 {
		t.Fatalf("no binding must not call CreatePR; got %v", pr.calls)
	}
}

func TestOpenPR_CreatorError_Propagates(t *testing.T) {
	db := newFakeDB()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", false)
	db.seedBinding(role.ID, "T1", "/tmp/wt")

	pr := &fakePRCreator{err: errors.New("gh not authed")}
	s := NewService(db, &fakeArgus{}, &fakeWorktreeRemover{}, &fakeLogger{})
	s.PR = pr
	if _, err := s.OpenPR(context.Background(), role.ID); err == nil {
		t.Fatalf("OpenPR must propagate creator error")
	}
}

// OpenPRFromWorktree opens a PR straight from a worktree path — the entry
// point `^p` uses for a freelancer, whose worktree is the argus task's own
// (hera has no binding to resolve). It delegates to the configured PRCreator
// with the path verbatim.
func TestOpenPRFromWorktree_DelegatesToCreator(t *testing.T) {
	pr := &fakePRCreator{url: "https://github.com/x/y/pull/9"}
	s := NewService(newFakeDB(), &fakeArgus{}, &fakeWorktreeRemover{}, &fakeLogger{})
	s.PR = pr

	url, err := s.OpenPRFromWorktree(context.Background(), "/tmp/wt/freelance/feat-x")
	if err != nil {
		t.Fatalf("OpenPRFromWorktree: %v", err)
	}
	if url != "https://github.com/x/y/pull/9" {
		t.Fatalf("url = %q", url)
	}
	if len(pr.calls) != 1 || pr.calls[0] != "/tmp/wt/freelance/feat-x" {
		t.Fatalf("CreatePR calls = %v, want [/tmp/wt/freelance/feat-x]", pr.calls)
	}
}

func TestOpenPRFromWorktree_NoCreator_Errors(t *testing.T) {
	s := NewService(newFakeDB(), &fakeArgus{}, &fakeWorktreeRemover{}, &fakeLogger{})
	// s.PR left nil
	if _, err := s.OpenPRFromWorktree(context.Background(), "/tmp/wt"); err == nil {
		t.Fatalf("OpenPRFromWorktree with no creator must error")
	}
}

func TestOpenPRFromWorktree_EmptyPath_Errors(t *testing.T) {
	pr := &fakePRCreator{}
	s := NewService(newFakeDB(), &fakeArgus{}, &fakeWorktreeRemover{}, &fakeLogger{})
	s.PR = pr
	if _, err := s.OpenPRFromWorktree(context.Background(), ""); err == nil {
		t.Fatalf("OpenPRFromWorktree with empty path must error")
	}
	if len(pr.calls) != 0 {
		t.Fatalf("empty path must not call CreatePR; got %v", pr.calls)
	}
}

func TestOpenPRFromWorktree_CreatorError_Propagates(t *testing.T) {
	pr := &fakePRCreator{err: errors.New("gh not authed")}
	s := NewService(newFakeDB(), &fakeArgus{}, &fakeWorktreeRemover{}, &fakeLogger{})
	s.PR = pr
	if _, err := s.OpenPRFromWorktree(context.Background(), "/tmp/wt"); err == nil {
		t.Fatalf("OpenPRFromWorktree must propagate creator error")
	}
}

// Smoke: ExecPRCreator satisfies the PRCreator interface and is constructible.
func TestExecPRCreator_ImplementsInterface(t *testing.T) {
	var _ PRCreator = ExecPRCreator{}
}
