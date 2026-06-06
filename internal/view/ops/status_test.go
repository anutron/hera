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

// BUG-017: Shift-S backward walk must traverse the full ladder without bouncing.
// Each RevertStatus call fetches the CURRENT argus status and steps it one rung
// toward pending; pressing Shift-S three times from complete must land at pending
// and then CLAMP (no further write).
func TestRevertStatus_FullBackwardWalk_NoBounceFwd(t *testing.T) {
	db := newFakeDB()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", false)
	db.seedBinding(role.ID, "T1", "")

	a := &fakeArgus{statuses: map[string]string{"T1": "complete"}}
	s := NewService(db, a, &fakeWorktreeRemover{}, &fakeLogger{})
	ctx := context.Background()

	// complete → in_review
	got, err := s.RevertStatus(ctx, role.ID)
	if err != nil || got != "in_review" {
		t.Fatalf("step 1: want in_review, got %q err=%v", got, err)
	}
	// in_review → in_progress
	got, err = s.RevertStatus(ctx, role.ID)
	if err != nil || got != "in_progress" {
		t.Fatalf("step 2: want in_progress, got %q err=%v", got, err)
	}
	// in_progress → pending
	got, err = s.RevertStatus(ctx, role.ID)
	if err != nil || got != "pending" {
		t.Fatalf("step 3: want pending, got %q err=%v", got, err)
	}
	// pending → clamp (no write)
	callsBefore := len(a.setStatusCalls)
	got, err = s.RevertStatus(ctx, role.ID)
	if err != nil || got != "pending" {
		t.Fatalf("clamp: want pending (no-op), got %q err=%v", got, err)
	}
	if len(a.setStatusCalls) != callsBefore {
		t.Fatalf("clamp at pending must not POST status; calls before=%d after=%d", callsBefore, len(a.setStatusCalls))
	}
}

// BUG-017: status direction sanity — prevStatus never returns a value ABOVE its
// input on the ladder (no forward bounce). Exhaustively checks every valid
// transition.
func TestPrevStatus_NeverBounceForward(t *testing.T) {
	ladder := statusOrder // ["pending","in_progress","in_review","complete"]
	for i, s := range ladder {
		prev := prevStatus(s)
		prevIdx := statusIndex(prev)
		if prevIdx > i {
			t.Errorf("prevStatus(%q)=%q is HIGHER on the ladder than %q — would bounce forward", s, prev, s)
		}
	}
}

// BUG-017: nextStatus never returns a value BELOW its input (no backward bounce
// on advance — mirrors the Shift-S clamp guarantee for `s`).
func TestNextStatus_NeverBouncBackward(t *testing.T) {
	ladder := statusOrder
	for i, s := range ladder {
		next := nextStatus(s)
		nextIdx := statusIndex(next)
		if nextIdx < i {
			t.Errorf("nextStatus(%q)=%q is LOWER on the ladder than %q — would bounce backward", s, next, s)
		}
	}
}
