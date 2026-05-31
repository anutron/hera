package view

import (
	"context"
	"errors"
	"sync"
	"testing"

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

	stubConfirmYes    bool
	stubConfirmNotOpen bool
}

type fakeInputCall struct {
	Title, Label, Initial string
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

func (f *fakeModals) ShowConfirm(title, message string, onYes func(), onNo func()) {
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

func (f *fakeModals) ShowError(message string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errors = append(f.errors, message)
}

type fakeSelector struct{ sel railSelection }

func (f *fakeSelector) CurrentRailSelection() railSelection { return f.sel }

type fakeRepopulator struct {
	mu    sync.Mutex
	count int
}

func (r *fakeRepopulator) RepopulateRail() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.count++
}

func (r *fakeRepopulator) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.count
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

	newCalls               []ops.NewOrchestratorInput
	newErr                 error
	renameOrchCalls        []renameOrchCall
	renameOrchErr          error
	renameRoleCalls        []renameRoleCall
	renameRoleErr          error
	deleteOrchCalls        []int64
	deleteOrchErr          error
	deleteRoleCalls        []int64
	deleteRoleErr          error
	toggleArchiveOrchCalls []int64
	toggleArchiveOrchErr   error
	toggleArchiveRoleCalls []int64
	toggleArchiveRoleErr   error
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

func (s *fakeMutationService) ToggleArchiveOrchestrator(_ context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.toggleArchiveOrchCalls = append(s.toggleArchiveOrchCalls, id)
	return s.toggleArchiveOrchErr
}

func (s *fakeMutationService) ToggleArchiveRole(_ context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.toggleArchiveRoleCalls = append(s.toggleArchiveRoleCalls, id)
	return s.toggleArchiveRoleErr
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
	m.stubInputAnswer = "foo"

	b.OnNew()

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
	m.stubInputAnswer = ""

	b.OnNew()

	if len(svc.newCalls) != 0 {
		t.Fatalf("empty name must NOT call NewOrchestrator; got %d", len(svc.newCalls))
	}
	if rp.Count() != 0 {
		t.Fatalf("empty name must NOT refresh; got %d", rp.Count())
	}
}

func TestBridge_OnNew_ServiceError_ShowsErrorModal(t *testing.T) {
	b, m, _, svc, _, rp := newBridgeUnderTest()
	m.stubInputAnswer = "foo"
	svc.newErr = errors.New("argus down")

	b.OnNew()

	if len(m.errors) != 1 || m.errors[0] != "argus down" {
		t.Fatalf("want error modal with %q; got %v", "argus down", m.errors)
	}
	if rp.Count() != 0 {
		t.Fatalf("error path must NOT refresh; got %d", rp.Count())
	}
}

func TestBridge_OnNew_Cancel_NoServiceCall(t *testing.T) {
	b, m, _, svc, _, _ := newBridgeUnderTest()
	m.stubInputCancel = true

	b.OnNew()

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

	if len(svc.renameOrchCalls) != 0 {
		t.Fatalf("empty name must NOT call RenameOrchestrator; got %d", len(svc.renameOrchCalls))
	}
}

func TestBridge_OnRename_UnchangedName_NoServiceCall(t *testing.T) {
	b, m, sel, svc, _, _ := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selRole, RoleID: 42, Name: "same"}
	m.stubInputAnswer = "same"

	b.OnRename()

	if len(svc.renameRoleCalls) != 0 {
		t.Fatalf("unchanged name must NOT call RenameRole; got %d", len(svc.renameRoleCalls))
	}
}

func TestBridge_OnRename_NoSelection_NoServiceCall_NoModal(t *testing.T) {
	b, m, _, svc, _, _ := newBridgeUnderTest()
	// sel.sel is zero-value (Kind == selNone)

	b.OnRename()

	if len(m.inputs) != 0 {
		t.Fatalf("no selection must not open input modal; got %d", len(m.inputs))
	}
	if len(svc.renameOrchCalls)+len(svc.renameRoleCalls) != 0 {
		t.Fatalf("no selection must not call any rename")
	}
}

// --- OnDelete ---

func TestBridge_OnDelete_ConfirmYes_Orchestrator_CallsDeleteOrchestrator(t *testing.T) {
	b, m, sel, svc, _, rp := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selOrchestrator, OrchestratorID: 7, Name: "foo"}
	m.stubConfirmYes = true

	b.OnDelete()

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

	if len(svc.deleteOrchCalls) != 0 {
		t.Fatalf("confirm=No must NOT call Delete; got %d", len(svc.deleteOrchCalls))
	}
	if rp.Count() != 0 {
		t.Fatalf("confirm=No must NOT refresh; got %d", rp.Count())
	}
}

func TestBridge_OnDelete_NoSelection_NoModal(t *testing.T) {
	b, m, _, svc, _, _ := newBridgeUnderTest()

	b.OnDelete()

	if len(m.confirms) != 0 {
		t.Fatalf("no selection must NOT open confirm modal; got %d", len(m.confirms))
	}
	if len(svc.deleteOrchCalls)+len(svc.deleteRoleCalls) != 0 {
		t.Fatalf("no selection must not call any delete")
	}
}

func TestBridge_OnDelete_ServiceError_ShowsErrorModal(t *testing.T) {
	b, m, sel, svc, _, rp := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selRole, RoleID: 13, Name: "w1"}
	m.stubConfirmYes = true
	svc.deleteRoleErr = errors.New("worktree busy")

	b.OnDelete()

	if len(m.errors) != 1 || m.errors[0] != "worktree busy" {
		t.Fatalf("want error modal; got %v", m.errors)
	}
	if rp.Count() != 0 {
		t.Fatalf("error path must NOT refresh; got %d", rp.Count())
	}
}

// --- OnArchive ---

func TestBridge_OnArchive_Orchestrator_TogglesArchive(t *testing.T) {
	b, _, sel, svc, _, rp := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selOrchestrator, OrchestratorID: 5, Name: "foo"}

	b.OnArchive()

	if len(svc.toggleArchiveOrchCalls) != 1 || svc.toggleArchiveOrchCalls[0] != 5 {
		t.Fatalf("want ToggleArchiveOrchestrator(5); got %v", svc.toggleArchiveOrchCalls)
	}
	if rp.Count() != 1 {
		t.Fatalf("expected rail refresh; got %d", rp.Count())
	}
}

func TestBridge_OnArchive_Role_TogglesArchive(t *testing.T) {
	b, _, sel, svc, _, rp := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selRole, RoleID: 9, Name: "w"}

	b.OnArchive()

	if len(svc.toggleArchiveRoleCalls) != 1 || svc.toggleArchiveRoleCalls[0] != 9 {
		t.Fatalf("want ToggleArchiveRole(9); got %v", svc.toggleArchiveRoleCalls)
	}
	if rp.Count() != 1 {
		t.Fatalf("expected rail refresh; got %d", rp.Count())
	}
}

func TestBridge_OnArchive_NoSelection_NoServiceCall(t *testing.T) {
	b, _, _, svc, _, rp := newBridgeUnderTest()

	b.OnArchive()

	if len(svc.toggleArchiveOrchCalls)+len(svc.toggleArchiveRoleCalls) != 0 {
		t.Fatalf("no selection must not call ToggleArchive*")
	}
	if rp.Count() != 0 {
		t.Fatalf("no selection must not refresh; got %d", rp.Count())
	}
}

func TestBridge_OnArchive_ServiceError_ShowsErrorModal(t *testing.T) {
	b, m, sel, svc, _, _ := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selRole, RoleID: 9, Name: "w"}
	svc.toggleArchiveRoleErr = errors.New("argus 500")

	b.OnArchive()

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
