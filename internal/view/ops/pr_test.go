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

// Smoke: ExecPRCreator satisfies the PRCreator interface and is constructible.
func TestExecPRCreator_ImplementsInterface(t *testing.T) {
	var _ PRCreator = ExecPRCreator{}
}
