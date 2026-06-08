package daemon

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/anutron/hera/internal/db"
)

// fakeInjector records Inject calls and returns a configurable result.
type fakeInjector struct {
	calls  []fakeInjectCall
	mode   db.DeliveryMode
	errMsg string
}

type fakeInjectCall struct {
	taskID, senderRoleName string
	msgID                  int64
	tldr                   string
}

func (f *fakeInjector) Inject(_ context.Context, taskID, senderRoleName string, msgID int64, tldr string) (db.DeliveryMode, error) {
	f.calls = append(f.calls, fakeInjectCall{taskID, senderRoleName, msgID, tldr})
	if f.errMsg != "" {
		return db.DeliveryPending, errors.New(f.errMsg)
	}
	return f.mode, nil
}

func openBounceTestDB(t *testing.T) *db.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "bounce_test.sqlite")
	d, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func setupOrchestrator(t *testing.T, ctx context.Context, d *db.DB, name string) (*db.Orchestrator, *db.Role) {
	t.Helper()
	orch, err := d.Orchestrators.Create(ctx, name)
	if err != nil {
		t.Fatalf("create orchestrator %q: %v", name, err)
	}
	coord, err := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "proj",
	})
	if err != nil {
		t.Fatalf("create coordinator role: %v", err)
	}
	return orch, coord
}

func setupWorker(t *testing.T, ctx context.Context, d *db.DB, orchID int64, name, taskID string) (*db.Role, *db.Binding) {
	t.Helper()
	role, err := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orchID, Name: name, Kind: db.KindWorker, ArgusProject: "proj",
	})
	if err != nil {
		t.Fatalf("create worker role %q: %v", name, err)
	}
	bnd, err := d.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: role.ID, OrchestratorID: orchID, ArgusTaskID: taskID,
		WorktreePath: "/tmp/" + taskID,
	})
	if err != nil {
		t.Fatalf("create binding for worker %q: %v", name, err)
	}
	return role, bnd
}

// TestBounceRecovery_TwoWorkers verifies that two active workers under a
// coordinator both receive resume messages after a bounce.
func TestBounceRecovery_TwoWorkers(t *testing.T) {
	ctx := context.Background()
	d := openBounceTestDB(t)
	inj := &fakeInjector{mode: db.DeliveryIdleSubmit}

	orch, _ := setupOrchestrator(t, ctx, d, "my-orch")
	w1, _ := setupWorker(t, ctx, d, orch.ID, "worker-1", "task-1")
	w2, _ := setupWorker(t, ctx, d, orch.ID, "worker-2", "task-2")

	rec := &BounceRecoverer{DB: d, Injector: inj}
	rec.ResumeWorkers(ctx)

	// Expect two inject calls (one per worker).
	if len(inj.calls) != 2 {
		t.Fatalf("expected 2 inject calls, got %d", len(inj.calls))
	}
	tasksSent := map[string]bool{}
	for _, c := range inj.calls {
		tasksSent[c.taskID] = true
		if c.tldr != bounceResumeTldr {
			t.Errorf("tldr = %q, want %q", c.tldr, bounceResumeTldr)
		}
	}
	if !tasksSent["task-1"] || !tasksSent["task-2"] {
		t.Fatalf("not all workers messaged: %v", tasksSent)
	}

	// Verify messages in the DB.
	w1Inbox, _ := d.Messages.UnreadForRole(ctx, w1.ID)
	if len(w1Inbox) != 1 {
		t.Fatalf("w1 inbox = %d, want 1", len(w1Inbox))
	}
	if w1Inbox[0].Body != bounceResumeBody {
		t.Errorf("w1 message body = %q, want %q", w1Inbox[0].Body, bounceResumeBody)
	}
	w2Inbox, _ := d.Messages.UnreadForRole(ctx, w2.ID)
	if len(w2Inbox) != 1 {
		t.Fatalf("w2 inbox = %d, want 1", len(w2Inbox))
	}
}

// TestBounceRecovery_FreelancerExcluded verifies that a freelance role is not
// messaged during recovery.
func TestBounceRecovery_FreelancerExcluded(t *testing.T) {
	ctx := context.Background()
	d := openBounceTestDB(t)
	inj := &fakeInjector{mode: db.DeliveryIdleSubmit}

	orch, _ := setupOrchestrator(t, ctx, d, "orch")
	// Add a freelance role with a live binding.
	fl, err := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "freelancer", Kind: db.KindFreelance, ArgusProject: "proj",
	})
	if err != nil {
		t.Fatalf("create freelance role: %v", err)
	}
	_, err = d.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: fl.ID, OrchestratorID: orch.ID, ArgusTaskID: "fl-task", WorktreePath: "/tmp/fl",
	})
	if err != nil {
		t.Fatalf("create freelance binding: %v", err)
	}

	rec := &BounceRecoverer{DB: d, Injector: inj}
	rec.ResumeWorkers(ctx)

	// No workers: no inject calls.
	if len(inj.calls) != 0 {
		t.Fatalf("expected 0 inject calls (freelance excluded), got %d", len(inj.calls))
	}
	// Freelancer's inbox must be empty.
	inbox, _ := d.Messages.UnreadForRole(ctx, fl.ID)
	if len(inbox) != 0 {
		t.Fatalf("freelance inbox = %d, want 0", len(inbox))
	}
}

// TestBounceRecovery_WorkerWithoutBinding verifies that a worker with no live
// binding is skipped (not messaged).
func TestBounceRecovery_WorkerWithoutBinding(t *testing.T) {
	ctx := context.Background()
	d := openBounceTestDB(t)
	inj := &fakeInjector{mode: db.DeliveryIdleSubmit}

	orch, _ := setupOrchestrator(t, ctx, d, "orch")
	// Create a worker with an ended binding.
	role, err := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "done-worker", Kind: db.KindWorker, ArgusProject: "proj",
	})
	if err != nil {
		t.Fatalf("create worker: %v", err)
	}
	bnd, _ := d.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: role.ID, OrchestratorID: orch.ID, ArgusTaskID: "done-task", WorktreePath: "/tmp/done",
	})
	if err := d.Bindings.End(ctx, bnd.ID, "completed"); err != nil {
		t.Fatalf("end binding: %v", err)
	}

	rec := &BounceRecoverer{DB: d, Injector: inj}
	rec.ResumeWorkers(ctx)

	if len(inj.calls) != 0 {
		t.Fatalf("expected 0 inject calls (no live binding), got %d", len(inj.calls))
	}
}

// TestBounceRecovery_NoOrchestrators verifies that an empty DB is a no-op.
func TestBounceRecovery_NoOrchestrators(t *testing.T) {
	ctx := context.Background()
	d := openBounceTestDB(t)
	inj := &fakeInjector{mode: db.DeliveryIdleSubmit}

	rec := &BounceRecoverer{DB: d, Injector: inj}
	rec.ResumeWorkers(ctx) // must not panic

	if len(inj.calls) != 0 {
		t.Fatalf("expected 0 inject calls on empty DB, got %d", len(inj.calls))
	}
}

// TestBounceRecovery_NoCoordinator verifies that an orchestrator without a
// coordinator is skipped cleanly.
func TestBounceRecovery_NoCoordinator(t *testing.T) {
	ctx := context.Background()
	d := openBounceTestDB(t)
	inj := &fakeInjector{mode: db.DeliveryIdleSubmit}

	// Orchestrator with only a worker — no coordinator.
	orch, err := d.Orchestrators.Create(ctx, "orch")
	if err != nil {
		t.Fatalf("create orchestrator: %v", err)
	}
	setupWorker(t, ctx, d, orch.ID, "lone-worker", "task-x")

	rec := &BounceRecoverer{DB: d, Injector: inj}
	rec.ResumeWorkers(ctx)

	if len(inj.calls) != 0 {
		t.Fatalf("expected 0 inject calls (no coordinator), got %d", len(inj.calls))
	}
}

// TestBounceRecovery_InjectFails_MessageQueuedNoBinding verifies that when
// PTY injection fails (argus session not running post-bounce), the message is
// persisted with delivery_mode = queued_no_binding.
func TestBounceRecovery_InjectFails_MessageQueuedNoBinding(t *testing.T) {
	ctx := context.Background()
	d := openBounceTestDB(t)
	inj := &fakeInjector{errMsg: "argus: no active session"}

	orch, _ := setupOrchestrator(t, ctx, d, "orch")
	w, _ := setupWorker(t, ctx, d, orch.ID, "worker", "task-1")

	rec := &BounceRecoverer{DB: d, Injector: inj}
	rec.ResumeWorkers(ctx)

	// Message must be persisted.
	inbox, _ := d.Messages.UnreadForRole(ctx, w.ID)
	if len(inbox) != 1 {
		t.Fatalf("inbox = %d, want 1", len(inbox))
	}
	// Delivery mode must be queued_no_binding.
	msg, err := d.Messages.GetByID(ctx, inbox[0].ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if msg.DeliveryMode != db.DeliveryQueuedNoBinding {
		t.Errorf("delivery_mode = %q, want %q", msg.DeliveryMode, db.DeliveryQueuedNoBinding)
	}
}

// TestBounceRecovery_MultipleOrchestrators verifies workers across multiple
// orchestrators all receive resume messages.
func TestBounceRecovery_MultipleOrchestrators(t *testing.T) {
	ctx := context.Background()
	d := openBounceTestDB(t)
	inj := &fakeInjector{mode: db.DeliveryBusyBuffer}

	orch1, _ := setupOrchestrator(t, ctx, d, "orch-1")
	orch2, _ := setupOrchestrator(t, ctx, d, "orch-2")
	setupWorker(t, ctx, d, orch1.ID, "w1a", "t1a")
	setupWorker(t, ctx, d, orch1.ID, "w1b", "t1b")
	setupWorker(t, ctx, d, orch2.ID, "w2a", "t2a")

	rec := &BounceRecoverer{DB: d, Injector: inj}
	rec.ResumeWorkers(ctx)

	if len(inj.calls) != 3 {
		t.Fatalf("expected 3 inject calls (2 from orch-1, 1 from orch-2), got %d", len(inj.calls))
	}
}

// TestBounceRecovery_CoordinatorAlsoHasBinding verifies that the coordinator
// itself (even if it has a live binding) is NOT included in resume messages.
func TestBounceRecovery_CoordinatorNotMessaged(t *testing.T) {
	ctx := context.Background()
	d := openBounceTestDB(t)
	inj := &fakeInjector{mode: db.DeliveryIdleSubmit}

	orch, coord := setupOrchestrator(t, ctx, d, "orch")
	// Give the coordinator a live binding too (normal in production).
	_, err := d.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: coord.ID, OrchestratorID: orch.ID, ArgusTaskID: "coord-task", WorktreePath: "/tmp/coord",
	})
	if err != nil {
		t.Fatalf("create coordinator binding: %v", err)
	}
	setupWorker(t, ctx, d, orch.ID, "worker", "worker-task")

	rec := &BounceRecoverer{DB: d, Injector: inj}
	rec.ResumeWorkers(ctx)

	// Only 1 inject call: the worker. Coordinator must not be messaged.
	if len(inj.calls) != 1 {
		t.Fatalf("expected 1 inject call (worker only), got %d", len(inj.calls))
	}
	if inj.calls[0].taskID != "worker-task" {
		t.Errorf("unexpected taskID: %q", inj.calls[0].taskID)
	}
	// Coordinator's inbox must be empty.
	coordInbox, _ := d.Messages.UnreadForRole(ctx, coord.ID)
	if len(coordInbox) != 0 {
		t.Fatalf("coordinator inbox = %d, want 0", len(coordInbox))
	}
}
