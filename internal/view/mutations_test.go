package view

import (
	"context"
	"errors"
	"strings"
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

	stubConfirmYes     bool
	stubConfirmNotOpen bool

	// stubForm2* drive the two-field form (ShowForm2). The fake fires the
	// submit callback with these values as soon as ShowForm2 is called.
	form2Calls       []fakeForm2Call
	stubForm2Name    string
	stubForm2Second  string
	stubForm2Cancel  bool
	stubForm2NotOpen bool
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

func (s *fakeMutationService) ListCompletedAgents(_ context.Context) ([]ops.CompletedAgent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.completedAgents, s.listCompletedErr
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
	defer s.mu.Unlock()
	s.advanceCalls = append(s.advanceCalls, id)
	return "in_progress", s.advanceErr
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

// --- Stage P: OnDelete destructive confirm + child-agent warning ---

func TestBridge_OnDelete_Orchestrator_ConfirmNamesAndWarnsChildren(t *testing.T) {
	b, m, sel, _, _, _ := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selOrchestrator, OrchestratorID: 7, Name: "foo", ChildCount: 3}
	m.stubConfirmNotOpen = true // open the modal but do not auto-confirm

	b.OnDelete()

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

	if len(svc.pruneCalls) != 0 {
		t.Fatalf("confirm=No must NOT prune; got %+v", svc.pruneCalls)
	}
}

func TestBridge_OnPrune_NoCompleted_NoConfirmNoPrune(t *testing.T) {
	b, m, _, svc, _, _ := newBridgeUnderTest()
	svc.completedAgents = nil

	b.OnPrune()

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

	if len(svc.revertCalls) != 1 || svc.revertCalls[0] != 9 {
		t.Fatalf("want RevertStatus(9); got %v", svc.revertCalls)
	}
}

func TestBridge_OnStatus_NonRole_NoCall(t *testing.T) {
	b, _, sel, svc, _, _ := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selOrchestrator, OrchestratorID: 1, Name: "foo"}

	b.OnStatusAdvance()
	b.OnStatusRevert()

	if len(svc.advanceCalls)+len(svc.revertCalls) != 0 {
		t.Fatalf("status keys must no-op on non-role selection")
	}
}

// --- Stage P: OnOpenPR ---

func TestBridge_OnOpenPR_ConfirmYes_OpensPR(t *testing.T) {
	b, m, sel, svc, _, _ := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selRole, RoleID: 9, Name: "w"}
	m.stubConfirmYes = true

	b.OnOpenPR()

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

	if len(svc.openPRCalls) != 1 || svc.openPRCalls[0] != 77 {
		t.Fatalf("want OpenPR(77) for the sub-coordinator role; got %v", svc.openPRCalls)
	}
}

// An orchestrator selection with no coord role (CoordRoleID 0) still no-ops —
// there is no coordinator task to open a PR from.
func TestBridge_OnOpenPR_OrchestratorNoCoord_NoConfirm(t *testing.T) {
	b, m, sel, svc, _, _ := newBridgeUnderTest()
	sel.sel = railSelection{Kind: selOrchestrator, OrchestratorID: 1, Name: "foo"}

	b.OnOpenPR()

	if len(m.confirms) != 0 || len(svc.openPRCalls) != 0 {
		t.Fatalf("open-PR on a coord-less orchestrator must no-op; confirms=%d prCalls=%v", len(m.confirms), svc.openPRCalls)
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

	if len(svc.openPRWtCalls) != 0 {
		t.Fatalf("confirm=No must NOT open a PR; got %v", svc.openPRWtCalls)
	}
}

// A freelancer with no resolvable worktree path (argus reported none) no-ops:
// there is nothing to open a PR from, so no confirm and no service call.
func TestBridge_OnOpenPR_Freelancer_NoWorktree_NoConfirm(t *testing.T) {
	b, m, sel, svc, _, _ := newBridgeUnderTest()
	sel.sel = railSelection{
		Kind:        selRole,
		RoleKind:    "freelance",
		Name:        "feat-x",
		ArgusTaskID: "T7",
		// WorktreePath empty
	}

	b.OnOpenPR()

	if len(m.confirms) != 0 || len(svc.openPRWtCalls) != 0 {
		t.Fatalf("freelancer with no worktree must no-op; confirms=%d wtCalls=%v", len(m.confirms), svc.openPRWtCalls)
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
