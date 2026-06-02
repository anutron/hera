package ops

import (
	"context"
	"errors"
	"strings"
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

	_, err := s.AdvanceStatus(context.Background(), role.ID)
	if err == nil {
		t.Fatalf("AdvanceStatus on role with no binding must error")
	}
	// The error must name the real condition — "no argus task recorded" —
	// not the misleading internal "no live binding" (which also fires for
	// archived rows whose ops should still work).
	if !strings.Contains(err.Error(), "no argus task recorded") {
		t.Fatalf("error %q must say the role has no argus task recorded", err)
	}
	if len(a.setStatusCalls) != 0 {
		t.Fatalf("no binding must not POST status; got %+v", a.setStatusCalls)
	}
}

func TestStepStatus_EndedBindingFallsBackToLatest(t *testing.T) {
	// The archived-role shape (live acceptance T3): archiving ended the
	// role's binding (end_reason='argus_archived') but kept its argus task
	// id. `s` must step that task via the latest-binding fallback instead
	// of erroring — status stepping is independent of archive state.
	db := newFakeDB()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", true)
	db.seedEndedBinding(role.ID, "T1", "/tmp/wt")

	a := &fakeArgus{statuses: map[string]string{"T1": "pending"}}
	s := NewService(db, a, &fakeWorktreeRemover{}, &fakeLogger{})

	got, err := s.AdvanceStatus(context.Background(), role.ID)
	if err != nil {
		t.Fatalf("AdvanceStatus on archived role with ended binding: %v", err)
	}
	if got != "in_progress" {
		t.Fatalf("status = %q, want in_progress", got)
	}
	if len(a.setStatusCalls) != 1 || a.setStatusCalls[0].TaskID != "T1" {
		t.Fatalf("SetTaskStatus calls = %+v, want one call for T1", a.setStatusCalls)
	}
}

func TestStepStatus_PrefersLiveBindingOverEnded(t *testing.T) {
	// When BOTH exist (an older ended binding plus a live rebind), the
	// live binding's task wins.
	db := newFakeDB()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", false)
	db.seedEndedBinding(role.ID, "T-old", "/tmp/wt1")
	db.seedBinding(role.ID, "T-live", "/tmp/wt2")

	a := &fakeArgus{statuses: map[string]string{"T-live": "pending", "T-old": "pending"}}
	s := NewService(db, a, &fakeWorktreeRemover{}, &fakeLogger{})

	if _, err := s.AdvanceStatus(context.Background(), role.ID); err != nil {
		t.Fatalf("AdvanceStatus: %v", err)
	}
	if len(a.setStatusCalls) != 1 || a.setStatusCalls[0].TaskID != "T-live" {
		t.Fatalf("SetTaskStatus calls = %+v, want one call for T-live", a.setStatusCalls)
	}
}

func TestStepStatus_EndedBindingWithEmptyTaskID_ClearError(t *testing.T) {
	db := newFakeDB()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", true)
	db.seedEndedBinding(role.ID, "", "/tmp/wt")

	a := &fakeArgus{}
	s := NewService(db, a, &fakeWorktreeRemover{}, &fakeLogger{})

	_, err := s.AdvanceStatus(context.Background(), role.ID)
	if err == nil {
		t.Fatalf("AdvanceStatus with empty-task binding must error")
	}
	if !strings.Contains(err.Error(), "no argus task recorded") {
		t.Fatalf("error %q must say the role has no argus task recorded", err)
	}
	if len(a.setStatusCalls) != 0 {
		t.Fatalf("must not POST status; got %+v", a.setStatusCalls)
	}
}

// --- StepTaskStatus (task-direct, freelancers) ---

func TestStepTaskStatus_Advance_StepsForwardByTaskID(t *testing.T) {
	// No role, no binding — a freelancer is an unmanaged argus task, and
	// StepTaskStatus must bypass the hera-binding lookup entirely.
	db := newFakeDB()
	a := &fakeArgus{statuses: map[string]string{"T9": "pending"}}
	s := NewService(db, a, &fakeWorktreeRemover{}, &fakeLogger{})

	got, err := s.StepTaskStatus(context.Background(), "T9", true)
	if err != nil {
		t.Fatalf("StepTaskStatus: %v", err)
	}
	if got != "in_progress" {
		t.Fatalf("status = %q, want in_progress", got)
	}
	if len(a.setStatusCalls) != 1 || a.setStatusCalls[0].TaskID != "T9" || a.setStatusCalls[0].Status != "in_progress" {
		t.Fatalf("SetTaskStatus calls = %+v", a.setStatusCalls)
	}
}

func TestStepTaskStatus_Revert_StepsBackwardByTaskID(t *testing.T) {
	db := newFakeDB()
	a := &fakeArgus{statuses: map[string]string{"T9": "in_review"}}
	s := NewService(db, a, &fakeWorktreeRemover{}, &fakeLogger{})

	got, err := s.StepTaskStatus(context.Background(), "T9", false)
	if err != nil {
		t.Fatalf("StepTaskStatus: %v", err)
	}
	if got != "in_progress" {
		t.Fatalf("status = %q, want in_progress", got)
	}
}

func TestStepTaskStatus_ClampsAtComplete_NoWrite(t *testing.T) {
	db := newFakeDB()
	a := &fakeArgus{statuses: map[string]string{"T9": "complete"}}
	s := NewService(db, a, &fakeWorktreeRemover{}, &fakeLogger{})

	got, err := s.StepTaskStatus(context.Background(), "T9", true)
	if err != nil {
		t.Fatalf("StepTaskStatus: %v", err)
	}
	if got != "complete" || len(a.setStatusCalls) != 0 {
		t.Fatalf("clamp at complete: status=%q calls=%+v", got, a.setStatusCalls)
	}
}

func TestStepTaskStatus_EmptyTaskID_Errors(t *testing.T) {
	db := newFakeDB()
	a := &fakeArgus{}
	s := NewService(db, a, &fakeWorktreeRemover{}, &fakeLogger{})

	if _, err := s.StepTaskStatus(context.Background(), "", true); err == nil {
		t.Fatalf("StepTaskStatus with empty task id must error")
	}
	if len(a.setStatusCalls) != 0 {
		t.Fatalf("empty task id must not POST status; got %+v", a.setStatusCalls)
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
