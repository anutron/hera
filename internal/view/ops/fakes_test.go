package ops

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// fakeDB is an in-memory DB for ops tests. Methods are minimal — they
// implement the DB interface and nothing more.
type fakeDB struct {
	mu            sync.Mutex
	nextOrchID    int64
	nextRoleID    int64
	nextBindingID int64

	orchestrators map[int64]*Orchestrator
	roles         map[int64]*Role
	bindings      map[int64]*Binding // keyed by binding id; live = ended bindings purged or marked via separate map

	// renameOrchCalls / renameRoleCalls track DAO-level mutation calls
	// for tests that need to assert "only the name column is touched".
	renameOrchCalls []renameCall
	renameRoleCalls []renameCall

	archiveOrchCalls   []int64
	unarchiveOrchCalls []int64
	archiveRoleCalls   []int64
	unarchiveRoleCalls []int64
	endBindingCalls    []endCall
}

type renameCall struct {
	ID      int64
	NewName string
}

type endCall struct {
	BindingID int64
	Reason    string
}

func newFakeDB() *fakeDB {
	return &fakeDB{
		orchestrators: map[int64]*Orchestrator{},
		roles:         map[int64]*Role{},
		bindings:      map[int64]*Binding{},
	}
}

// seedOrchestrator returns a copy installed into the fake. Mutations
// after this call don't affect the seeded row unless made via the
// fakeDB's own methods.
func (f *fakeDB) seedOrchestrator(name string, archived bool) *Orchestrator {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextOrchID++
	o := &Orchestrator{ID: f.nextOrchID, Name: name, Archived: archived}
	f.orchestrators[o.ID] = o
	return o
}

func (f *fakeDB) seedRole(orchID int64, name string, kind RoleKind, argusProject string, archived bool) *Role {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextRoleID++
	r := &Role{
		ID:             f.nextRoleID,
		OrchestratorID: orchID,
		Name:           name,
		Kind:           kind,
		ArgusProject:   argusProject,
		Mission:        "test mission",
		Constraints:    "test constraints",
		Archived:       archived,
	}
	f.roles[r.ID] = r
	return r
}

func (f *fakeDB) seedBinding(roleID int64, argusTaskID, worktreePath string) *Binding {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextBindingID++
	b := &Binding{
		ID:           f.nextBindingID,
		RoleID:       roleID,
		ArgusTaskID:  argusTaskID,
		WorktreePath: worktreePath,
	}
	f.bindings[b.ID] = b
	return b
}

func (f *fakeDB) GetOrchestratorByID(ctx context.Context, id int64) (*Orchestrator, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.orchestrators[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *o
	return &cp, nil
}

func (f *fakeDB) GetOrchestratorByName(ctx context.Context, name string) (*Orchestrator, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, o := range f.orchestrators {
		if o.Name == name && !o.Archived {
			cp := *o
			return &cp, nil
		}
	}
	return nil, ErrNotFound
}

func (f *fakeDB) ListOrchestrators(ctx context.Context) ([]*Orchestrator, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []*Orchestrator{}
	for _, o := range f.orchestrators {
		if o.Archived {
			continue
		}
		cp := *o
		out = append(out, &cp)
	}
	return out, nil
}

func (f *fakeDB) ArchiveOrchestrator(ctx context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.archiveOrchCalls = append(f.archiveOrchCalls, id)
	o, ok := f.orchestrators[id]
	if !ok {
		return ErrNotFound
	}
	o.Archived = true
	return nil
}

func (f *fakeDB) UnarchiveOrchestrator(ctx context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unarchiveOrchCalls = append(f.unarchiveOrchCalls, id)
	o, ok := f.orchestrators[id]
	if !ok {
		return ErrNotFound
	}
	o.Archived = false
	return nil
}

func (f *fakeDB) RenameOrchestrator(ctx context.Context, id int64, newName string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.renameOrchCalls = append(f.renameOrchCalls, renameCall{ID: id, NewName: newName})
	o, ok := f.orchestrators[id]
	if !ok {
		return ErrNotFound
	}
	o.Name = newName
	return nil
}

func (f *fakeDB) GetRoleByID(ctx context.Context, id int64) (*Role, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.roles[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *r
	return &cp, nil
}

func (f *fakeDB) ListRolesByOrchestrator(ctx context.Context, orchID int64) ([]*Role, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []*Role{}
	for _, r := range f.roles {
		if r.OrchestratorID != orchID {
			continue
		}
		if r.Archived {
			continue
		}
		cp := *r
		out = append(out, &cp)
	}
	return out, nil
}

func (f *fakeDB) ArchiveRole(ctx context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.archiveRoleCalls = append(f.archiveRoleCalls, id)
	r, ok := f.roles[id]
	if !ok {
		return ErrNotFound
	}
	r.Archived = true
	return nil
}

func (f *fakeDB) UnarchiveRole(ctx context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unarchiveRoleCalls = append(f.unarchiveRoleCalls, id)
	r, ok := f.roles[id]
	if !ok {
		return ErrNotFound
	}
	r.Archived = false
	return nil
}

func (f *fakeDB) RenameRole(ctx context.Context, id int64, newName string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.renameRoleCalls = append(f.renameRoleCalls, renameCall{ID: id, NewName: newName})
	r, ok := f.roles[id]
	if !ok {
		return ErrNotFound
	}
	r.Name = newName
	return nil
}

func (f *fakeDB) GetLiveBindingByRole(ctx context.Context, roleID int64) (*Binding, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, b := range f.bindings {
		if b.RoleID == roleID {
			cp := *b
			return &cp, nil
		}
	}
	return nil, ErrNotFound
}

func (f *fakeDB) ListLiveBindings(ctx context.Context) ([]*Binding, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []*Binding{}
	for _, b := range f.bindings {
		cp := *b
		out = append(out, &cp)
	}
	return out, nil
}

func (f *fakeDB) EndBinding(ctx context.Context, bindingID int64, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.endBindingCalls = append(f.endBindingCalls, endCall{BindingID: bindingID, Reason: reason})
	if _, ok := f.bindings[bindingID]; !ok {
		return ErrNotFound
	}
	// "Ended" bindings are removed from the live-binding map so
	// subsequent GetLiveBindingByRole returns ErrNotFound.
	delete(f.bindings, bindingID)
	return nil
}

// fakeArgus implements ArgusClient. Records every call and returns
// pre-configured responses or errors.
type fakeArgus struct {
	mu sync.Mutex

	createCalls []CreateTaskRequest
	createResp  *CreatedTask
	createErr   error

	archiveCalls   []string
	archiveErr     error
	unarchiveCalls []string
	unarchiveErr   error

	deleteCalls []string
	deleteErr   error

	// statuses maps taskID -> current status, consulted by GetTaskStatus
	// and updated by SetTaskStatus. setStatusCalls records every write.
	statuses       map[string]string
	getStatusErr   error
	setStatusCalls []setStatusCall
	setStatusErr   error
}

type setStatusCall struct {
	TaskID string
	Status string
}

func (a *fakeArgus) CreateTask(ctx context.Context, req CreateTaskRequest) (*CreatedTask, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.createCalls = append(a.createCalls, req)
	if a.createErr != nil {
		return nil, a.createErr
	}
	if a.createResp != nil {
		cp := *a.createResp
		return &cp, nil
	}
	return &CreatedTask{ID: fmt.Sprintf("task-%d", len(a.createCalls)), Name: req.Name}, nil
}

func (a *fakeArgus) ArchiveTask(ctx context.Context, taskID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.archiveCalls = append(a.archiveCalls, taskID)
	return a.archiveErr
}

func (a *fakeArgus) UnarchiveTask(ctx context.Context, taskID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.unarchiveCalls = append(a.unarchiveCalls, taskID)
	return a.unarchiveErr
}

func (a *fakeArgus) DeleteTask(ctx context.Context, taskID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.deleteCalls = append(a.deleteCalls, taskID)
	return a.deleteErr
}

func (a *fakeArgus) GetTaskStatus(ctx context.Context, taskID string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.getStatusErr != nil {
		return "", a.getStatusErr
	}
	if a.statuses == nil {
		return "", nil
	}
	return a.statuses[taskID], nil
}

func (a *fakeArgus) SetTaskStatus(ctx context.Context, taskID, status string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.setStatusCalls = append(a.setStatusCalls, setStatusCall{TaskID: taskID, Status: status})
	if a.setStatusErr != nil {
		return "", a.setStatusErr
	}
	if a.statuses == nil {
		a.statuses = map[string]string{}
	}
	a.statuses[taskID] = status
	return status, nil
}

// fakePRCreator records CreatePR invocations. url / err are returned on
// every call.
type fakePRCreator struct {
	mu    sync.Mutex
	calls []string
	url   string
	err   error
}

func (p *fakePRCreator) CreatePR(ctx context.Context, worktreePath string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, worktreePath)
	return p.url, p.err
}

// fakeWorktreeRemover records every Remove invocation. err is returned
// on every call (nil unless tests set it).
type fakeWorktreeRemover struct {
	mu    sync.Mutex
	calls []string
	err   error
}

func (w *fakeWorktreeRemover) Remove(ctx context.Context, worktreePath string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls = append(w.calls, worktreePath)
	return w.err
}

// fakeLogger records every Printf call's formatted output for tests
// that need to assert audit-log content.
type fakeLogger struct {
	mu       sync.Mutex
	messages []string
}

func (l *fakeLogger) Printf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.messages = append(l.messages, fmt.Sprintf(format, args...))
}

// newTestService wires a fully-populated Service with all-fake deps.
// Returns the service plus each fake so tests can seed and assert.
func newTestService() (*Service, *fakeDB, *fakeArgus, *fakeWorktreeRemover, *fakeLogger) {
	db := newFakeDB()
	a := &fakeArgus{}
	w := &fakeWorktreeRemover{}
	l := &fakeLogger{}
	s := NewService(db, a, w, l)
	return s, db, a, w, l
}

// asValidation extracts the *ValidationError from err, or fails the
// test if err is not a validation error.
func asValidation(err error) *ValidationError {
	if err == nil {
		return nil
	}
	var v *ValidationError
	if errors.As(err, &v) {
		return v
	}
	return nil
}
