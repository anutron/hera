package ops

import (
	"context"
	"errors"
	"testing"
)

func TestAdvanceStatus_StepsForward(t *testing.T) {
	db := newFakeDB()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", false)
	db.seedBinding(role.ID, "T1", "")

	a := &fakeArgus{statuses: map[string]string{"T1": "pending"}}
	s := NewService(db, a, &fakeWorktreeRemover{}, &fakeLogger{})

	got, err := s.AdvanceStatus(context.Background(), role.ID)
	if err != nil {
		t.Fatalf("AdvanceStatus: %v", err)
	}
	if got != "in_progress" {
		t.Fatalf("status = %q, want in_progress", got)
	}
	if len(a.setStatusCalls) != 1 || a.setStatusCalls[0].Status != "in_progress" {
		t.Fatalf("SetTaskStatus calls = %+v", a.setStatusCalls)
	}
}

func TestRevertStatus_StepsBackward(t *testing.T) {
	db := newFakeDB()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", false)
	db.seedBinding(role.ID, "T1", "")

	a := &fakeArgus{statuses: map[string]string{"T1": "in_review"}}
	s := NewService(db, a, &fakeWorktreeRemover{}, &fakeLogger{})

	got, err := s.RevertStatus(context.Background(), role.ID)
	if err != nil {
		t.Fatalf("RevertStatus: %v", err)
	}
	if got != "in_progress" {
		t.Fatalf("status = %q, want in_progress", got)
	}
}

func TestAdvanceStatus_ClampsAtComplete(t *testing.T) {
	db := newFakeDB()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", false)
	db.seedBinding(role.ID, "T1", "")

	a := &fakeArgus{statuses: map[string]string{"T1": "complete"}}
	s := NewService(db, a, &fakeWorktreeRemover{}, &fakeLogger{})

	got, err := s.AdvanceStatus(context.Background(), role.ID)
	if err != nil {
		t.Fatalf("AdvanceStatus: %v", err)
	}
	if got != "complete" {
		t.Fatalf("status = %q, want complete", got)
	}
	// Already complete => no write.
	if len(a.setStatusCalls) != 0 {
		t.Fatalf("clamp at complete must not POST status; got %+v", a.setStatusCalls)
	}
}

func TestRevertStatus_ClampsAtPending(t *testing.T) {
	db := newFakeDB()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", false)
	db.seedBinding(role.ID, "T1", "")

	a := &fakeArgus{statuses: map[string]string{"T1": "pending"}}
	s := NewService(db, a, &fakeWorktreeRemover{}, &fakeLogger{})

	got, err := s.RevertStatus(context.Background(), role.ID)
	if err != nil {
		t.Fatalf("RevertStatus: %v", err)
	}
	if got != "pending" || len(a.setStatusCalls) != 0 {
		t.Fatalf("clamp at pending: status=%q calls=%+v", got, a.setStatusCalls)
	}
}

func TestStepStatus_NoBinding_Errors(t *testing.T) {
	db := newFakeDB()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", false)
	// no binding

	a := &fakeArgus{}
	s := NewService(db, a, &fakeWorktreeRemover{}, &fakeLogger{})

	if _, err := s.AdvanceStatus(context.Background(), role.ID); err == nil {
		t.Fatalf("AdvanceStatus on role with no binding must error")
	}
	if len(a.setStatusCalls) != 0 {
		t.Fatalf("no binding must not POST status; got %+v", a.setStatusCalls)
	}
}

func TestStepStatus_SetError_Propagates(t *testing.T) {
	db := newFakeDB()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", false)
	db.seedBinding(role.ID, "T1", "")

	a := &fakeArgus{statuses: map[string]string{"T1": "pending"}, setStatusErr: errors.New("argus 500")}
	s := NewService(db, a, &fakeWorktreeRemover{}, &fakeLogger{})

	if _, err := s.AdvanceStatus(context.Background(), role.ID); err == nil {
		t.Fatalf("AdvanceStatus must propagate SetTaskStatus error")
	}
}
