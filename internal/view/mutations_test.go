package view

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anutron/hera/internal/view/ops"
)

// --- test fakes ---

type fakeModals struct {
	mu sync.Mutex

	inputs   []fakeInputCall
	confirms []fakeConfirmCall
	errors   []string

	// stubInput, stubConfirm let tests inject the operator's answer.
	// The fake fires the matching callback as soon as ShowInput /
	// ShowConfirm is called (mirroring the modal-open + submit flow
	// the tview Pages drives in production).
	stubInputAnswer    string
	stubInputCancel    bool
	stubInputNotOpened bool

	stubConfirmYes     bool
	stubConfirmNotOpen bool

	// stubForm2* drive the two-field form (ShowForm2). The fake fires the
	// submit callback with these values as soon as ShowForm2 is called.
	form2Calls       []fakeForm2Call
	stubForm2Name    string
	stubForm2Second  string
	stubForm2Cancel  bool
	stubForm2NotOpen bool

	// confirmGate, when non-nil, blocks ShowConfirm at entry until the test
	// closes it. Set before driving the bridge; never mutate afterwards.
	confirmGate chan struct{}

	// selects records every ShowSelect call. stubSelectIndex / stubSelectCancel
	// / stubSelectNotOpen drive the picker's callback the way stubConfirm* do.
	selects           []fakeSelectCall
	stubSelectIndex   int
	stubSelectCancel  bool
	stubSelectNotOpen bool
}

type fakeSelectCall struct {
	Title, Label string
	Items        []string
}

type fakeInputCall struct {
	Title, Label, Initial string
}

type fakeForm2Call struct {
	Title, Label1, Label2 string
}

type fakeConfirmCall struct {
	Title, Message string
}

func (f *fakeModals) ShowInput(title, label, initial string, onSubmit func(string), onCancel func()) {
	f.mu.Lock()
	f.inputs = append(f.inputs, fakeInputCall{Title: title, Label: label, Initial: initial})
	answer := f.stubInputAnswer
	cancel := f.stubInputCancel
	notOpen := f.stubInputNotOpened
	f.mu.Unlock()

	if notOpen {
		return
	}
	if cancel {
		if onCancel != nil {
			onCancel()
		}
		return
	}
	if onSubmit != nil {
		onSubmit(answer)
	}
}

func (f *fakeModals) ShowForm2(title, label1, initial1, label2, initial2 string, onSubmit func(v1, v2 string), onCancel func()) {
	f.mu.Lock()
	f.form2Calls = append(f.form2Calls, fakeForm2Call{Title: title, Label1: label1, Label2: label2})
	v1 := f.stubForm2Name
	v2 := f.stubForm2Second
	cancel := f.stubForm2Cancel
	notOpen := f.stubForm2NotOpen
	f.mu.Unlock()

	if notOpen {
		return
	}
	if cancel {
		if onCancel != nil {
			onCancel()
		}
		return
	}
	if onSubmit != nil {
		onSubmit(v1, v2)
	}
}

func (f *fakeModals) ConfirmCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.confirms)
}

func (f *fakeModals) ErrorCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.errors)
}

func (f *fakeModals) ShowConfirm(title, message string, onYes func(), onNo func()) {
	// confirmGate, when non-nil, blocks the modal open until the test
	// releases it — proving the bridge opened the modal OFF the caller's
	// goroutine (the handler must already have returned).
	if f.confirmGate != nil {
		<-f.confirmGate
	}
	f.mu.Lock()
	f.confirms = append(f.confirms, fakeConfirmCall{Title: title, Message: message})
	yes := f.stubConfirmYes
	notOpen := f.stubConfirmNotOpen
	f.mu.Unlock()

	if notOpen {
		return
	}
	if yes {
		if onYes != nil {
			onYes()
		}
		return
	}
	if onNo != nil {
		onNo()
	}
}

func (f *fakeModals) ShowSelect(title, label string, items []string, onSelect func(idx int), onCancel func()) {
	f.mu.Lock()
	f.selects = append(f.selects, fakeSelectCall{Title: title, Label: label, Items: items})
	idx := f.stubSelectIndex
	cancel := f.stubSelectCancel
	notOpen := f.stubSelectNotOpen
	f.mu.Unlock()

	if notOpen {
		return
	}
	if cancel {
		if onCancel != nil {
			onCancel()
		}
		return
	}
	if onSelect != nil {
		onSelect(idx)
	}
}

func (f *fakeModals) SelectCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.selects)
}

func (f *fakeModals) ShowError(message string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errors = append(f.errors, message)
}

type fakeSelector struct{ sel railSelection }

func (f *fakeSelector) CurrentRailSelection() railSelection { return f.sel }

// fakeRowSelector records QueueSelectRole calls so tests can assert that the
// bridge QUEUES the newly created worker row for deferred auto-select (applied
// on the next rail repopulate, not immediately).
type fakeRowSelector struct {
	mu     sync.Mutex
	queued []int64
}

func (f *fakeRowSelector) QueueSelectRole(id int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queued = append(f.queued, id)
}

func (f *fakeRowSelector) LastQueued() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.queued) == 0 {
		return 0
	}
	return f.queued[len(f.queued)-1]
}

type fakeRepopulator struct {
	mu    sync.Mutex
	count int

	// gate, when non-nil, blocks RepopulateRail at entry (before counting)
	// until the test closes it — proving the bridge refreshed OFF the
	// caller's goroutine. Set before driving the bridge.
	gate chan struct{}

	// showArchived records every SetShowArchived call so the `l` visibility-
	// sync test can assert the toggle value reached the repopulator.
	showArchived []bool
}

func (r *fakeRepopulator) RepopulateRail() {
	if r.gate != nil {
		<-r.gate
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.count++
}

func (r *fakeRepopulator) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.count
}

// SetShowArchived satisfies the bridge's optional archive-visibility sync
// (the production *App implements the same method).
func (r *fakeRepopulator) SetShowArchived(v bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.showArchived = append(r.showArchived, v)
}

func (r *fakeRepopulator) ShowArchivedValues() []bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]bool, len(r.showArchived))
	copy(out, r.showArchived)
	return out
}

type fakeListAll struct {
	mu      sync.Mutex
	visible bool
	toggles int
}

func (l *fakeListAll) Visible() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.visible
}

func (l *fakeListAll) Toggle() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.toggles++
	l.visible = !l.visible
	return l.visible
}

type fakeMutationService struct {
	mu sync.Mutex

	newCalls           []ops.NewOrchestratorInput
	newErr             error
	renameOrchCalls    []renameOrchCall
	renameOrchErr      error
	renameRoleCalls    []renameRoleCall
	renameRoleErr      error
	deleteOrchCalls    []int64
	deleteOrchErr      error
	deleteRoleCalls    []int64
	deleteRoleErr      error
	archiveOrchCalls   []int64
	archiveOrchErr     error
	unarchiveOrchCalls []int64
	unarchiveOrchErr   error
	archiveRoleCalls   []int64
	archiveRoleErr     error
	unarchiveRoleCalls []int64
	unarchiveRoleErr   error
	pinOrchCalls       []int64
	pinOrchErr         error
	unpinOrchCalls     []int64
	unpinOrchErr       error
	pinRoleCalls       []int64
	pinRoleErr         error
	unpinRoleCalls     []int64
	unpinRoleErr       error

	completedAgents  []ops.CompletedAgent
	listCompletedErr error
	pruneCalls       [][]ops.CompletedAgent
	pruneErr         error
	advanceCalls     []int64
	advanceErr       error
	revertCalls      []int64
	revertErr        error
	openPRCalls      []int64
	openPRErr        error
	openPRWtCalls    []string
	openPRWtErr      error
	resurrectCalls   []int64
	resurrectErr     error

	// Task-direct verbs (freelance rows).
	toggleArchiveTaskCalls []toggleTaskCall
	toggleArchiveTaskErr   error
	stepTaskCalls          []stepTaskCall
	stepTaskErr            error

	// SpawnWorker plumbing.
	spawnWorkerCalls []ops.SpawnWorkerInput
	spawnWorkerResp  *ops.SpawnWorkerResult
	spawnWorkerErr   error

	// Adopt verbs (`J`). listOrchs is returned by ListActiveOrchestrators;
	// adoptCalls records every AdoptTaskIntoOrchestrator input.
	listOrchs     []*ops.Orchestrator
	listOrchsErr  error
	listOrchsGate chan struct{}
	adoptCalls    []ops.AdoptInput
	adoptErr      error

	// Seam-test plumbing. *Started, when non-nil, is closed when the
	// corresponding method is entered (after recording the call). *Gate,
	// when non-nil, blocks the method until the test closes it — both let
	// tests prove a handler returned to its caller while the svc call was
	// still executing. Set before driving the bridge; never mutate after.
	archiveRoleStarted chan struct{}
	archiveRoleGate    chan struct{}
	advanceGate        chan struct{}
	listCompletedGate  chan struct{}
}

type toggleTaskCall struct {
	TaskID   string
	Archived bool
}

type stepTaskCall struct {
	TaskID  string
	Advance bool
}

type renameOrchCall struct {
	ID      int64
	NewName string
}

type renameRoleCall struct {
	ID      int64
	NewName string
}

func (s *fakeMutationService) NewOrchestrator(_ context.Context, in ops.NewOrchestratorInput) (*ops.CreatedTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.newCalls = append(s.newCalls, in)
	if s.newErr != nil {
		return nil, s.newErr
	}
	return &ops.CreatedTask{ID: "task-1", Name: in.Name + "-coord"}, nil
}

func (s *fakeMutationService) RenameOrchestrator(_ context.Context, id int64, newName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.renameOrchCalls = append(s.renameOrchCalls, renameOrchCall{ID: id, NewName: newName})
	return s.renameOrchErr
}

func (s *fakeMutationService) RenameRole(_ context.Context, id int64, newName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.renameRoleCalls = append(s.renameRoleCalls, renameRoleCall{ID: id, NewName: newName})
	return s.renameRoleErr
}

func (s *fakeMutationService) DeleteOrchestrator(_ context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteOrchCalls = append(s.deleteOrchCalls, id)
	return s.deleteOrchErr
}

func (s *fakeMutationService) DeleteRole(_ context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteRoleCalls = append(s.deleteRoleCalls, id)
	return s.deleteRoleErr
}

func (s *fakeMutationService) ArchiveOrchestrator(_ context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.archiveOrchCalls = append(s.archiveOrchCalls, id)
	return s.archiveOrchErr
}

func (s *fakeMutationService) UnarchiveOrchestrator(_ context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unarchiveOrchCalls = append(s.unarchiveOrchCalls, id)
	return s.unarchiveOrchErr
}

func (s *fakeMutationService) ArchiveRole(_ context.Context, id int64) error {
	s.mu.Lock()
	s.archiveRoleCalls = append(s.archiveRoleCalls, id)
	err := s.archiveRoleErr
	started := s.archiveRoleStarted
	gate := s.archiveRoleGate
	s.mu.Unlock()
	// Signal entry, then block on the gate OUTSIDE the mutex so seam tests
	// can read the fake's state while the "argus call" is in flight.
	if started != nil {
		close(started)
	}
	if gate != nil {
		<-gate
	}
	return err
}

func (s *fakeMutationService) UnarchiveRole(_ context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unarchiveRoleCalls = append(s.unarchiveRoleCalls, id)
	return s.unarchiveRoleErr
}

func (s *fakeMutationService) PinOrchestrator(_ context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pinOrchCalls = append(s.pinOrchCalls, id)
	return s.pinOrchErr
}

func (s *fakeMutationService) UnpinOrchestrator(_ context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unpinOrchCalls = append(s.unpinOrchCalls, id)
	return s.unpinOrchErr
}

func (s *fakeMutationService) PinRole(_ context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pinRoleCalls = append(s.pinRoleCalls, id)
	return s.pinRoleErr
}

func (s *fakeMutationService) UnpinRole(_ context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unpinRoleCalls = append(s.unpinRoleCalls, id)
	return s.unpinRoleErr
}

func (s *fakeMutationService) ToggleArchiveTask(_ context.Context, taskID string, archived bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.toggleArchiveTaskCalls = append(s.toggleArchiveTaskCalls, toggleTaskCall{TaskID: taskID, Archived: archived})
	return s.toggleArchiveTaskErr
}

func (s *fakeMutationService) StepTaskStatus(_ context.Context, taskID string, advance bool) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stepTaskCalls = append(s.stepTaskCalls, stepTaskCall{TaskID: taskID, Advance: advance})
	if s.stepTaskErr != nil {
		return "", s.stepTaskErr
	}
	return "in_progress", nil
}

func (s *fakeMutationService) ListCompletedAgents(_ context.Context) ([]ops.CompletedAgent, error) {
	s.mu.Lock()
	agents := s.completedAgents
	err := s.listCompletedErr
	gate := s.listCompletedGate
	s.mu.Unlock()
	if gate != nil {
		<-gate
	}
	return agents, err
}

func (s *fakeMutationService) PruneCompleted(_ context.Context, agents []ops.CompletedAgent) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneCalls = append(s.pruneCalls, agents)
	if s.pruneErr != nil {
		return 0, s.pruneErr
	}
	return len(agents), nil
}

func (s *fakeMutationService) AdvanceStatus(_ context.Context, id int64) (string, error) {
	s.mu.Lock()
	s.advanceCalls = append(s.advanceCalls, id)
	err := s.advanceErr
	gate := s.advanceGate
	s.mu.Unlock()
	if gate != nil {
		<-gate
	}
	return "in_progress", err
}

func (s *fakeMutationService) RevertStatus(_ context.Context, id int64) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revertCalls = append(s.revertCalls, id)
	return "pending", s.revertErr
}

func (s *fakeMutationService) OpenPR(_ context.Context, id int64) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.openPRCalls = append(s.openPRCalls, id)
	return "https://example/pr/1", s.openPRErr
}

func (s *fakeMutationService) OpenPRFromWorktree(_ context.Context, worktreePath string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.openPRWtCalls = append(s.openPRWtCalls, worktreePath)
	return "https://example/pr/wt", s.openPRWtErr
}

func (s *fakeMutationService) ResurrectOrchestrator(_ context.Context, coordRoleID int64) (*ops.CreatedTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resurrectCalls = append(s.resurrectCalls, coordRoleID)
	if s.resurrectErr != nil {
		return nil, s.resurrectErr
	}
	return &ops.CreatedTask{ID: "task-resurrect"}, nil
}

func (s *fakeMutationService) SpawnWorker(_ context.Context, in ops.SpawnWorkerInput) (*ops.SpawnWorkerResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.spawnWorkerCalls = append(s.spawnWorkerCalls, in)
	if s.spawnWorkerErr != nil {
		return nil, s.spawnWorkerErr
	}
	if s.spawnWorkerResp != nil {
		cp := *s.spawnWorkerResp
		return &cp, nil
	}
	return &ops.SpawnWorkerResult{RoleID: 42, ArgusTaskID: "task-worker-1"}, nil
}

func (s *fakeMutationService) ListActiveOrchestrators(_ context.Context) ([]*ops.Orchestrator, error) {
	s.mu.Lock()
	orchs := s.listOrchs
	err := s.listOrchsErr
	gate := s.listOrchsGate
	s.mu.Unlock()
	if gate != nil {
		<-gate
	}
	return orchs, err
}

func (s *fakeMutationService) AdoptTaskIntoOrchestrator(_ context.Context, in ops.AdoptInput) (*ops.AdoptResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.adoptCalls = append(s.adoptCalls, in)
	if s.adoptErr != nil {
		return nil, s.adoptErr
	}
	return &ops.AdoptResult{OrchestratorName: "orch", RoleName: in.RoleName, RoleID: 1, BindingID: 1}, nil
}

// newBridgeUnderTest wires a mutationBridge with all-fake deps. The
// caller is expected to seed the selector, modals, and service before
// invoking the verb.
func newBridgeUnderTest() (*mutationBridge, *fakeModals, *fakeSelector, *fakeMutationService, *fakeListAll, *fakeRepopulator) {
	b, m, sel, svc, la, rp, _ := newBridgeUnderTestWithHelp()
	return b, m, sel, svc, la, rp
}

// fakeHelpSender records SendHelp calls so the OnHelp contract test can assert
// a help frame was sent (D12) instead of an in-surface modal.
type fakeHelpSender struct{ helps int }

func (f *fakeHelpSender) SendHelp() error { f.helps++; return nil }

func newBridgeUnderTestWithHelp() (*mutationBridge, *fakeModals, *fakeSelector, *fakeMutationService, *fakeListAll, *fakeRepopulator, *fakeHelpSender) {
	m := &fakeModals{}
	sel := &fakeSelector{}
	svc := &fakeMutationService{}
	la := &fakeListAll{}
	rp := &fakeRepopulator{}
	help := &fakeHelpSender{}
	b := newMutationBridge(context.Background(), m, sel, svc, la, rp, help, nil)
	return b, m, sel, svc, la, rp, help
}

// --- OnNew ---

func TestBridge_OnNew_ValidName_CallsNewOrchestrator(t *testing.T) {
	b, m, _, svc, _, rp := newBridgeUnderTest()
	m.stubForm2Name = "foo"

	b.OnNew()
	b.waitIdle()

	if len(svc.newCalls) != 1 {
		t.Fatalf("want 1 NewOrchestrator call, got %d", len(svc.newCalls))
	}
	if svc.newCalls[0].Name != "foo" {
		t.Fatalf("NewOrchestrator.Name: want %q, got %q", "foo", svc.newCalls[0].Name)
	}
	if rp.Count() != 1 {
		t.Fatalf("expected rail refresh after successful create; got %d", rp.Count())
	}
	if len(m.errors) != 0 {
		t.Fatalf("expected no error modal; got %v", m.errors)
	}
}

func TestBridge_OnNew_EmptyName_NoServiceCall(t *testing.T) {
	b, m, _, svc, _, rp := newBridgeUnderTest()
	m.stubForm2Name = ""

	b.OnNew()
	b.waitIdle()

	if len(svc.newCalls) != 0 {
		t.Fatalf("empty name must NOT call NewOrchestrator; got %d", len(svc.newCalls))
	}
	if rp.Count() != 0 {
		t.Fatalf("empty name must NOT refresh; got %d", rp.Count())
	}
}

func TestBridge_OnNew_ServiceError_ShowsErrorModal(t *testing.T) {
	b, m, _, svc, _, rp := newBridgeUnderTest()
	m.stubForm2Name = "foo"
	svc.newErr = errors.New("argus down")

	b.OnNew()
	b.waitIdle()

	if len(m.errors) != 1 || m.errors[0] != "argus down" {
		t.Fatalf("want error modal with %q; got %v", "argus down", m.errors)
	}
	if rp.Count() != 0 {
		t.Fatalf("error path must NOT refresh; got %d", rp.Count())
	}
}

func TestBridge_OnNew_Cancel_NoServiceCall(t *testing.T) {
	b, m, _, svc, _, _ := newBridgeUnderTest()
	m.stubForm2Cancel = true

	b.OnNew()
	b.waitIdle()

	if len(svc.newCalls) != 0 {
		t.Fatalf("cancel must NOT call NewOrchestrator; got %d", len(svc.newCalls))
	}
}

// --- OnRename ---

func TestBridge_OnRename_Orchestrator_CallsRenameOrchestrator(t *testing.T) {
	b, m, sel, svc, _, rp := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selOrchestrator, OrchestratorID: 7, Name: "old"}
	m.stubInputAnswer = "new"

	b.OnRename()
	b.waitIdle()

	if len(svc.renameOrchCalls) != 1 {
		t.Fatalf("want 1 RenameOrchestrator call, got %d", len(svc.renameOrchCalls))
	}
	if svc.renameOrchCalls[0].ID != 7 || svc.renameOrchCalls[0].NewName != "new" {
		t.Fatalf("RenameOrchestrator args: want (7,%q), got (%d,%q)",
			"new", svc.renameOrchCalls[0].ID, svc.renameOrchCalls[0].NewName)
	}
	if len(svc.renameRoleCalls) != 0 {
		t.Fatalf("must not call RenameRole when selection is orchestrator")
	}
	if rp.Count() != 1 {
		t.Fatalf("expected rail refresh; got %d", rp.Count())
	}
}

func TestBridge_OnRename_Role_CallsRenameRole(t *testing.T) {
	b, m, sel, svc, _, rp := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selRole, RoleID: 42, Name: "old", RoleKind: "worker"}
	m.stubInputAnswer = "renamed"

	b.OnRename()
	b.waitIdle()

	if len(svc.renameRoleCalls) != 1 {
		t.Fatalf("want 1 RenameRole call, got %d", len(svc.renameRoleCalls))
	}
	if svc.renameRoleCalls[0].ID != 42 || svc.renameRoleCalls[0].NewName != "renamed" {
		t.Fatalf("RenameRole args: want (42,%q), got (%d,%q)",
			"renamed", svc.renameRoleCalls[0].ID, svc.renameRoleCalls[0].NewName)
	}
	if len(svc.renameOrchCalls) != 0 {
		t.Fatalf("must not call RenameOrchestrator when selection is role")
	}
	if rp.Count() != 1 {
		t.Fatalf("expected rail refresh; got %d", rp.Count())
	}
}

func TestBridge_OnRename_EmptyName_NoServiceCall(t *testing.T) {
	b, m, sel, svc, _, _ := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selOrchestrator, OrchestratorID: 7, Name: "old"}
	m.stubInputAnswer = ""

	b.OnRename()
	b.waitIdle()

	if len(svc.renameOrchCalls) != 0 {
		t.Fatalf("empty name must NOT call RenameOrchestrator; got %d", len(svc.renameOrchCalls))
	}
}

func TestBridge_OnRename_UnchangedName_NoServiceCall(t *testing.T) {
	b, m, sel, svc, _, _ := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selRole, RoleID: 42, Name: "same"}
	m.stubInputAnswer = "same"

	b.OnRename()
	b.waitIdle()

	if len(svc.renameRoleCalls) != 0 {
		t.Fatalf("unchanged name must NOT call RenameRole; got %d", len(svc.renameRoleCalls))
	}
}

func TestBridge_OnRename_NoSelection_NoServiceCall_Feedback(t *testing.T) {
	b, m, _, svc, _, _ := newBridgeUnderTest()
	// sel.sel is zero-value (Kind == selNone)

	b.OnRename()
	b.waitIdle()

	if len(m.inputs) != 0 {
		t.Fatalf("no selection must not open input modal; got %d", len(m.inputs))
	}
	if len(svc.renameOrchCalls)+len(svc.renameRoleCalls) != 0 {
		t.Fatalf("no selection must not call any rename")
	}
	if m.ErrorCount() != 1 || !stringsContains(m.errors[0], "not applicable") {
		t.Fatalf("r on a non-addressable row must give feedback; errors=%v", m.errors)
	}
}

// --- OnDelete ---

func TestBridge_OnDelete_ConfirmYes_Orchestrator_CallsDeleteOrchestrator(t *testing.T) {
	b, m, sel, svc, _, rp := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selOrchestrator, OrchestratorID: 7, Name: "foo"}
	m.stubConfirmYes = true

	b.OnDelete()
	b.waitIdle()

	if len(m.confirms) != 1 {
		t.Fatalf("delete must open confirm modal; got %d", len(m.confirms))
	}
	if len(svc.deleteOrchCalls) != 1 || svc.deleteOrchCalls[0] != 7 {
		t.Fatalf("want DeleteOrchestrator(7); got %v", svc.deleteOrchCalls)
	}
	if rp.Count() != 1 {
		t.Fatalf("expected rail refresh; got %d", rp.Count())
	}
}

func TestBridge_OnDelete_ConfirmYes_Role_CallsDeleteRole(t *testing.T) {
	b, m, sel, svc, _, _ := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selRole, RoleID: 13, Name: "w1"}
	m.stubConfirmYes = true

	b.OnDelete()
	b.waitIdle()

	if len(svc.deleteRoleCalls) != 1 || svc.deleteRoleCalls[0] != 13 {
		t.Fatalf("want DeleteRole(13); got %v", svc.deleteRoleCalls)
	}
	if len(svc.deleteOrchCalls) != 0 {
		t.Fatalf("must not call DeleteOrchestrator when role is selected")
	}
}

func TestBridge_OnDelete_ConfirmNo_NoServiceCall(t *testing.T) {
	b, m, sel, svc, _, rp := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selOrchestrator, OrchestratorID: 7, Name: "foo"}
	m.stubConfirmYes = false

	b.OnDelete()
	b.waitIdle()

	if len(svc.deleteOrchCalls) != 0 {
		t.Fatalf("confirm=No must NOT call Delete; got %d", len(svc.deleteOrchCalls))
	}
	if rp.Count() != 0 {
		t.Fatalf("confirm=No must NOT refresh; got %d", rp.Count())
	}
}

func TestBridge_OnDelete_NoSelection_NoConfirm_Feedback(t *testing.T) {
	b, m, _, svc, _, _ := newBridgeUnderTest()

	b.OnDelete()
	b.waitIdle()

	if len(m.confirms) != 0 {
		t.Fatalf("no selection must NOT open confirm modal; got %d", len(m.confirms))
	}
	if len(svc.deleteOrchCalls)+len(svc.deleteRoleCalls) != 0 {
		t.Fatalf("no selection must not call any delete")
	}
	if m.ErrorCount() != 1 || !stringsContains(m.errors[0], "not applicable") {
		t.Fatalf("^d on a non-addressable row must give feedback; errors=%v", m.errors)
	}
}

func TestBridge_OnDelete_ServiceError_ShowsErrorModal(t *testing.T) {
	b, m, sel, svc, _, rp := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selRole, RoleID: 13, Name: "w1"}
	m.stubConfirmYes = true
	svc.deleteRoleErr = errors.New("worktree busy")

	b.OnDelete()
	b.waitIdle()

	if len(m.errors) != 1 || m.errors[0] != "worktree busy" {
		t.Fatalf("want error modal; got %v", m.errors)
	}
	if rp.Count() != 0 {
		t.Fatalf("error path must NOT refresh; got %d", rp.Count())
	}
}

// --- OnArchive ---

func TestBridge_OnArchive_ActiveOrchestrator_Archives(t *testing.T) {
	b, _, sel, svc, _, rp := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selOrchestrator, OrchestratorID: 5, Name: "foo"}

	b.OnArchive()
	b.waitIdle()

	if len(svc.archiveOrchCalls) != 1 || svc.archiveOrchCalls[0] != 5 {
		t.Fatalf("want ArchiveOrchestrator(5); got %v", svc.archiveOrchCalls)
	}
	if len(svc.unarchiveOrchCalls) != 0 {
		t.Fatalf("active orchestrator must not unarchive; got %v", svc.unarchiveOrchCalls)
	}
	if rp.Count() != 1 {
		t.Fatalf("expected rail refresh; got %d", rp.Count())
	}
}

func TestBridge_OnArchive_ArchivedOrchestrator_Unarchives(t *testing.T) {
	b, _, sel, svc, _, rp := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selOrchestrator, OrchestratorID: 5, Name: "foo", Archived: true}

	b.OnArchive()
	b.waitIdle()

	if len(svc.unarchiveOrchCalls) != 1 || svc.unarchiveOrchCalls[0] != 5 {
		t.Fatalf("want UnarchiveOrchestrator(5); got %v", svc.unarchiveOrchCalls)
	}
	if len(svc.archiveOrchCalls) != 0 {
		t.Fatalf("archived orchestrator must not re-archive; got %v", svc.archiveOrchCalls)
	}
	if rp.Count() != 1 {
		t.Fatalf("expected rail refresh; got %d", rp.Count())
	}
}

func TestBridge_OnArchive_ActiveRole_Archives(t *testing.T) {
	b, _, sel, svc, _, rp := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selRole, RoleID: 9, Name: "w"}

	b.OnArchive()
	b.waitIdle()

	if len(svc.archiveRoleCalls) != 1 || svc.archiveRoleCalls[0] != 9 {
		t.Fatalf("want ArchiveRole(9); got %v", svc.archiveRoleCalls)
	}
	if len(svc.unarchiveRoleCalls) != 0 {
		t.Fatalf("active role must not unarchive; got %v", svc.unarchiveRoleCalls)
	}
	if rp.Count() != 1 {
		t.Fatalf("expected rail refresh; got %d", rp.Count())
	}
}

func TestBridge_OnArchive_HeraArchivedRole_Unarchives(t *testing.T) {
	b, _, sel, svc, _, _ := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selRole, RoleID: 9, Name: "w", Archived: true}

	b.OnArchive()
	b.waitIdle()

	if len(svc.unarchiveRoleCalls) != 1 || svc.unarchiveRoleCalls[0] != 9 {
		t.Fatalf("want UnarchiveRole(9); got %v", svc.unarchiveRoleCalls)
	}
	if len(svc.archiveRoleCalls) != 0 {
		t.Fatalf("archived role must not re-archive; got %v", svc.archiveRoleCalls)
	}
}

// The live-found bug: a role can be hera-active + argus-archived (mixed flags
// from historical asymmetric toggles). The rail DISPLAYS it as archived
// (roleArchived: Archived || ArgusArchived || Dead), so `a` must UNARCHIVE —
// direction follows the effective rendered state, not the hera flag alone.
// The old flag-inspecting toggle re-archived it (set a fresh archived_at).
func TestBridge_OnArchive_MixedFlagRole_ArgusArchived_Unarchives(t *testing.T) {
	b, _, sel, svc, _, _ := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selRole, RoleID: 9, Name: "w", Archived: false, ArgusArchived: true}

	b.OnArchive()
	b.waitIdle()

	if len(svc.unarchiveRoleCalls) != 1 || svc.unarchiveRoleCalls[0] != 9 {
		t.Fatalf("mixed-flag row displays archived, want UnarchiveRole(9); got unarchive=%v archive=%v",
			svc.unarchiveRoleCalls, svc.archiveRoleCalls)
	}
	if len(svc.archiveRoleCalls) != 0 {
		t.Fatalf("mixed-flag row must NOT be re-archived; got %v", svc.archiveRoleCalls)
	}
}

// A dead binding also displays as archived — `a` must go the unarchive
// direction there too, never stamp a fresh archived_at.
func TestBridge_OnArchive_DeadRole_Unarchives(t *testing.T) {
	b, _, sel, svc, _, _ := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selRole, RoleID: 9, Name: "w", Dead: true}

	b.OnArchive()
	b.waitIdle()

	if len(svc.unarchiveRoleCalls) != 1 || svc.unarchiveRoleCalls[0] != 9 {
		t.Fatalf("dead row displays archived, want UnarchiveRole(9); got unarchive=%v archive=%v",
			svc.unarchiveRoleCalls, svc.archiveRoleCalls)
	}
	if len(svc.archiveRoleCalls) != 0 {
		t.Fatalf("dead row must NOT be re-archived; got %v", svc.archiveRoleCalls)
	}
}

func TestBridge_OnArchive_ServiceError_ShowsErrorModal(t *testing.T) {
	b, m, sel, svc, _, _ := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selRole, RoleID: 9, Name: "w"}
	svc.archiveRoleErr = errors.New("argus 500")

	b.OnArchive()
	b.waitIdle()

	if len(m.errors) != 1 || m.errors[0] != "argus 500" {
		t.Fatalf("want error modal; got %v", m.errors)
	}
}

// --- OnListAll ---

func TestBridge_OnListAll_TogglesState_AndRefreshes(t *testing.T) {
	b, _, _, _, la, rp := newBridgeUnderTest()
	if la.Visible() {
		t.Fatalf("initial Visible: want false")
	}

	b.OnListAll()
	b.waitIdle()

	if la.toggles != 1 {
		t.Fatalf("want 1 toggle, got %d", la.toggles)
	}
	if !la.Visible() {
		t.Fatalf("after one toggle: want Visible=true")
	}
	if rp.Count() != 1 {
		t.Fatalf("expected rail refresh after l toggle; got %d", rp.Count())
	}

	b.OnListAll()
	b.waitIdle()
	if la.toggles != 2 {
		t.Fatalf("want 2 toggles, got %d", la.toggles)
	}
	if la.Visible() {
		t.Fatalf("after two toggles: want Visible=false")
	}
}

// --- OnHelp ---

// Under the key-surrender contract (D12) OnHelp sends a help control frame so
// argus pops its overlay; it MUST NOT render an in-surface modal and MUST NOT
// refresh the rail (no DB read/write).
func TestBridge_OnHelp_SendsHelpFrame(t *testing.T) {
	b, _, _, _, _, rp, help := newBridgeUnderTestWithHelp()

	b.OnHelp()

	if help.helps != 1 {
		t.Fatalf("want 1 help frame, got %d", help.helps)
	}
	if rp.Count() != 0 {
		t.Fatalf("help must not refresh rail; got %d", rp.Count())
	}
}

// A nil help sender makes OnHelp a safe no-op.
func TestBridge_OnHelp_NilSenderNoPanic(t *testing.T) {
	m := &fakeModals{}
	b := newMutationBridge(context.Background(), m, &fakeSelector{}, &fakeMutationService{}, &fakeListAll{}, &fakeRepopulator{}, nil, nil)
	b.OnHelp() // must not panic
}

// --- Stage P: OnDelete destructive confirm + child-agent warning ---

func TestBridge_OnDelete_Orchestrator_ConfirmNamesAndWarnsChildren(t *testing.T) {
	b, m, sel, _, _, _ := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selOrchestrator, OrchestratorID: 7, Name: "foo", ChildCount: 3}
	m.stubConfirmNotOpen = true // open the modal but do not auto-confirm

	b.OnDelete()
	b.waitIdle()

	if len(m.confirms) != 1 {
		t.Fatalf("delete must open a confirm modal; got %d", len(m.confirms))
	}
	msg := m.confirms[0].Message
	if !stringsContains(msg, "foo") || !stringsContains(msg, "3") {
		t.Fatalf("confirm must name target and child count; got %q", msg)
	}
	if !stringsContains(msg, "DESTRUCTIVE") {
		t.Fatalf("confirm must flag DESTRUCTIVE; got %q", msg)
	}
}

func TestBridge_OnDelete_Role_WithChildren_WarnsChildren(t *testing.T) {
	b, m, sel, _, _, _ := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selRole, RoleID: 13, Name: "coord", ChildCount: 2}
	m.stubConfirmNotOpen = true

	b.OnDelete()
	b.waitIdle()

	if len(m.confirms) != 1 {
		t.Fatalf("delete must open a confirm modal; got %d", len(m.confirms))
	}
	if !stringsContains(m.confirms[0].Message, "child agent") {
		t.Fatalf("role with children must warn; got %q", m.confirms[0].Message)
	}
}

// --- Stage P: OnPrune ---

func TestBridge_OnPrune_ConfirmYes_PrunesListed(t *testing.T) {
	b, m, _, svc, _, rp := newBridgeUnderTest()
	svc.completedAgents = []ops.CompletedAgent{
		{RoleID: 1, Name: "done-a", ArgusTaskID: "Ta"},
		{RoleID: 2, Name: "done-b", ArgusTaskID: "Tb"},
	}
	m.stubConfirmYes = true

	b.OnPrune()
	b.waitIdle()

	if len(m.confirms) != 1 {
		t.Fatalf("prune must open a confirm modal; got %d", len(m.confirms))
	}
	if !stringsContains(m.confirms[0].Message, "done-a") || !stringsContains(m.confirms[0].Message, "done-b") {
		t.Fatalf("confirm must list completed agents; got %q", m.confirms[0].Message)
	}
	if len(svc.pruneCalls) != 1 || len(svc.pruneCalls[0]) != 2 {
		t.Fatalf("prune confirm must call PruneCompleted with the 2 agents; got %+v", svc.pruneCalls)
	}
	if rp.Count() != 1 {
		t.Fatalf("prune must refresh rail; got %d", rp.Count())
	}
}

func TestBridge_OnPrune_ConfirmNo_NoPrune(t *testing.T) {
	b, m, _, svc, _, _ := newBridgeUnderTest()
	svc.completedAgents = []ops.CompletedAgent{{RoleID: 1, Name: "done-a", ArgusTaskID: "Ta"}}
	m.stubConfirmYes = false

	b.OnPrune()
	b.waitIdle()

	if len(svc.pruneCalls) != 0 {
		t.Fatalf("confirm=No must NOT prune; got %+v", svc.pruneCalls)
	}
}

func TestBridge_OnPrune_NoCompleted_NoConfirmNoPrune(t *testing.T) {
	b, m, _, svc, _, _ := newBridgeUnderTest()
	svc.completedAgents = nil

	b.OnPrune()
	b.waitIdle()

	if len(m.confirms) != 0 {
		t.Fatalf("no completed agents must NOT open a destructive confirm; got %d", len(m.confirms))
	}
	if len(svc.pruneCalls) != 0 {
		t.Fatalf("no completed agents must NOT prune; got %+v", svc.pruneCalls)
	}
}

// --- Stage P: OnStatusAdvance / OnStatusRevert ---

func TestBridge_OnStatusAdvance_Role_Steps(t *testing.T) {
	b, _, sel, svc, _, rp := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selRole, RoleID: 9, Name: "w"}

	b.OnStatusAdvance()
	b.waitIdle()

	if len(svc.advanceCalls) != 1 || svc.advanceCalls[0] != 9 {
		t.Fatalf("want AdvanceStatus(9); got %v", svc.advanceCalls)
	}
	if rp.Count() != 1 {
		t.Fatalf("status step must refresh rail; got %d", rp.Count())
	}
}

func TestBridge_OnStatusRevert_Role_Steps(t *testing.T) {
	b, _, sel, svc, _, _ := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selRole, RoleID: 9, Name: "w"}

	b.OnStatusRevert()
	b.waitIdle()

	if len(svc.revertCalls) != 1 || svc.revertCalls[0] != 9 {
		t.Fatalf("want RevertStatus(9); got %v", svc.revertCalls)
	}
}

// --- Stage P: OnOpenPR ---

func TestBridge_OnOpenPR_ConfirmYes_OpensPR(t *testing.T) {
	b, m, sel, svc, _, _ := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selRole, RoleID: 9, Name: "w"}
	m.stubConfirmYes = true

	b.OnOpenPR()
	b.waitIdle()

	if len(m.confirms) != 1 {
		t.Fatalf("open-PR must confirm first; got %d", len(m.confirms))
	}
	if len(svc.openPRCalls) != 1 || svc.openPRCalls[0] != 9 {
		t.Fatalf("want OpenPR(9); got %v", svc.openPRCalls)
	}
}

func TestBridge_OnOpenPR_ConfirmNo_NoPR(t *testing.T) {
	b, m, sel, svc, _, _ := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selRole, RoleID: 9, Name: "w"}
	m.stubConfirmYes = false

	b.OnOpenPR()
	b.waitIdle()

	if len(svc.openPRCalls) != 0 {
		t.Fatalf("confirm=No must NOT open a PR; got %v", svc.openPRCalls)
	}
}

// `^p` on a coordinator selection (root orchestrator header carrying its
// CoordRoleID) opens a PR for the coordinator's bound argus task — no longer a
// no-op on coord rows. Combined with worker-less orchestrators rendering
// header-only, this opens a PR on the coord of a coord-only project.
func TestBridge_OnOpenPR_Coordinator_OpensPRForCoordRole(t *testing.T) {
	b, m, sel, svc, _, _ := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selOrchestrator, OrchestratorID: 1, CoordRoleID: 42, Name: "foo"}
	m.stubConfirmYes = true

	b.OnOpenPR()
	b.waitIdle()

	if len(m.confirms) != 1 {
		t.Fatalf("open-PR on a coordinator must confirm first; got %d", len(m.confirms))
	}
	if len(svc.openPRCalls) != 1 || svc.openPRCalls[0] != 42 {
		t.Fatalf("want OpenPR(42) for the coord role; got %v", svc.openPRCalls)
	}
}

// A sub-coordinator selection (a coordinator role row) opens a PR for that
// role's own binding.
func TestBridge_OnOpenPR_SubCoordinatorRole_OpensPR(t *testing.T) {
	b, m, sel, svc, _, _ := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selRole, RoleID: 77, RoleKind: "coordinator", Name: "sub"}
	m.stubConfirmYes = true

	b.OnOpenPR()
	b.waitIdle()

	if len(svc.openPRCalls) != 1 || svc.openPRCalls[0] != 77 {
		t.Fatalf("want OpenPR(77) for the sub-coordinator role; got %v", svc.openPRCalls)
	}
}

// `^p` on a FREELANCER selection (a freelance row: RoleKind "freelance",
// RoleID 0, carrying an ArgusTaskID and the argus task's worktree path) opens
// a PR straight from that worktree via OpenPRFromWorktree — no longer a no-op
// just because the freelancer has no hera RoleID.
func TestBridge_OnOpenPR_Freelancer_OpensPRFromWorktree(t *testing.T) {
	b, m, sel, svc, _, _ := newBridgeUnderTest()
	sel.sel = railSelection{
		Kind:         selRole,
		RoleKind:     "freelance",
		RoleID:       0,
		Name:         "feat-x",
		ArgusTaskID:  "T7",
		WorktreePath: "/tmp/wt/freelance/feat-x",
	}
	m.stubConfirmYes = true

	b.OnOpenPR()
	b.waitIdle()

	if len(m.confirms) != 1 {
		t.Fatalf("open-PR on a freelancer must confirm first; got %d", len(m.confirms))
	}
	if !stringsContains(m.confirms[0].Title, "feat-x") {
		t.Fatalf("confirm must name the freelancer; got %q", m.confirms[0].Title)
	}
	if len(svc.openPRWtCalls) != 1 || svc.openPRWtCalls[0] != "/tmp/wt/freelance/feat-x" {
		t.Fatalf("want OpenPRFromWorktree(/tmp/wt/freelance/feat-x); got %v", svc.openPRWtCalls)
	}
	if len(svc.openPRCalls) != 0 {
		t.Fatalf("freelancer ^p must NOT use the role-id PR path; got %v", svc.openPRCalls)
	}
}

func TestBridge_OnOpenPR_Freelancer_ConfirmNo_NoPR(t *testing.T) {
	b, m, sel, svc, _, _ := newBridgeUnderTest()
	sel.sel = railSelection{
		Kind:         selRole,
		RoleKind:     "freelance",
		Name:         "feat-x",
		ArgusTaskID:  "T7",
		WorktreePath: "/tmp/wt/freelance/feat-x",
	}
	m.stubConfirmYes = false

	b.OnOpenPR()
	b.waitIdle()

	if len(svc.openPRWtCalls) != 0 {
		t.Fatalf("confirm=No must NOT open a PR; got %v", svc.openPRWtCalls)
	}
}

// A freelancer with no resolvable worktree path (argus reported none) opens
// no confirm and calls no service — but gives visible feedback rather than
// a silent nothing.
func TestBridge_OnOpenPR_Freelancer_NoWorktree_NoConfirm_Feedback(t *testing.T) {
	b, m, sel, svc, _, _ := newBridgeUnderTest()
	sel.sel = railSelection{
		Kind:        selRole,
		RoleKind:    "freelance",
		Name:        "feat-x",
		ArgusTaskID: "T7",
		// WorktreePath empty
	}

	b.OnOpenPR()
	b.waitIdle()

	if len(m.confirms) != 0 || len(svc.openPRWtCalls) != 0 {
		t.Fatalf("freelancer with no worktree must not confirm or call; confirms=%d wtCalls=%v", len(m.confirms), svc.openPRWtCalls)
	}
	if m.ErrorCount() != 1 {
		t.Fatalf("freelancer ^p with no worktree must give visible feedback; errors=%v", m.errors)
	}
}

// --- Gap 2: OnNew collects an optional coord mission ---

// Confirming the new-project modal with a name AND a mission must pass BOTH
// through to the new-orchestrator service (spec: "New-project confirm spawns
// argus task" asserts the spawned prompt contains mission="ship F").
func TestBridge_OnNew_NameAndMission_PassesBothThrough(t *testing.T) {
	b, m, _, svc, _, rp := newBridgeUnderTest()
	m.stubForm2Name = "foo"
	m.stubForm2Second = "ship F"

	b.OnNew()
	b.waitIdle()

	if len(svc.newCalls) != 1 {
		t.Fatalf("want 1 NewOrchestrator call, got %d", len(svc.newCalls))
	}
	if svc.newCalls[0].Name != "foo" {
		t.Fatalf("NewOrchestrator.Name: want %q, got %q", "foo", svc.newCalls[0].Name)
	}
	if svc.newCalls[0].Mission != "ship F" {
		t.Fatalf("NewOrchestrator.Mission: want %q, got %q", "ship F", svc.newCalls[0].Mission)
	}
	if rp.Count() != 1 {
		t.Fatalf("expected rail refresh after successful create; got %d", rp.Count())
	}
}

// --- Gap 1: OnResurrect (Enter against an archived coord with Archive visible) ---

// An archived coordinator role with the Archive section visible must show a
// confirm and, on confirm, invoke the resurrect service with that coord role.
func TestBridge_OnResurrect_ArchivedCoordRole_ArchiveVisible_Confirms(t *testing.T) {
	b, m, sel, svc, la, rp := newBridgeUnderTest()
	la.visible = true // Archive section is visible
	sel.sel = railSelection{Kind: selRole, RoleID: 55, Name: "coord", RoleKind: "coordinator", Archived: true}
	m.stubConfirmYes = true

	handled := b.OnResurrect()
	b.waitIdle()

	if !handled {
		t.Fatalf("OnResurrect must report it handled the archived-coord Enter")
	}
	if len(m.confirms) != 1 {
		t.Fatalf("resurrect must open a confirm modal; got %d", len(m.confirms))
	}
	if !stringsContains(m.confirms[0].Title+m.confirms[0].Message, "coord") {
		t.Fatalf("confirm must name the project; got %q / %q", m.confirms[0].Title, m.confirms[0].Message)
	}
	if len(svc.resurrectCalls) != 1 || svc.resurrectCalls[0] != 55 {
		t.Fatalf("want ResurrectOrchestrator(55); got %v", svc.resurrectCalls)
	}
	if rp.Count() != 1 {
		t.Fatalf("resurrect must refresh rail; got %d", rp.Count())
	}
}

// An archived ROOT orchestrator row (selOrchestrator) carries the coord role id
// so resurrect targets the coord role.
func TestBridge_OnResurrect_ArchivedOrchestrator_UsesCoordRoleID(t *testing.T) {
	b, m, sel, svc, la, _ := newBridgeUnderTest()
	la.visible = true
	sel.sel = railSelection{Kind: selOrchestrator, OrchestratorID: 7, CoordRoleID: 91, Name: "foo", Archived: true}
	m.stubConfirmYes = true

	handled := b.OnResurrect()
	b.waitIdle()

	if !handled {
		t.Fatalf("OnResurrect must handle an archived root orchestrator")
	}
	if len(svc.resurrectCalls) != 1 || svc.resurrectCalls[0] != 91 {
		t.Fatalf("want ResurrectOrchestrator(91); got %v", svc.resurrectCalls)
	}
}

// Archive hidden → Enter on an archived coord is NOT a resurrect (it falls
// through to pane-entry). OnResurrect reports it did not handle the key.
func TestBridge_OnResurrect_ArchiveHidden_NotHandled(t *testing.T) {
	b, _, sel, svc, la, _ := newBridgeUnderTest()
	la.visible = false
	sel.sel = railSelection{Kind: selRole, RoleID: 55, Name: "coord", RoleKind: "coordinator", Archived: true}

	if b.OnResurrect() {
		t.Fatalf("Archive hidden must NOT trigger resurrect")
	}
	if len(svc.resurrectCalls) != 0 {
		t.Fatalf("Archive hidden must not call resurrect; got %v", svc.resurrectCalls)
	}
}

// A LIVE (non-archived) coord is never a resurrect target, even with Archive
// visible.
func TestBridge_OnResurrect_LiveCoord_NotHandled(t *testing.T) {
	b, _, sel, svc, la, _ := newBridgeUnderTest()
	la.visible = true
	sel.sel = railSelection{Kind: selRole, RoleID: 55, Name: "coord", RoleKind: "coordinator", Archived: false}

	if b.OnResurrect() {
		t.Fatalf("live coord must NOT trigger resurrect")
	}
	if len(svc.resurrectCalls) != 0 {
		t.Fatalf("live coord must not call resurrect; got %v", svc.resurrectCalls)
	}
}

// An archived WORKER (non-coordinator) is not a resurrect target.
func TestBridge_OnResurrect_ArchivedWorker_NotHandled(t *testing.T) {
	b, _, sel, svc, la, _ := newBridgeUnderTest()
	la.visible = true
	sel.sel = railSelection{Kind: selRole, RoleID: 55, Name: "w1", RoleKind: "worker", Archived: true}

	if b.OnResurrect() {
		t.Fatalf("archived worker must NOT trigger resurrect")
	}
	if len(svc.resurrectCalls) != 0 {
		t.Fatalf("archived worker must not call resurrect; got %v", svc.resurrectCalls)
	}
}

func stringsContains(s, sub string) bool { return strings.Contains(s, sub) }

// --- Deadlock fix: every mutation path hands its blocking work off the
// caller's goroutine. The caller is the tview event loop in production, where
// a blocking svc call + QueueUpdateDraw bounce self-deadlocks the loop
// (tview's QueueUpdate blocks until the queued func runs). Each seam test
// gates the first blocking touch (svc call, modal open, repop) and proves the
// handler returned while the gate was still closed. ---

// returnsWithin runs fn on its own goroutine and fails the test if it does
// not return promptly — the signature of a handler executing its blocking
// work synchronously on the caller.
func returnsWithin(t *testing.T, name string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		fn()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("%s did not return while its blocking work was gated — it runs on the caller (event-loop) goroutine", name)
	}
}

func TestBridge_OnArchive_ReturnsBeforeSvcCompletes(t *testing.T) {
	b, _, sel, svc, _, rp := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selRole, RoleID: 9, Name: "w", RoleKind: "worker"}
	gate := make(chan struct{})
	svc.archiveRoleGate = gate

	returnsWithin(t, "OnArchive", b.OnArchive)

	if rp.Count() != 0 {
		t.Fatalf("refresh fired before the mutation completed; got %d", rp.Count())
	}
	close(gate)
	b.waitIdle()
	if len(svc.archiveRoleCalls) != 1 {
		t.Fatalf("want 1 ArchiveRole call after release; got %d", len(svc.archiveRoleCalls))
	}
	if rp.Count() != 1 {
		t.Fatalf("want exactly 1 refresh after the mutation completed; got %d", rp.Count())
	}
}

func TestBridge_OnStatusAdvance_ReturnsBeforeSvcCompletes(t *testing.T) {
	b, _, sel, svc, _, rp := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selRole, RoleID: 9, Name: "w", RoleKind: "worker"}
	gate := make(chan struct{})
	svc.advanceGate = gate

	returnsWithin(t, "OnStatusAdvance", b.OnStatusAdvance)

	if rp.Count() != 0 {
		t.Fatalf("refresh fired before the status step completed; got %d", rp.Count())
	}
	close(gate)
	b.waitIdle()
	if len(svc.advanceCalls) != 1 || rp.Count() != 1 {
		t.Fatalf("after release: advanceCalls=%d refresh=%d, want 1/1", len(svc.advanceCalls), rp.Count())
	}
}

func TestBridge_OnDelete_ReturnsBeforeConfirmShown(t *testing.T) {
	b, m, sel, svc, _, _ := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selRole, RoleID: 13, Name: "w1", RoleKind: "worker"}
	m.stubConfirmYes = true
	gate := make(chan struct{})
	m.confirmGate = gate

	returnsWithin(t, "OnDelete", b.OnDelete)

	if m.ConfirmCount() != 0 {
		t.Fatalf("confirm modal opened before the handler returned to the event loop")
	}
	close(gate)
	b.waitIdle()
	if m.ConfirmCount() != 1 {
		t.Fatalf("want 1 confirm after release; got %d", m.ConfirmCount())
	}
	if len(svc.deleteRoleCalls) != 1 || svc.deleteRoleCalls[0] != 13 {
		t.Fatalf("want DeleteRole(13) after confirm; got %v", svc.deleteRoleCalls)
	}
}

func TestBridge_OnPrune_ReturnsBeforeListCompletes(t *testing.T) {
	b, m, _, svc, _, _ := newBridgeUnderTest()
	svc.completedAgents = []ops.CompletedAgent{{RoleID: 1, Name: "done-a", ArgusTaskID: "Ta"}}
	m.stubConfirmYes = true
	gate := make(chan struct{})
	svc.listCompletedGate = gate

	returnsWithin(t, "OnPrune", b.OnPrune)

	if m.ConfirmCount() != 0 {
		t.Fatalf("confirm opened before the completed-agents list resolved")
	}
	close(gate)
	b.waitIdle()
	if m.ConfirmCount() != 1 || len(svc.pruneCalls) != 1 {
		t.Fatalf("after release: confirms=%d pruneCalls=%d, want 1/1", m.ConfirmCount(), len(svc.pruneCalls))
	}
}

func TestBridge_OnListAll_ReturnsBeforeRefresh(t *testing.T) {
	b, _, _, _, la, rp := newBridgeUnderTest()
	gate := make(chan struct{})
	rp.gate = gate

	returnsWithin(t, "OnListAll", b.OnListAll)

	if la.toggles != 1 {
		t.Fatalf("toggle must fire synchronously (cheap, in-memory); got %d", la.toggles)
	}
	close(gate)
	b.waitIdle()
	if rp.Count() != 1 {
		t.Fatalf("want 1 refresh after release; got %d", rp.Count())
	}
}

// While one mutation's blocking work is in flight, a second mutation key must
// not fire a concurrent conflicting op — and must NOT be silent about it.
func TestBridge_SecondMutationWhileInFlight_NoOpsWithFeedback(t *testing.T) {
	b, m, sel, svc, _, rp := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selRole, RoleID: 9, Name: "w", RoleKind: "worker"}
	started := make(chan struct{})
	gate := make(chan struct{})
	svc.archiveRoleStarted = started
	svc.archiveRoleGate = gate

	b.OnArchive()
	<-started // the archive svc call is now executing on the bridge's goroutine

	b.OnStatusAdvance() // second mutation while the first is in flight

	close(gate)
	b.waitIdle()

	if len(svc.advanceCalls) != 0 {
		t.Fatalf("second mutation must not fire while one is in flight; got %v", svc.advanceCalls)
	}
	if len(svc.archiveRoleCalls) != 1 {
		t.Fatalf("first mutation must still complete; got %d calls", len(svc.archiveRoleCalls))
	}
	if m.ErrorCount() != 1 || !stringsContains(m.errors[0], "in flight") {
		t.Fatalf("dropped second mutation must give visible feedback; errors=%v", m.errors)
	}
	if rp.Count() != 1 {
		t.Fatalf("want exactly the first mutation's refresh; got %d", rp.Count())
	}
}

// --- Freelancer addressability: a/s/S address the argus task directly ---

func freelancerSel() railSelection {
	return railSelection{
		Kind:         selRole,
		RoleKind:     "freelance",
		RoleID:       0, // freelancers have no hera role
		Name:         "feat-x",
		ArgusTaskID:  "T9",
		WorktreePath: "/tmp/wt/freelance/feat-x",
	}
}

func TestBridge_OnArchive_Freelancer_TogglesArgusTaskDirectly(t *testing.T) {
	b, m, sel, svc, _, rp := newBridgeUnderTest()
	sel.sel = freelancerSel()

	b.OnArchive()
	b.waitIdle()

	if len(svc.toggleArchiveTaskCalls) != 1 ||
		svc.toggleArchiveTaskCalls[0] != (toggleTaskCall{TaskID: "T9", Archived: false}) {
		t.Fatalf("want ToggleArchiveTask(T9, archived=false); got %+v", svc.toggleArchiveTaskCalls)
	}
	if len(svc.archiveRoleCalls)+len(svc.unarchiveRoleCalls)+len(svc.archiveOrchCalls)+len(svc.unarchiveOrchCalls) != 0 {
		t.Fatalf("freelancer `a` must not touch role/orchestrator archive paths")
	}
	if rp.Count() != 1 {
		t.Fatalf("freelancer archive must refresh the rail; got %d", rp.Count())
	}
	if m.ErrorCount() != 0 {
		t.Fatalf("unexpected error modal: %v", m.errors)
	}
}

func TestBridge_OnArchive_ArchivedFreelancer_Unarchives(t *testing.T) {
	b, _, sel, svc, _, _ := newBridgeUnderTest()
	s := freelancerSel()
	s.ArgusArchived = true
	sel.sel = s

	b.OnArchive()
	b.waitIdle()

	if len(svc.toggleArchiveTaskCalls) != 1 ||
		svc.toggleArchiveTaskCalls[0] != (toggleTaskCall{TaskID: "T9", Archived: true}) {
		t.Fatalf("want ToggleArchiveTask(T9, archived=true); got %+v", svc.toggleArchiveTaskCalls)
	}
}

func TestBridge_OnStatus_Freelancer_StepsTaskDirectly(t *testing.T) {
	b, _, sel, svc, _, rp := newBridgeUnderTest()
	sel.sel = freelancerSel()

	b.OnStatusAdvance()
	b.waitIdle()
	b.OnStatusRevert()
	b.waitIdle()

	want := []stepTaskCall{{TaskID: "T9", Advance: true}, {TaskID: "T9", Advance: false}}
	if len(svc.stepTaskCalls) != 2 || svc.stepTaskCalls[0] != want[0] || svc.stepTaskCalls[1] != want[1] {
		t.Fatalf("want %+v; got %+v", want, svc.stepTaskCalls)
	}
	if len(svc.advanceCalls)+len(svc.revertCalls) != 0 {
		t.Fatalf("freelancer s/S must not use the role-binding path")
	}
	if rp.Count() != 2 {
		t.Fatalf("each freelancer status step must refresh; got %d", rp.Count())
	}
}

// --- Header addressability: s/S on an orchestrator header step the coord task ---

func TestBridge_OnStatus_OrchestratorHeader_StepsCoordRole(t *testing.T) {
	b, _, sel, svc, _, rp := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selOrchestrator, OrchestratorID: 1, CoordRoleID: 42, Name: "foo"}

	b.OnStatusAdvance()
	b.waitIdle()
	b.OnStatusRevert()
	b.waitIdle()

	if len(svc.advanceCalls) != 1 || svc.advanceCalls[0] != 42 {
		t.Fatalf("want AdvanceStatus(42) for the coord role; got %v", svc.advanceCalls)
	}
	if len(svc.revertCalls) != 1 || svc.revertCalls[0] != 42 {
		t.Fatalf("want RevertStatus(42) for the coord role; got %v", svc.revertCalls)
	}
	if rp.Count() != 2 {
		t.Fatalf("header status steps must refresh; got %d", rp.Count())
	}
}

func TestBridge_OnStatus_OrchestratorNoCoord_Feedback(t *testing.T) {
	b, m, sel, svc, _, _ := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selOrchestrator, OrchestratorID: 1, Name: "foo"} // CoordRoleID 0

	b.OnStatusAdvance()
	b.waitIdle()

	if len(svc.advanceCalls)+len(svc.stepTaskCalls) != 0 {
		t.Fatalf("coord-less header must not step anything")
	}
	if m.ErrorCount() != 1 {
		t.Fatalf("coord-less header s must give visible feedback; errors=%v", m.errors)
	}
}

// --- Never silent: non-applicable keys give visible feedback ---

func TestBridge_OnStatus_NoSelection_Feedback(t *testing.T) {
	b, m, _, svc, _, _ := newBridgeUnderTest()
	// sel.sel zero-value: Kind selNone (e.g. the Archive separator row)

	b.OnStatusAdvance()
	b.waitIdle()

	if len(svc.advanceCalls)+len(svc.stepTaskCalls) != 0 {
		t.Fatalf("selNone must not step anything")
	}
	if m.ErrorCount() != 1 || !stringsContains(m.errors[0], "not applicable") {
		t.Fatalf("s on a non-addressable row must give feedback; errors=%v", m.errors)
	}
}

func TestBridge_OnArchive_NoSelection_Feedback(t *testing.T) {
	b, m, _, svc, _, rp := newBridgeUnderTest()

	b.OnArchive()
	b.waitIdle()

	if len(svc.archiveOrchCalls)+len(svc.unarchiveOrchCalls)+len(svc.archiveRoleCalls)+len(svc.unarchiveRoleCalls)+len(svc.toggleArchiveTaskCalls) != 0 {
		t.Fatalf("selNone must not call any archive verb")
	}
	if m.ErrorCount() != 1 || !stringsContains(m.errors[0], "not applicable") {
		t.Fatalf("a on a non-addressable row must give feedback; errors=%v", m.errors)
	}
	if rp.Count() != 0 {
		t.Fatalf("no mutation, no refresh; got %d", rp.Count())
	}
}

func TestBridge_OnDelete_Freelancer_Feedback(t *testing.T) {
	b, m, sel, svc, _, _ := newBridgeUnderTest()
	sel.sel = freelancerSel()

	b.OnDelete()
	b.waitIdle()

	if m.ConfirmCount() != 0 {
		t.Fatalf("freelancer ^d must not open a destructive confirm")
	}
	if len(svc.deleteRoleCalls)+len(svc.deleteOrchCalls) != 0 {
		t.Fatalf("freelancer ^d must not delete anything; got %v / %v", svc.deleteRoleCalls, svc.deleteOrchCalls)
	}
	if m.ErrorCount() != 1 {
		t.Fatalf("freelancer ^d must give visible feedback; errors=%v", m.errors)
	}
}

func TestBridge_OnRename_Freelancer_Feedback(t *testing.T) {
	b, m, sel, svc, _, _ := newBridgeUnderTest()
	sel.sel = freelancerSel()

	b.OnRename()
	b.waitIdle()

	if len(m.inputs) != 0 {
		t.Fatalf("freelancer r must not open the rename input")
	}
	if len(svc.renameRoleCalls)+len(svc.renameOrchCalls) != 0 {
		t.Fatalf("freelancer r must not rename anything")
	}
	if m.ErrorCount() != 1 {
		t.Fatalf("freelancer r must give visible feedback; errors=%v", m.errors)
	}
}

func TestBridge_OnOpenPR_NoTarget_Feedback(t *testing.T) {
	b, m, sel, svc, _, _ := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selOrchestrator, OrchestratorID: 1, Name: "foo"} // no coord role

	b.OnOpenPR()
	b.waitIdle()

	if m.ConfirmCount() != 0 || len(svc.openPRCalls)+len(svc.openPRWtCalls) != 0 {
		t.Fatalf("coord-less ^p must not confirm or open a PR")
	}
	if m.ErrorCount() != 1 {
		t.Fatalf("coord-less ^p must give visible feedback; errors=%v", m.errors)
	}
}

// --- `l` syncs the session's archive visibility into the repopulator so the
// Archive section actually reveals (the toggle previously flipped ops state
// the rail never read). ---

func TestBridge_OnListAll_SyncsArchiveVisibilityToRepopulator(t *testing.T) {
	b, _, _, _, _, rp := newBridgeUnderTest()

	b.OnListAll()
	b.waitIdle()
	if got := rp.ShowArchivedValues(); len(got) != 1 || got[0] != true {
		t.Fatalf("first toggle must sync SetShowArchived(true); got %v", got)
	}

	b.OnListAll()
	b.waitIdle()
	if got := rp.ShowArchivedValues(); len(got) != 2 || got[1] != false {
		t.Fatalf("second toggle must sync SetShowArchived(false); got %v", got)
	}
}

// Spec (mixed-coord-repair): `a` on a mixed-coord header — a displayed-active
// orchestrator whose coord task is argus-archived — REPAIRS by unarchiving
// the coord's argus task directly (task-direct verb, no hera DB write), and
// must NOT cascade-archive the orchestrator the operator sees as active.
func TestBridge_OnArchive_MixedCoordHeader_RepairsViaTaskUnarchive(t *testing.T) {
	b, _, sel, svc, _, rp := newBridgeUnderTest()
	sel.sel = railSelection{
		Kind: selOrchestrator, OrchestratorID: 5, Name: "foo",
		CoordTaskID: "T1", CoordArgusArchived: true,
	}

	b.OnArchive()
	b.waitIdle()

	if len(svc.toggleArchiveTaskCalls) != 1 ||
		svc.toggleArchiveTaskCalls[0] != (toggleTaskCall{TaskID: "T1", Archived: true}) {
		t.Fatalf("want ToggleArchiveTask(T1, archived=true) — the repair unarchive; got %+v", svc.toggleArchiveTaskCalls)
	}
	if len(svc.archiveOrchCalls) != 0 {
		t.Fatalf("mixed-coord header must NOT cascade-archive; got %v", svc.archiveOrchCalls)
	}
	if len(svc.unarchiveOrchCalls) != 0 {
		t.Fatalf("repair is task-direct, not an orchestrator unarchive; got %v", svc.unarchiveOrchCalls)
	}
	if rp.Count() != 1 {
		t.Fatalf("expected rail refresh after the repair; got %d", rp.Count())
	}
}

// Spec (mixed-coord-repair): once repaired (coord task no longer argus-
// archived), `a` on the header behaves exactly as today — the standard
// cascade-archive.
func TestBridge_OnArchive_RepairedHeader_CascadesAsBefore(t *testing.T) {
	b, _, sel, svc, _, _ := newBridgeUnderTest()
	sel.sel = railSelection{
		Kind: selOrchestrator, OrchestratorID: 5, Name: "foo",
		CoordTaskID: "T1", CoordArgusArchived: false,
	}

	b.OnArchive()
	b.waitIdle()

	if len(svc.archiveOrchCalls) != 1 || svc.archiveOrchCalls[0] != 5 {
		t.Fatalf("repaired header must cascade-archive; got %v", svc.archiveOrchCalls)
	}
	if len(svc.toggleArchiveTaskCalls) != 0 {
		t.Fatalf("repaired header must not fire the task-direct repair; got %+v", svc.toggleArchiveTaskCalls)
	}
}

// Spec (mixed-coord-repair): an ARCHIVED orchestrator whose coord task is
// also argus-archived is NOT the mixed state (both sides agree) — `a` keeps
// the standard unarchive-orchestrator direction.
func TestBridge_OnArchive_ArchivedHeaderWithArchivedCoordTask_UnarchivesOrch(t *testing.T) {
	b, _, sel, svc, _, _ := newBridgeUnderTest()
	sel.sel = railSelection{
		Kind: selOrchestrator, OrchestratorID: 5, Name: "foo", Archived: true,
		CoordTaskID: "T1", CoordArgusArchived: true,
	}

	b.OnArchive()
	b.waitIdle()

	if len(svc.unarchiveOrchCalls) != 1 || svc.unarchiveOrchCalls[0] != 5 {
		t.Fatalf("archived header must UnarchiveOrchestrator(5); got %v", svc.unarchiveOrchCalls)
	}
	if len(svc.toggleArchiveTaskCalls) != 0 {
		t.Fatalf("archived-both-sides header must not fire the task-direct repair; got %+v", svc.toggleArchiveTaskCalls)
	}
}

// --- OnNewWorker ---

// newBridgeWithRowSelector constructs a bridge with a fakeRowSelector wired
// so tests can assert auto-select behavior after SpawnWorker succeeds.
func newBridgeWithRowSelector() (*mutationBridge, *fakeModals, *fakeSelector, *fakeMutationService, *fakeRepopulator, *fakeRowSelector) {
	m := &fakeModals{}
	sel := &fakeSelector{}
	svc := &fakeMutationService{}
	la := &fakeListAll{}
	rp := &fakeRepopulator{}
	rowSel := &fakeRowSelector{}
	b := newMutationBridge(context.Background(), m, sel, svc, la, rp, nil, nil)
	b.rowSel = rowSel
	return b, m, sel, svc, rp, rowSel
}

// TestBridge_OnNewWorker_CoordRow_OpensInputModal asserts that when a
// coordinator row is selected and OnNewWorker is called, an input modal is
// opened for the prompt.
func TestBridge_OnNewWorker_CoordRow_OpensInputModal(t *testing.T) {
	b, m, sel, _, _, _ := newBridgeWithRowSelector()
	sel.sel = railSelection{
		Kind:           selOrchestrator,
		OrchestratorID: 1,
		CoordRoleID:    10,
		Name:           "foo",
	}
	m.stubInputNotOpened = true // just record the open, don't submit

	b.OnNewWorker()
	b.waitIdle()

	m.mu.Lock()
	openCount := len(m.inputs)
	m.mu.Unlock()

	if openCount != 1 {
		t.Fatalf("expected 1 input modal open for coordinator row; got %d", openCount)
	}
}

// TestBridge_OnNewWorker_AgentRow_ResolvesToCoord asserts that when an agent
// row is selected, the spawn targets that agent's coordinator (OrchestratorID).
func TestBridge_OnNewWorker_AgentRow_ResolvesToCoord(t *testing.T) {
	b, m, sel, svc, _, _ := newBridgeWithRowSelector()
	// Agent row: Kind=selRole, RoleKind=worker, OrchestratorID=7, CoordRoleID=20.
	sel.sel = railSelection{
		Kind:           selRole,
		OrchestratorID: 7,
		RoleID:         99,
		CoordRoleID:    20,
		Name:           "some-agent",
		RoleKind:       "worker",
	}
	m.stubInputAnswer = "implement X"

	b.OnNewWorker()
	b.waitIdle()

	svc.mu.Lock()
	calls := svc.spawnWorkerCalls
	svc.mu.Unlock()

	if len(calls) != 1 {
		t.Fatalf("expected 1 SpawnWorker call; got %d", len(calls))
	}
	if calls[0].TargetOrchestratorID != 7 {
		t.Fatalf("TargetOrchestratorID: want 7 (agent's coord), got %d", calls[0].TargetOrchestratorID)
	}
	if calls[0].CoordRoleID != 20 {
		t.Fatalf("CoordRoleID: want 20, got %d", calls[0].CoordRoleID)
	}
	if calls[0].Prompt != "implement X" {
		t.Fatalf("Prompt: want %q, got %q", "implement X", calls[0].Prompt)
	}
}

// TestBridge_OnNewWorker_FreelanceRow_NotApplicable asserts that a freelancer
// row triggers a "not applicable" notice and no SpawnWorker call.
func TestBridge_OnNewWorker_FreelanceRow_NotApplicable(t *testing.T) {
	b, m, sel, svc, _, _ := newBridgeWithRowSelector()
	sel.sel = railSelection{
		Kind:     selRole,
		RoleID:   5,
		Name:     "dangling-task",
		RoleKind: "freelance",
	}

	b.OnNewWorker()
	b.waitIdle()

	svc.mu.Lock()
	spawnCount := len(svc.spawnWorkerCalls)
	svc.mu.Unlock()

	m.mu.Lock()
	errorCount := len(m.errors)
	m.mu.Unlock()

	if spawnCount != 0 {
		t.Fatalf("freelance row must not trigger SpawnWorker; got %d calls", spawnCount)
	}
	if errorCount == 0 {
		t.Fatal("freelance row must surface a not-applicable notice")
	}
}

// TestBridge_OnNewWorker_NoneSelection_NotApplicable asserts that a selNone
// selection (e.g. separator/expando) shows not-applicable and no spawn call.
func TestBridge_OnNewWorker_NoneSelection_NotApplicable(t *testing.T) {
	b, m, sel, svc, _, _ := newBridgeWithRowSelector()
	sel.sel = railSelection{Kind: selNone}

	b.OnNewWorker()
	b.waitIdle()

	svc.mu.Lock()
	spawnCount := len(svc.spawnWorkerCalls)
	svc.mu.Unlock()

	m.mu.Lock()
	errorCount := len(m.errors)
	m.mu.Unlock()

	if spawnCount != 0 {
		t.Fatalf("selNone row must not trigger SpawnWorker; got %d calls", spawnCount)
	}
	if errorCount == 0 {
		t.Fatal("selNone row must surface a not-applicable notice")
	}
}

// TestBridge_OnNewWorker_OrchHeaderNoCoordRole_NotApplicable asserts D3: an
// orchestrator header with no coord role (CoordRoleID==0) surfaces a notice and
// issues no op call.
func TestBridge_OnNewWorker_OrchHeaderNoCoordRole_NotApplicable(t *testing.T) {
	b, m, sel, svc, _, _ := newBridgeWithRowSelector()
	sel.sel = railSelection{
		Kind:           selOrchestrator,
		OrchestratorID: 1,
		CoordRoleID:    0, // no coord role
		Name:           "foo",
	}

	b.OnNewWorker()
	b.waitIdle()

	svc.mu.Lock()
	spawnCount := len(svc.spawnWorkerCalls)
	svc.mu.Unlock()
	m.mu.Lock()
	inputs := len(m.inputs)
	errs := len(m.errors)
	m.mu.Unlock()

	if spawnCount != 0 {
		t.Fatalf("orch header with no coord role must not trigger SpawnWorker; got %d calls", spawnCount)
	}
	if inputs != 0 {
		t.Fatalf("orch header with no coord role must not open the prompt modal; got %d", inputs)
	}
	if errs == 0 {
		t.Fatal("orch header with no coord role must surface a not-applicable notice")
	}
}

// TestBridge_OnNewWorker_SubCoordNoChildOrch_NotApplicable asserts D3: a
// sub-coordinator row (RoleKind=coordinator) with no child orchestrator
// (ChildOrchestratorID==0) surfaces a notice and issues no op call.
func TestBridge_OnNewWorker_SubCoordNoChildOrch_NotApplicable(t *testing.T) {
	b, m, sel, svc, _, _ := newBridgeWithRowSelector()
	sel.sel = railSelection{
		Kind:                selRole,
		OrchestratorID:      1,
		RoleID:              5,
		CoordRoleID:         10,
		Name:                "sub",
		RoleKind:            "coordinator",
		ChildOrchestratorID: 0, // promoted coord row with no resolvable child
	}

	b.OnNewWorker()
	b.waitIdle()

	svc.mu.Lock()
	spawnCount := len(svc.spawnWorkerCalls)
	svc.mu.Unlock()
	m.mu.Lock()
	inputs := len(m.inputs)
	errs := len(m.errors)
	m.mu.Unlock()

	if spawnCount != 0 {
		t.Fatalf("sub-coord row with no child orch must not trigger SpawnWorker; got %d calls", spawnCount)
	}
	if inputs != 0 {
		t.Fatalf("sub-coord row with no child orch must not open the prompt modal; got %d", inputs)
	}
	if errs == 0 {
		t.Fatal("sub-coord row with no child orch must surface a not-applicable notice")
	}
}

// TestBridge_OnNewWorker_EmptyPrompt_SurfacesNotice asserts D1: confirming the
// spawn-worker modal with an empty OR whitespace-only prompt surfaces a
// dismissible notice (NOT a silent close) and issues no SpawnWorker call.
func TestBridge_OnNewWorker_EmptyPrompt_SurfacesNotice(t *testing.T) {
	for _, answer := range []string{"", "   ", "\t\n"} {
		b, m, sel, svc, _, _ := newBridgeWithRowSelector()
		sel.sel = railSelection{
			Kind:           selOrchestrator,
			OrchestratorID: 1,
			CoordRoleID:    10,
			Name:           "foo",
		}
		m.stubInputAnswer = answer

		b.OnNewWorker()
		b.waitIdle()

		svc.mu.Lock()
		spawnCount := len(svc.spawnWorkerCalls)
		svc.mu.Unlock()
		if spawnCount != 0 {
			t.Fatalf("answer=%q: empty/whitespace prompt must not trigger SpawnWorker; got %d calls", answer, spawnCount)
		}

		m.mu.Lock()
		errs := append([]string(nil), m.errors...)
		m.mu.Unlock()
		if len(errs) == 0 {
			t.Fatalf("answer=%q: empty/whitespace prompt must surface a dismissible notice, not a silent close", answer)
		}
		if !strings.Contains(errs[len(errs)-1], "prompt is required") {
			t.Fatalf("answer=%q: notice should say \"prompt is required\"; got %q", answer, errs[len(errs)-1])
		}
	}
}

// TestBridge_OnNewWorker_Success_QueuesAutoSelect asserts that after a
// successful spawn, the bridge QUEUES the new role id for deferred auto-select
// (applied on the next broadcaster-driven rail repopulate, not immediately).
func TestBridge_OnNewWorker_Success_QueuesAutoSelect(t *testing.T) {
	b, m, sel, svc, _, rowSel := newBridgeWithRowSelector()
	sel.sel = railSelection{
		Kind:           selOrchestrator,
		OrchestratorID: 1,
		CoordRoleID:    10,
		Name:           "foo",
	}
	m.stubInputAnswer = "do the thing"
	svc.spawnWorkerResp = &ops.SpawnWorkerResult{RoleID: 42, ArgusTaskID: "task-1"}

	b.OnNewWorker()
	b.waitIdle()

	if rowSel.LastQueued() != 42 {
		t.Fatalf("auto-select: want queued RoleID 42, got %d", rowSel.LastQueued())
	}
}

// TestBridge_OnNewWorker_SpawnError_SurfacesErrorModal asserts that a
// SpawnWorker failure shows an error modal (not a freeze).
func TestBridge_OnNewWorker_SpawnError_SurfacesErrorModal(t *testing.T) {
	b, m, sel, svc, _, _ := newBridgeWithRowSelector()
	sel.sel = railSelection{
		Kind:           selOrchestrator,
		OrchestratorID: 1,
		CoordRoleID:    10,
		Name:           "foo",
	}
	m.stubInputAnswer = "do the thing"
	svc.spawnWorkerErr = errors.New("argus exploded")

	b.OnNewWorker()
	b.waitIdle()

	m.mu.Lock()
	errs := m.errors
	m.mu.Unlock()

	if len(errs) == 0 {
		t.Fatal("expected an error modal when SpawnWorker fails")
	}
	if !strings.Contains(errs[0], "argus exploded") {
		t.Fatalf("error modal should contain the error text; got %q", errs[0])
	}
}

// --- Story 1: `P` pin toggle (OnPin) ---

func TestBridge_OnPin_UnpinnedOrchestrator_Pins(t *testing.T) {
	b, _, sel, svc, _, rp := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selOrchestrator, OrchestratorID: 5, Name: "foo"}

	b.OnPin()
	b.waitIdle()

	if len(svc.pinOrchCalls) != 1 || svc.pinOrchCalls[0] != 5 {
		t.Fatalf("want PinOrchestrator(5); got %v", svc.pinOrchCalls)
	}
	if len(svc.unpinOrchCalls) != 0 {
		t.Fatalf("unpinned orchestrator must pin, not unpin; got %v", svc.unpinOrchCalls)
	}
	if rp.Count() != 1 {
		t.Fatalf("expected rail refresh; got %d", rp.Count())
	}
}

func TestBridge_OnPin_PinnedOrchestrator_Unpins(t *testing.T) {
	b, _, sel, svc, _, _ := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selOrchestrator, OrchestratorID: 5, Name: "foo", Pinned: true}

	b.OnPin()
	b.waitIdle()

	if len(svc.unpinOrchCalls) != 1 || svc.unpinOrchCalls[0] != 5 {
		t.Fatalf("want UnpinOrchestrator(5); got %v", svc.unpinOrchCalls)
	}
	if len(svc.pinOrchCalls) != 0 {
		t.Fatalf("pinned orchestrator must unpin, not re-pin; got %v", svc.pinOrchCalls)
	}
}

func TestBridge_OnPin_UnpinnedRole_Pins(t *testing.T) {
	b, _, sel, svc, _, _ := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selRole, RoleID: 9, Name: "w", RoleKind: "worker"}

	b.OnPin()
	b.waitIdle()

	if len(svc.pinRoleCalls) != 1 || svc.pinRoleCalls[0] != 9 {
		t.Fatalf("want PinRole(9); got %v", svc.pinRoleCalls)
	}
	if len(svc.unpinRoleCalls) != 0 {
		t.Fatalf("unpinned role must pin, not unpin; got %v", svc.unpinRoleCalls)
	}
}

func TestBridge_OnPin_PinnedRole_Unpins(t *testing.T) {
	b, _, sel, svc, _, _ := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selRole, RoleID: 9, Name: "w", RoleKind: "worker", Pinned: true}

	b.OnPin()
	b.waitIdle()

	if len(svc.unpinRoleCalls) != 1 || svc.unpinRoleCalls[0] != 9 {
		t.Fatalf("want UnpinRole(9); got %v", svc.unpinRoleCalls)
	}
	if len(svc.pinRoleCalls) != 0 {
		t.Fatalf("pinned role must unpin, not re-pin; got %v", svc.pinRoleCalls)
	}
}

func TestBridge_OnPin_Freelancer_NotApplicable(t *testing.T) {
	b, m, sel, svc, _, _ := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selRole, RoleID: 0, Name: "free", RoleKind: "freelance", ArgusTaskID: "T9"}

	b.OnPin()
	b.waitIdle()

	if len(svc.pinRoleCalls)+len(svc.unpinRoleCalls) != 0 {
		t.Fatalf("P on a freelancer must not call any pin verb; pin=%v unpin=%v", svc.pinRoleCalls, svc.unpinRoleCalls)
	}
	if m.ErrorCount() != 1 || !stringsContains(m.errors[0], "hera-side") {
		t.Fatalf("P on a freelancer must give the hera-side gap feedback; errors=%v", m.errors)
	}
}

// --- `J` adopt (operator-side rail adoption) ---

// On a freelancer, `J` lists the active orchestrators, opens the picker, and a
// selection adopts the freelancer into the chosen orchestrator (worker
// binding), passing the task id, default role name, repo, and worktree.
func TestBridge_OnAdopt_Freelancer_PicksOrchestrator_Adopts(t *testing.T) {
	b, m, sel, svc, _, rp := newBridgeUnderTest()
	sel.sel = railSelection{
		Kind:         selRole,
		RoleKind:     "freelance",
		RoleID:       0,
		Name:         "feat-x",
		ArgusTaskID:  "T7",
		WorktreePath: "/tmp/wt/feat-x",
		Project:      "Hera",
	}
	svc.listOrchs = []*ops.Orchestrator{{ID: 3, Name: "alpha"}, {ID: 5, Name: "beta"}}
	m.stubSelectIndex = 1 // pick "beta"

	b.OnAdopt()
	b.waitIdle()

	if m.SelectCount() != 1 {
		t.Fatalf("J on a freelancer must open the picker once; got %d", m.SelectCount())
	}
	if got := m.selects[0].Items; len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Fatalf("picker must list active orchestrator names; got %v", got)
	}
	if !stringsContains(m.selects[0].Title, "feat-x") {
		t.Fatalf("picker title must name the freelancer; got %q", m.selects[0].Title)
	}
	if len(svc.adoptCalls) != 1 {
		t.Fatalf("selecting an orchestrator must adopt once; got %d", len(svc.adoptCalls))
	}
	in := svc.adoptCalls[0]
	if in.ArgusTaskID != "T7" || in.OrchestratorID != 5 || in.RoleName != "feat-x" ||
		in.ArgusProject != "Hera" || in.WorktreePath != "/tmp/wt/feat-x" {
		t.Fatalf("adopt input mismatch: %+v", in)
	}
	if rp.Count() != 1 {
		t.Fatalf("a successful adopt must refresh the rail once; got %d", rp.Count())
	}
}

// Cancelling the picker (Esc) adopts nothing.
func TestBridge_OnAdopt_Freelancer_Cancel_NoAdopt(t *testing.T) {
	b, m, sel, svc, _, _ := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selRole, RoleKind: "freelance", Name: "feat-x", ArgusTaskID: "T7"}
	svc.listOrchs = []*ops.Orchestrator{{ID: 3, Name: "alpha"}}
	m.stubSelectCancel = true

	b.OnAdopt()
	b.waitIdle()

	if len(svc.adoptCalls) != 0 {
		t.Fatalf("cancelling the picker must not adopt; got %v", svc.adoptCalls)
	}
}

// `J` on a non-freelancer row (managed worker) gives visible feedback and does
// not list orchestrators or open the picker.
func TestBridge_OnAdopt_ManagedWorker_NotApplicable(t *testing.T) {
	b, m, sel, svc, _, _ := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selRole, RoleID: 9, Name: "w", RoleKind: "worker"}

	b.OnAdopt()
	b.waitIdle()

	if m.SelectCount() != 0 || len(svc.adoptCalls) != 0 {
		t.Fatalf("J on a managed worker must not pick or adopt; selects=%d adopts=%d", m.SelectCount(), len(svc.adoptCalls))
	}
	if m.ErrorCount() != 1 || !stringsContains(m.errors[0], "only freelancers") {
		t.Fatalf("J on a managed worker must give freelancer-only feedback; errors=%v", m.errors)
	}
}

// `J` on an orchestrator header gives feedback (only freelancers adopt).
func TestBridge_OnAdopt_Orchestrator_NotApplicable(t *testing.T) {
	b, m, sel, svc, _, _ := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selOrchestrator, OrchestratorID: 7, Name: "alpha"}

	b.OnAdopt()
	b.waitIdle()

	if m.SelectCount() != 0 || len(svc.adoptCalls) != 0 {
		t.Fatalf("J on an orchestrator must not pick or adopt")
	}
	if m.ErrorCount() != 1 || !stringsContains(m.errors[0], "only freelancers") {
		t.Fatalf("J on an orchestrator must give feedback; errors=%v", m.errors)
	}
}

// A freelancer with no argus task id gives feedback, opens no picker.
func TestBridge_OnAdopt_Freelancer_NoTaskID_Feedback(t *testing.T) {
	b, m, sel, svc, _, _ := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selRole, RoleKind: "freelance", Name: "feat-x"} // no ArgusTaskID

	b.OnAdopt()
	b.waitIdle()

	if m.SelectCount() != 0 || len(svc.adoptCalls) != 0 {
		t.Fatalf("freelancer with no task id must not pick or adopt")
	}
	if m.ErrorCount() != 1 || !stringsContains(m.errors[0], "argus task id") {
		t.Fatalf("freelancer with no task id must give feedback; errors=%v", m.errors)
	}
}

// No active orchestrators → feedback, no picker, no adopt.
func TestBridge_OnAdopt_NoActiveOrchestrators_Feedback(t *testing.T) {
	b, m, sel, svc, _, _ := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selRole, RoleKind: "freelance", Name: "feat-x", ArgusTaskID: "T7"}
	svc.listOrchs = nil

	b.OnAdopt()
	b.waitIdle()

	if m.SelectCount() != 0 || len(svc.adoptCalls) != 0 {
		t.Fatalf("no orchestrators must not pick or adopt")
	}
	if m.ErrorCount() != 1 || !stringsContains(m.errors[0], "no active coordinators") {
		t.Fatalf("no orchestrators must give feedback; errors=%v", m.errors)
	}
}

// A failing adopt surfaces an error modal and refreshes nothing.
func TestBridge_OnAdopt_ServiceError_ShowsErrorModal(t *testing.T) {
	b, m, sel, svc, _, rp := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selRole, RoleKind: "freelance", Name: "feat-x", ArgusTaskID: "T7"}
	svc.listOrchs = []*ops.Orchestrator{{ID: 3, Name: "alpha"}}
	svc.adoptErr = errors.New("already bound")
	m.stubSelectIndex = 0

	b.OnAdopt()
	b.waitIdle()

	if m.ErrorCount() != 1 || !stringsContains(m.errors[0], "already bound") {
		t.Fatalf("adopt error must surface in an error modal; errors=%v", m.errors)
	}
	if rp.Count() != 0 {
		t.Fatalf("a failed adopt must not refresh the rail; got %d", rp.Count())
	}
}

// OnAdopt returns to the event loop before the (blocking) orchestrator listing
// completes — proving the work runs off-loop (no deadlock).
func TestBridge_OnAdopt_ReturnsBeforeSvcCompletes(t *testing.T) {
	b, _, sel, svc, _, _ := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selRole, RoleKind: "freelance", Name: "feat-x", ArgusTaskID: "T7"}
	gate := make(chan struct{})
	svc.listOrchsGate = gate
	svc.listOrchs = []*ops.Orchestrator{{ID: 3, Name: "alpha"}}

	returnsWithin(t, "OnAdopt", b.OnAdopt)

	close(gate)
	b.waitIdle()
}
