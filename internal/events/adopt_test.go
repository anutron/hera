package events

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/anutron/hera/internal/argus"
	"github.com/anutron/hera/internal/db"
)

// fakeArgus is an httptest-backed argus stub for adopt tests. It serves:
//   - GET /api/tasks/{id}/meta?namespace=hera
//   - GET /api/tasks/{id}
//   - PUT /api/tasks/{id}/meta
//   - GET /api/tasks
type fakeArgus struct {
	mu        sync.Mutex
	tasks     map[string]argus.Task
	taskMeta  map[string]map[string]string // task_id → key → value (hera ns)
	putWrites []putRecord
}

type putRecord struct {
	taskID string
	key    string
	value  string
}

func newFakeArgus() *fakeArgus {
	return &fakeArgus{
		tasks:    map[string]argus.Task{},
		taskMeta: map[string]map[string]string{},
	}
}

func (f *fakeArgus) addTask(t argus.Task) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tasks[t.ID] = t
}

func (f *fakeArgus) setMeta(taskID, key, value string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.taskMeta[taskID]; !ok {
		f.taskMeta[taskID] = map[string]string{}
	}
	f.taskMeta[taskID][key] = value
}

func (f *fakeArgus) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/tasks", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		var out struct {
			Tasks []argus.Task `json:"tasks"`
		}
		for _, t := range f.tasks {
			out.Tasks = append(out.Tasks, t)
		}
		_ = json.NewEncoder(w).Encode(out)
	})
	mux.HandleFunc("/api/tasks/", func(w http.ResponseWriter, r *http.Request) {
		// Parse /api/tasks/{id} or /api/tasks/{id}/meta or .../input
		rest := strings.TrimPrefix(r.URL.Path, "/api/tasks/")
		segs := strings.SplitN(rest, "/", 2)
		taskID := segs[0]
		sub := ""
		if len(segs) == 2 {
			sub = segs[1]
		}

		f.mu.Lock()
		defer f.mu.Unlock()

		switch {
		case sub == "meta" && r.Method == "GET":
			ns := r.URL.Query().Get("namespace")
			var entries []argus.MetaEntry
			for k, v := range f.taskMeta[taskID] {
				if ns == "" || ns == "hera" {
					entries = append(entries, argus.MetaEntry{
						Namespace: "hera", Key: k, Value: v,
					})
				}
			}
			_ = json.NewEncoder(w).Encode(struct {
				Entries []argus.MetaEntry `json:"entries"`
			}{entries})
		case sub == "meta" && r.Method == "PUT":
			var body argus.PutTaskMetaInput
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.putWrites = append(f.putWrites, putRecord{taskID: taskID, key: body.Key, value: body.Value})
			if _, ok := f.taskMeta[taskID]; !ok {
				f.taskMeta[taskID] = map[string]string{}
			}
			f.taskMeta[taskID][body.Key] = body.Value
			w.WriteHeader(http.StatusOK)
		case sub == "" && r.Method == "GET":
			t, ok := f.tasks[taskID]
			if !ok {
				http.Error(w, `{"error":"not found"}`, 404)
				return
			}
			_ = json.NewEncoder(w).Encode(t)
		default:
			http.NotFound(w, r)
		}
	})
	return mux
}

// adoptTestEnv wires fake argus + hera DB + adopt handler.
type adoptTestEnv struct {
	fake    *fakeArgus
	srv     *httptest.Server
	client  *argus.Client
	db      *db.DB
	handler *AdoptHandler
}

func setupAdopt(t *testing.T) *adoptTestEnv {
	t.Helper()
	fake := newFakeArgus()
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	dbPath := filepath.Join(t.TempDir(), "hera.sqlite")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	client := argus.New(srv.URL, "tok")
	handler := NewAdoptHandler(client, database, nil)
	return &adoptTestEnv{fake: fake, srv: srv, client: client, db: database, handler: handler}
}

// fixtureCoordinator registers an orchestrator + coordinator role + live
// binding for a coordinator task.
func fixtureCoordinator(t *testing.T, e *adoptTestEnv, taskID string) *db.Role {
	ctx := context.Background()
	orch, err := e.db.Orchestrators.Create(ctx, "foo")
	if err != nil {
		t.Fatalf("orch.Create: %v", err)
	}
	role, err := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coordinator", Kind: db.KindCoordinator,
		ArgusProject: "hera",
	})
	if err != nil {
		t.Fatalf("role.Create: %v", err)
	}
	if _, err := e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: role.ID, ArgusTaskID: taskID, WorktreePath: "/tmp/coord",
	}); err != nil {
		t.Fatalf("binding.Create: %v", err)
	}
	e.fake.addTask(argus.Task{ID: taskID, Name: "coordinator", Project: "hera", WorktreePath: "/tmp/coord"})
	return role
}

func linkCreatedEvent(parent, child string, id int64) argus.Event {
	payload, _ := json.Marshal(LinkCreatedPayload{Child: child, Parent: parent})
	return argus.Event{ID: id, Type: TypeLinkCreated, TaskID: child, Payload: payload}
}

func TestAdopt_HappyPath_RoleAndBindingCreated(t *testing.T) {
	ctx := context.Background()
	e := setupAdopt(t)
	parentTask := "task-coord"
	childTask := "task-f2"
	fixtureCoordinator(t, e, parentTask)
	e.fake.addTask(argus.Task{ID: childTask, Name: "f2-impl", Project: "frontend", WorktreePath: "/tmp/f2"})
	e.fake.setMeta(childTask, MetaKeyRole, string(db.KindWorker))
	e.fake.setMeta(childTask, MetaKeyMission, "implement F2")
	e.fake.setMeta(childTask, MetaKeyConstraints, "no breaking changes")

	e.handler.HandleEvent(ctx, linkCreatedEvent(parentTask, childTask, 100))

	// Role should exist with mission/constraints populated.
	orch, _ := e.db.Orchestrators.GetByName(ctx, "foo")
	role, err := e.db.Roles.GetByOrchestratorAndName(ctx, orch.ID, "f2-impl")
	if err != nil {
		t.Fatalf("role lookup failed: %v", err)
	}
	if role.Kind != db.KindWorker {
		t.Fatalf("role.Kind = %s", role.Kind)
	}
	if role.Mission != "implement F2" {
		t.Fatalf("role.Mission = %q", role.Mission)
	}
	if role.Constraints != "no breaking changes" {
		t.Fatalf("role.Constraints = %q", role.Constraints)
	}
	if role.ArgusProject != "frontend" {
		t.Fatalf("role.ArgusProject = %q", role.ArgusProject)
	}

	// Binding should be live and reference the worker task.
	bnd, err := e.db.Bindings.GetLiveByTaskID(ctx, childTask)
	if err != nil {
		t.Fatalf("binding lookup: %v", err)
	}
	if bnd.RoleID != role.ID {
		t.Fatalf("binding.RoleID mismatch")
	}

	// Role meta should have been mirrored to argus.
	e.fake.mu.Lock()
	defer e.fake.mu.Unlock()
	found := false
	for _, w := range e.fake.putWrites {
		if w.taskID == childTask && w.key == MetaKeyRole && w.value == string(db.KindWorker) {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected role meta to be mirrored to argus; writes = %+v", e.fake.putWrites)
	}
}

func TestAdopt_MissingMeta_NotAdopted(t *testing.T) {
	ctx := context.Background()
	e := setupAdopt(t)
	parentTask := "task-coord"
	childTask := "task-stray"
	fixtureCoordinator(t, e, parentTask)
	e.fake.addTask(argus.Task{ID: childTask, Name: "stray", Project: "p", WorktreePath: "/tmp/stray"})
	// NO meta set for childTask.

	e.handler.HandleEvent(ctx, linkCreatedEvent(parentTask, childTask, 100))

	orch, _ := e.db.Orchestrators.GetByName(ctx, "foo")
	_, err := e.db.Roles.GetByOrchestratorAndName(ctx, orch.ID, "stray")
	if err == nil {
		t.Fatalf("expected role NOT to be created without hera.role meta")
	}
}

func TestAdopt_MetaSaysNotWorker_NotAdopted(t *testing.T) {
	ctx := context.Background()
	e := setupAdopt(t)
	parentTask := "task-coord"
	childTask := "task-something-else"
	fixtureCoordinator(t, e, parentTask)
	e.fake.addTask(argus.Task{ID: childTask, Name: "other", Project: "p", WorktreePath: "/tmp/other"})
	e.fake.setMeta(childTask, MetaKeyRole, "freelance") // not "worker"

	e.handler.HandleEvent(ctx, linkCreatedEvent(parentTask, childTask, 100))

	orch, _ := e.db.Orchestrators.GetByName(ctx, "foo")
	_, err := e.db.Roles.GetByOrchestratorAndName(ctx, orch.ID, "other")
	if err == nil {
		t.Fatalf("expected role NOT to be created when meta:hera.role != 'worker'")
	}
}

func TestAdopt_ParentNotCoordinator_NotAdopted(t *testing.T) {
	ctx := context.Background()
	e := setupAdopt(t)

	ctxBackground := context.Background()
	// Set up a worker role that is bound to parentTask (not a coordinator).
	orch, _ := e.db.Orchestrators.Create(ctxBackground, "foo")
	worker, _ := e.db.Roles.Create(ctxBackground, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w", Kind: db.KindWorker, ArgusProject: "p",
	})
	_, _ = e.db.Bindings.Create(ctxBackground, db.CreateBindingInput{
		RoleID: worker.ID, ArgusTaskID: "task-worker", WorktreePath: "/tmp/w",
	})
	childTask := "task-new"
	e.fake.addTask(argus.Task{ID: childTask, Name: "new", Project: "p", WorktreePath: "/tmp/new"})
	e.fake.setMeta(childTask, MetaKeyRole, string(db.KindWorker))

	e.handler.HandleEvent(ctx, linkCreatedEvent("task-worker", childTask, 100))

	_, err := e.db.Roles.GetByOrchestratorAndName(ctx, orch.ID, "new")
	if err == nil {
		t.Fatalf("expected role NOT to be adopted when parent is not a coordinator")
	}
}

func TestAdopt_ParentNotBound_NotAdopted(t *testing.T) {
	ctx := context.Background()
	e := setupAdopt(t)
	parentTask := "task-unrelated"
	childTask := "task-child"
	// Note: no fixture; no bindings exist for parentTask.
	e.fake.addTask(argus.Task{ID: childTask, Name: "child", Project: "p", WorktreePath: "/tmp/c"})
	e.fake.setMeta(childTask, MetaKeyRole, string(db.KindWorker))

	e.handler.HandleEvent(ctx, linkCreatedEvent(parentTask, childTask, 100))

	orchs, _ := e.db.Orchestrators.List(ctx)
	if len(orchs) != 0 {
		t.Fatalf("expected no orchestrators to be created, got %d", len(orchs))
	}
}

func TestAdopt_TaskArchived_BindingEnds(t *testing.T) {
	ctx := context.Background()
	e := setupAdopt(t)
	fixtureCoordinator(t, e, "task-coord")

	// Add a worker binding to end.
	orch, _ := e.db.Orchestrators.GetByName(ctx, "foo")
	role, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w1", Kind: db.KindWorker, ArgusProject: "p",
	})
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: role.ID, ArgusTaskID: "task-w1", WorktreePath: "/tmp/w1",
	})

	e.handler.HandleEvent(ctx, argus.Event{ID: 200, Type: TypeTaskArchived, TaskID: "task-w1"})

	// Binding should now be ended.
	_, err := e.db.Bindings.GetLiveByTaskID(ctx, "task-w1")
	if err == nil {
		t.Fatalf("expected live binding for task-w1 to be ended after task.archived")
	}
}

// silence unused vars in case io is removed from imports during edits.
var _ = io.Discard
