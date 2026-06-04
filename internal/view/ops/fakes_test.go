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
	bindings      map[int64]*Binding // keyed by binding id; live bindings only
	ended         map[int64]*Binding // bindings moved here by EndBinding / seedEndedBinding

	// renameOrchCalls / renameRoleCalls track DAO-level mutation calls
	// for tests that need to assert "only the name column is touched".
	renameOrchCalls []renameCall
	renameRoleCalls []renameCall

	archiveOrchCalls   []int64
	unarchiveOrchCalls []int64
	archiveRoleCalls   []int64
	unarchiveRoleCalls []int64
	endBindingCalls    []endCall

	// createRoleErr / createBindingErr, when set, make CreateRole /
	// CreateBinding fail — backing the post-create insert-failure tests
	// (the orphan must NOT be rolled back).
	createRoleErr    error
	createBindingErr error
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
		ended:         map[int64]*Binding{},
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

// seedEndedBinding installs a binding that has already ended (the
// archived-role shape: the live lookup misses it, the latest lookup
// resolves it). Later seeds are more recent — GetLatestBindingByRole
// uses the binding id as the recency proxy.
func (f *fakeDB) seedEndedBinding(roleID int64, argusTaskID, worktreePath string) *Binding {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextBindingID++
	b := &Binding{
		ID:           f.nextBindingID,
		RoleID:       roleID,
		ArgusTaskID:  argusTaskID,
		WorktreePath: worktreePath,
	}
	f.ended[b.ID] = b
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

func (f *fakeDB) ListRolesByOrchestratorInclusive(ctx context.Context, orchID int64) ([]*Role, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []*Role{}
	for _, r := range f.roles {
		if r.OrchestratorID != orchID {
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

func (f *fakeDB) GetLatestBindingByRole(ctx context.Context, roleID int64) (*Binding, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Most recent = highest binding id (the fake's recency proxy for
	// started_at), across live AND ended bindings — mirroring the DAO's
	// "regardless of ended_at" contract.
	var latest *Binding
	for _, m := range []map[int64]*Binding{f.bindings, f.ended} {
		for _, b := range m {
			if b.RoleID != roleID {
				continue
			}
			if latest == nil || b.ID > latest.ID {
				latest = b
			}
		}
	}
	if latest == nil {
		return nil, ErrNotFound
	}
	cp := *latest
	return &cp, nil
}

func (f *fakeDB) ListLiveBindingsByTask(ctx context.Context, argusTaskID string) ([]*Binding, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []*Binding{}
	for _, b := range f.bindings {
		if b.ArgusTaskID != argusTaskID {
			continue
		}
		cp := *b
		out = append(out, &cp)
	}
	return out, nil
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

func (f *fakeDB) CreateRole(ctx context.Context, in CreateRoleInput) (*Role, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createRoleErr != nil {
		return nil, f.createRoleErr
	}
	// Check for an existing active role with the same (orchestrator_id, name).
	for _, r := range f.roles {
		if r.OrchestratorID == in.OrchestratorID && r.Name == in.Name && !r.Archived {
			if r.Kind != in.Kind {
				return nil, fmt.Errorf("role %q exists with kind %q, not %q", in.Name, r.Kind, in.Kind)
			}
			cp := *r
			return &cp, nil
		}
	}
	f.nextRoleID++
	r := &Role{
		ID:             f.nextRoleID,
		OrchestratorID: in.OrchestratorID,
		Name:           in.Name,
		Kind:           in.Kind,
		ArgusProject:   in.ArgusProject,
		Mission:        in.Mission,
		Constraints:    in.Constraints,
	}
	f.roles[r.ID] = r
	return r, nil
}

func (f *fakeDB) CreateBinding(ctx context.Context, in CreateBindingInput) (*Binding, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createBindingErr != nil {
		return nil, f.createBindingErr
	}
	f.nextBindingID++
	orchID := in.OrchestratorID
	if orchID == 0 {
		// Derive from the role.
		if r, ok := f.roles[in.RoleID]; ok {
			orchID = r.OrchestratorID
		}
	}
	b := &Binding{
		ID:           f.nextBindingID,
		RoleID:       in.RoleID,
		ArgusTaskID:  in.ArgusTaskID,
		WorktreePath: in.WorktreePath,
	}
	f.bindings[b.ID] = b
	return b, nil
}

func (f *fakeDB) EndBinding(ctx context.Context, bindingID int64, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.endBindingCalls = append(f.endBindingCalls, endCall{BindingID: bindingID, Reason: reason})
	b, ok := f.bindings[bindingID]
	if !ok {
		return ErrNotFound
	}
	// "Ended" bindings move out of the live-binding map (subsequent
	// GetLiveBindingByRole returns ErrNotFound) but stay resolvable via
	// GetLatestBindingByRole, mirroring the real DAO.
	delete(f.bindings, bindingID)
	f.ended[bindingID] = b
	return nil
}

// fakeArgus implements ArgusClient. Records every call and returns
// pre-configured responses or errors.
type fakeArgus struct {
	mu sync.Mutex

	createCalls []CreateTaskRequest
	createResp  *CreatedTask
	createErr   error

	// getTaskResp / getTaskErr back the GetTask implementation used by SpawnWorker.
	getTaskResp  *TaskDetails
	getTaskErr   error
	getTaskCalls []string

	archiveCalls   []string
	archiveErr     error
	unarchiveCalls []string
	unarchiveErr   error

	// archiveErrByTask / unarchiveErrByTask override archiveErr /
	// unarchiveErr for specific task ids — backing mixed live/pruned
	// cascades where only some tasks 404.
	archiveErrByTask   map[string]error
	unarchiveErrByTask map[string]error

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

func (a *fakeArgus) GetTask(ctx context.Context, taskID string) (*TaskDetails, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.getTaskCalls = append(a.getTaskCalls, taskID)
	if a.getTaskErr != nil {
		return nil, a.getTaskErr
	}
	if a.getTaskResp != nil {
		cp := *a.getTaskResp
		return &cp, nil
	}
	return &TaskDetails{ID: taskID, WorktreePath: ""}, nil
}

func (a *fakeArgus) ArchiveTask(ctx context.Context, taskID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.archiveCalls = append(a.archiveCalls, taskID)
	if err, ok := a.archiveErrByTask[taskID]; ok {
		return err
	}
	return a.archiveErr
}

func (a *fakeArgus) UnarchiveTask(ctx context.Context, taskID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.unarchiveCalls = append(a.unarchiveCalls, taskID)
	if err, ok := a.unarchiveErrByTask[taskID]; ok {
		return err
	}
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
