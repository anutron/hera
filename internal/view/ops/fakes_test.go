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
	deleteOrchCalls    []int64
	archiveRoleCalls   []int64
	unarchiveRoleCalls []int64
	deleteRoleCalls    []int64
	pinOrchCalls       []int64
	unpinOrchCalls     []int64
	pinRoleCalls       []int64
	unpinRoleCalls     []int64
	endBindingCalls    []endCall

	upsertRoleStatusCalls []upsertRoleStatusCall
	upsertRoleStatusErr   error

	// createRoleErr / createBindingErr, when set, make CreateRole /
	// CreateBinding fail — backing the post-create insert-failure tests
	// (the orphan must NOT be rolled back).
	createRoleErr    error
	createBindingErr error
}

type upsertRoleStatusCall struct {
	RoleID int64
	Status string
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
		Prompt:         "test prompt",
		Archived:       archived,
	}
	f.roles[r.ID] = r
	return r
}

func (f *fakeDB) seedBinding(roleID int64, argusTaskID, worktreePath string) *Binding {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextBindingID++
	// Denormalize the orchestrator id from the bound role, mirroring the real
	// DAO (bindings carry orchestrator_id for per-orchestrator uniqueness).
	var orchID int64
	if r, ok := f.roles[roleID]; ok {
		orchID = r.OrchestratorID
	}
	b := &Binding{
		ID:             f.nextBindingID,
		RoleID:         roleID,
		OrchestratorID: orchID,
		ArgusTaskID:    argusTaskID,
		WorktreePath:   worktreePath,
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

func (f *fakeDB) CreateOrchestrator(ctx context.Context, name string) (*Orchestrator, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Idempotent: return existing active row if name is already in use.
	for _, o := range f.orchestrators {
		if o.Name == name && !o.Archived {
			cp := *o
			return &cp, nil
		}
	}
	f.nextOrchID++
	o := &Orchestrator{ID: f.nextOrchID, Name: name}
	f.orchestrators[o.ID] = o
	return o, nil
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

func (f *fakeDB) DeleteOrchestratorByID(ctx context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteOrchCalls = append(f.deleteOrchCalls, id)
	if _, ok := f.orchestrators[id]; !ok {
		return ErrNotFound
	}
	delete(f.orchestrators, id)
	// Cascade: delete roles and their bindings, mirroring ON DELETE CASCADE.
	for rID, r := range f.roles {
		if r.OrchestratorID != id {
			continue
		}
		for bID, b := range f.bindings {
			if b.RoleID == rID {
				delete(f.bindings, bID)
			}
		}
		for bID, b := range f.ended {
			if b.RoleID == rID {
				delete(f.ended, bID)
			}
		}
		delete(f.roles, rID)
	}
	return nil
}

func (f *fakeDB) PinOrchestrator(ctx context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pinOrchCalls = append(f.pinOrchCalls, id)
	o, ok := f.orchestrators[id]
	if !ok {
		return ErrNotFound
	}
	o.Pinned = true
	o.Archived = false
	return nil
}

func (f *fakeDB) UnpinOrchestrator(ctx context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unpinOrchCalls = append(f.unpinOrchCalls, id)
	o, ok := f.orchestrators[id]
	if !ok {
		return ErrNotFound
	}
	o.Pinned = false
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

// SubtreeOrchIDs mirrors db.SubtreeOrchIDs over the in-memory maps: BFS from
// rootOrchID following LIVE coordinator bindings that share an argus task id
// with a LIVE binding in the parent frontier. Only active (non-archived) child
// coordinators and orchestrators extend the frontier, matching the SQL.
func (f *fakeDB) SubtreeOrchIDs(ctx context.Context, rootOrchID int64) ([]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	seen := map[int64]struct{}{rootOrchID: {}}
	result := []int64{rootOrchID}
	frontier := []int64{rootOrchID}

	for len(frontier) > 0 {
		frontierSet := map[int64]struct{}{}
		for _, id := range frontier {
			frontierSet[id] = struct{}{}
		}

		// Live argus task ids bound to any role in the frontier orchestrators.
		parentTasks := map[string]struct{}{}
		for _, b := range f.bindings {
			role, ok := f.roles[b.RoleID]
			if !ok {
				continue
			}
			if _, in := frontierSet[role.OrchestratorID]; in {
				parentTasks[b.ArgusTaskID] = struct{}{}
			}
		}

		var next []int64
		for _, b := range f.bindings {
			if _, shared := parentTasks[b.ArgusTaskID]; !shared {
				continue
			}
			role, ok := f.roles[b.RoleID]
			if !ok || role.Kind != KindCoordinator || role.Archived {
				continue
			}
			childOrch := role.OrchestratorID
			if _, ok := seen[childOrch]; ok {
				continue
			}
			o, ok := f.orchestrators[childOrch]
			if !ok || o.Archived {
				continue
			}
			seen[childOrch] = struct{}{}
			result = append(result, childOrch)
			next = append(next, childOrch)
		}
		frontier = next
	}
	return result, nil
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

func (f *fakeDB) GetRoleByOrchestratorAndName(ctx context.Context, orchID int64, name string) (*Role, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.roles {
		if r.OrchestratorID == orchID && r.Name == name && !r.Archived {
			cp := *r
			return &cp, nil
		}
	}
	return nil, ErrNotFound
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

func (f *fakeDB) PinRole(ctx context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pinRoleCalls = append(f.pinRoleCalls, id)
	r, ok := f.roles[id]
	if !ok {
		return ErrNotFound
	}
	r.Pinned = true
	r.Archived = false
	return nil
}

func (f *fakeDB) UnpinRole(ctx context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unpinRoleCalls = append(f.unpinRoleCalls, id)
	r, ok := f.roles[id]
	if !ok {
		return ErrNotFound
	}
	r.Pinned = false
	return nil
}

func (f *fakeDB) DeleteRoleByID(ctx context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteRoleCalls = append(f.deleteRoleCalls, id)
	if _, ok := f.roles[id]; !ok {
		return ErrNotFound
	}
	delete(f.roles, id)
	for bID, b := range f.bindings {
		if b.RoleID == id {
			delete(f.bindings, bID)
		}
	}
	for bID, b := range f.ended {
		if b.RoleID == id {
			delete(f.ended, bID)
		}
	}
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

func (f *fakeDB) UpsertRoleStatus(ctx context.Context, roleID int64, status string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upsertRoleStatusCalls = append(f.upsertRoleStatusCalls, upsertRoleStatusCall{RoleID: roleID, Status: status})
	return f.upsertRoleStatusErr
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
		Prompt:         in.Prompt,
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
	b := &Binding{
		ID:             f.nextBindingID,
		RoleID:         in.RoleID,
		OrchestratorID: in.OrchestratorID,
		ArgusTaskID:    in.ArgusTaskID,
		WorktreePath:   in.WorktreePath,
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

// postInputCall records one PostTaskInput invocation.
type postInputCall struct {
	TaskID string
	Bytes  []byte
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

	// meta records PutTaskMeta writes keyed by "taskID\x00key"; putMetaErr,
	// when set, makes every PutTaskMeta fail (the best-effort path).
	meta       map[string]string
	putMetaErr error

	// PostTaskInput tracking for auto-submit CR tests.
	postInputCalls []postInputCall
	postInputErr   error

	// ListProjects / ListBackends stubs.
	listProjectsResp []string
	listProjectsErr  error
	listBackendsResp []string
	listBackendsErr  error

	// RestartTask tracking.
	restartCalls []string
	restartErr   error
}

func (a *fakeArgus) PutTaskMeta(ctx context.Context, taskID, key, value string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.putMetaErr != nil {
		return a.putMetaErr
	}
	if a.meta == nil {
		a.meta = map[string]string{}
	}
	a.meta[taskID+"\x00"+key] = value
	return nil
}

// metaFor returns the recorded meta value for (taskID, key), or "".
func (a *fakeArgus) metaFor(taskID, key string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.meta[taskID+"\x00"+key]
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

func (a *fakeArgus) PostTaskInput(_ context.Context, taskID string, bytes []byte) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.postInputCalls = append(a.postInputCalls, postInputCall{TaskID: taskID, Bytes: bytes})
	if a.postInputErr != nil {
		return 0, a.postInputErr
	}
	return len(bytes), nil
}

func (a *fakeArgus) ListProjects(_ context.Context) ([]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.listProjectsResp, a.listProjectsErr
}

func (a *fakeArgus) ListBackends(_ context.Context) ([]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.listBackendsResp, a.listBackendsErr
}

func (a *fakeArgus) RestartTask(_ context.Context, taskID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.restartCalls = append(a.restartCalls, taskID)
	return a.restartErr
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
