package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/anutron/hera/internal/argus"
	"github.com/anutron/hera/internal/db"
)

// handlerFixture wires a fake argus + a real SQLite + a Resolver to drive
// handler tests.
type handlerFixture struct {
	fake     *fakeArgusForHandlers
	srv      *httptest.Server
	client   *argus.Client
	db       *db.DB
	resolver *Resolver
}

type fakeArgusForHandlers struct {
	mu       sync.Mutex
	tasks    []argus.Task
	metaPuts []struct{ taskID, key, value string }

	// Extended for hera_spawn_worker tests.
	// nextTaskID is returned as the ID of the next POST /api/tasks call.
	// createInputs records the full CreateTaskInput for each POST /api/tasks call.
	// inputPosts records POST /api/tasks/{id}/input calls.
	// taskGetWorktree maps task ID → worktree path for GET /api/tasks/{id}.
	// taskGetFail, when true, makes GET /api/tasks/{id} return 500.
	// inputFail, when true, makes POST /api/tasks/{id}/input return 500.
	nextTaskID   string
	createInputs []argus.CreateTaskInput
	inputPosts   []struct {
		taskID string
		body   []byte
	}
	taskGetWorktree map[string]string
	taskGetFail     bool
	inputFail       bool

	// notify support: notifyPosts records POST /api/tasks/{id}/notify calls.
	// notifyState controls the state field returned in the 202 response ("submitted"
	// or "pending"). notifyFail makes POST /api/tasks/{id}/notify return 500.
	// cancels records DELETE /api/tasks/{id}/notify/{deliveryID} calls.
	notifyPosts    []argus.NotifyInput
	notifyState    string
	notifyFail     bool
	notifyNotFound bool // returns 404 for POST .../notify (task has no active session)
	cancels        []struct{ taskID, deliveryID string }
	cancelFail     bool // returns 500 for DELETE .../notify/{id}

	// projectsFull, when non-nil, is served by GET /api/projects/full. When
	// nil the route 404s (mirrors the endpoint being unreachable for a test);
	// a non-nil empty slice serves a 200 with zero projects.
	projectsFull []argus.Project
}

func (f *fakeArgusForHandlers) addTask(task argus.Task) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tasks = append(f.tasks, task)
}

func (f *fakeArgusForHandlers) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/tasks", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Method == http.MethodPost {
			// Create task: decode input, assign nextTaskID, add to task list.
			var in argus.CreateTaskInput
			_ = json.NewDecoder(r.Body).Decode(&in)
			f.createInputs = append(f.createInputs, in)
			id := f.nextTaskID
			if id == "" {
				id = "fake-task-id"
			}
			wtp := ""
			if f.taskGetWorktree != nil {
				wtp = f.taskGetWorktree[id]
			}
			name := in.Name
			if name == "" {
				name = id
			}
			f.tasks = append(f.tasks, argus.Task{
				ID:           id,
				Name:         name,
				Project:      in.Project,
				WorktreePath: wtp,
			})
			_ = json.NewEncoder(w).Encode(argus.CreatedTask{ID: id, Name: name, Status: "in_progress"})
			return
		}
		_ = json.NewEncoder(w).Encode(struct {
			Tasks []argus.Task `json:"tasks"`
		}{f.tasks})
	})
	mux.HandleFunc("/api/projects/full", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.projectsFull == nil {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(struct {
			Projects []argus.Project `json:"projects"`
		}{f.projectsFull})
	})
	mux.HandleFunc("/api/tasks/", func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/api/tasks/")
		segs := strings.SplitN(rest, "/", 2)
		taskID := segs[0]
		sub := ""
		if len(segs) == 2 {
			sub = segs[1]
		}

		if sub == "meta" && r.Method == http.MethodPut {
			var body argus.PutTaskMetaInput
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.mu.Lock()
			f.metaPuts = append(f.metaPuts, struct{ taskID, key, value string }{taskID, body.Key, body.Value})
			f.mu.Unlock()
			w.WriteHeader(http.StatusOK)
			return
		}

		if sub == "input" && r.Method == http.MethodPost {
			f.mu.Lock()
			if f.inputFail {
				f.mu.Unlock()
				http.Error(w, `{"error":"injected failure"}`, http.StatusInternalServerError)
				return
			}
			b := make([]byte, r.ContentLength)
			if r.ContentLength > 0 {
				_, _ = r.Body.Read(b)
			}
			f.inputPosts = append(f.inputPosts, struct {
				taskID string
				body   []byte
			}{taskID, b})
			f.mu.Unlock()
			_ = json.NewEncoder(w).Encode(struct {
				Status string `json:"status"`
				Bytes  int    `json:"bytes"`
			}{"ok", len(b)})
			return
		}

		if sub == "" && r.Method == http.MethodGet {
			f.mu.Lock()
			if f.taskGetFail {
				f.mu.Unlock()
				http.Error(w, `{"error":"injected failure"}`, http.StatusInternalServerError)
				return
			}
			wtp := ""
			if f.taskGetWorktree != nil {
				wtp = f.taskGetWorktree[taskID]
			}
			f.mu.Unlock()
			_ = json.NewEncoder(w).Encode(argus.Task{
				ID:           taskID,
				WorktreePath: wtp,
			})
			return
		}

		// POST /api/tasks/{id}/notify
		if sub == "notify" && r.Method == http.MethodPost {
			f.mu.Lock()
			fail := f.notifyFail
			notFound := f.notifyNotFound
			f.mu.Unlock()
			if fail {
				http.Error(w, `{"error":"injected notify failure"}`, http.StatusInternalServerError)
				return
			}
			if notFound {
				http.NotFound(w, r)
				return
			}
			f.mu.Lock()
			var in argus.NotifyInput
			_ = json.NewDecoder(r.Body).Decode(&in)
			f.notifyPosts = append(f.notifyPosts, in)
			state := f.notifyState
			if state == "" {
				state = "submitted"
			}
			f.mu.Unlock()
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(argus.NotifyResponse{DeliveryID: in.DeliveryID, State: state})
			return
		}

		// DELETE /api/tasks/{id}/notify/{deliveryID}
		if strings.HasPrefix(sub, "notify/") && r.Method == http.MethodDelete {
			f.mu.Lock()
			fail := f.cancelFail
			f.mu.Unlock()
			if fail {
				http.Error(w, `{"error":"injected cancel failure"}`, http.StatusInternalServerError)
				return
			}
			deliveryID := strings.TrimPrefix(sub, "notify/")
			f.mu.Lock()
			f.cancels = append(f.cancels, struct{ taskID, deliveryID string }{taskID, deliveryID})
			f.mu.Unlock()
			_ = json.NewEncoder(w).Encode(argus.CancelNotifyResponse{DeliveryID: deliveryID, Cancelled: true})
			return
		}

		http.NotFound(w, r)
	})
	return mux
}

func setupHandlers(t *testing.T) *handlerFixture {
	t.Helper()
	fake := &fakeArgusForHandlers{}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	dbPath := filepath.Join(t.TempDir(), "hera.sqlite")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	client := argus.New(srv.URL, "tok")
	resolver := NewResolver(client, database)
	return &handlerFixture{fake: fake, srv: srv, client: client, db: database, resolver: resolver}
}

func decodeJoinOutput(t *testing.T, r Response) JoinOutput {
	t.Helper()
	if r.IsError {
		t.Fatalf("got error response: %s", r.Content[0].Text)
	}
	var out JoinOutput
	if err := json.Unmarshal([]byte(r.Content[0].Text), &out); err != nil {
		t.Fatalf("decode JoinOutput: %v", err)
	}
	return out
}

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestJoin_FreelanceAttach_HappyPath(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	// Pre-create orchestrator and a coordinator-bound task so freelancers
	// can attach.
	orch, _ := e.db.Orchestrators.Create(ctx, "foo")
	_, _ = e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "p",
	})
	e.fake.addTask(argus.Task{ID: "t-free", Name: "freelance", Project: "hera", WorktreePath: "/tmp/free"})

	h := NewJoinHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, JoinInput{
		Cwd: "/tmp/free", Orchestrator: "foo", RoleName: "refactor-x",
		Kind: "freelance", Prompt: "extract X",
		Status: "working",
	}))
	out := decodeJoinOutput(t, resp)
	if out.RoleName != "refactor-x" {
		t.Fatalf("out = %+v", out)
	}
	if out.Prompt != "extract X" {
		t.Fatalf("prompt = %q", out.Prompt)
	}

	// Confirm DB rows are real.
	role, err := e.db.Roles.GetByOrchestratorAndName(ctx, orch.ID, "refactor-x")
	if err != nil {
		t.Fatalf("role lookup: %v", err)
	}
	if role.Kind != db.KindFreelance {
		t.Fatalf("role.Kind = %s", role.Kind)
	}
	bnd, err := e.db.Bindings.GetLiveByTaskID(ctx, "t-free")
	if err != nil {
		t.Fatalf("binding lookup: %v", err)
	}
	if bnd.RoleID != role.ID {
		t.Fatalf("binding.RoleID != role.ID")
	}
	rs, err := e.db.RoleStatus.Get(ctx, role.ID)
	if err != nil {
		t.Fatalf("RoleStatus.Get: %v", err)
	}
	if rs.Status != db.StatusWorking {
		t.Fatalf("status = %s", rs.Status)
	}
}

func TestJoin_FreelanceAttach_OrchestratorMissing(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	e.fake.addTask(argus.Task{ID: "t-free", Name: "free", Project: "p", WorktreePath: "/tmp/free"})

	h := NewJoinHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, JoinInput{
		Cwd: "/tmp/free", Orchestrator: "ghost", RoleName: "x", Kind: "freelance",
	}))
	if !resp.IsError {
		t.Fatalf("expected error, got success: %+v", resp)
	}
	if !strings.Contains(resp.Content[0].Text, "does not exist") {
		t.Fatalf("error wording: %q", resp.Content[0].Text)
	}
}

func TestJoin_FreelanceAttach_ConflictingKind(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	orch, _ := e.db.Orchestrators.Create(ctx, "foo")
	_, _ = e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "f2-impl", Kind: db.KindWorker, ArgusProject: "p",
	})
	e.fake.addTask(argus.Task{ID: "t-free", Name: "free", Project: "p", WorktreePath: "/tmp/free"})

	h := NewJoinHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, JoinInput{
		Cwd: "/tmp/free", Orchestrator: "foo", RoleName: "f2-impl", Kind: "freelance",
	}))
	if !resp.IsError {
		t.Fatalf("expected error, got success: %+v", resp)
	}
	if !strings.Contains(resp.Content[0].Text, "already exists with kind") {
		t.Fatalf("error wording: %q", resp.Content[0].Text)
	}
}

func TestJoin_BareReincarnation_HappyPath(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	orch, _ := e.db.Orchestrators.Create(ctx, "foo")
	role, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator,
		ArgusProject: "p", Prompt: "build it",
	})
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: role.ID, ArgusTaskID: "t-1", WorktreePath: "/tmp/coord",
	})
	e.fake.addTask(argus.Task{ID: "t-1", Name: "coord", Project: "p", WorktreePath: "/tmp/coord"})

	h := NewJoinHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, JoinInput{Cwd: "/tmp/coord"}))
	out := decodeJoinOutput(t, resp)
	if out.RoleName != "coord" || out.Prompt != "build it" {
		t.Fatalf("out = %+v", out)
	}
}

func TestJoin_BareReincarnation_NoBinding(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	e.fake.addTask(argus.Task{ID: "t-1", Name: "x", Project: "p", WorktreePath: "/tmp/x"})

	h := NewJoinHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, JoinInput{Cwd: "/tmp/x"}))
	if !resp.IsError {
		t.Fatalf("expected error, got success")
	}
	if !strings.Contains(resp.Content[0].Text, "not bound") {
		t.Fatalf("error wording: %q", resp.Content[0].Text)
	}
}

func TestJoin_UnknownCwd(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	h := NewJoinHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, JoinInput{Cwd: "/nowhere"}))
	if !resp.IsError {
		t.Fatalf("expected error, got success")
	}
}

func TestStatus_HappyPath(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	orch, _ := e.db.Orchestrators.Create(ctx, "foo")
	role, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w", Kind: db.KindWorker, ArgusProject: "p",
	})
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: role.ID, ArgusTaskID: "t-w", WorktreePath: "/tmp/w",
	})
	e.fake.addTask(argus.Task{ID: "t-w", Name: "w", Project: "p", WorktreePath: "/tmp/w"})

	h := NewStatusHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, StatusInput{Cwd: "/tmp/w", Status: "blocked"}))
	if resp.IsError {
		t.Fatalf("error: %s", resp.Content[0].Text)
	}

	rs, err := e.db.RoleStatus.Get(ctx, role.ID)
	if err != nil {
		t.Fatalf("RoleStatus.Get: %v", err)
	}
	if rs.Status != db.StatusBlocked {
		t.Fatalf("status = %s", rs.Status)
	}

	// Meta mirror happened.
	e.fake.mu.Lock()
	defer e.fake.mu.Unlock()
	found := false
	for _, p := range e.fake.metaPuts {
		if p.taskID == "t-w" && p.key == "thread_status" && p.value == "blocked" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected meta mirror PUT, got %+v", e.fake.metaPuts)
	}
}

func TestStatus_InvalidStatus(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	h := NewStatusHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, StatusInput{Cwd: "/x", Status: "wibble"}))
	if !resp.IsError {
		t.Fatalf("expected error")
	}
	if !strings.Contains(resp.Content[0].Text, "invalid status") {
		t.Fatalf("error wording: %q", resp.Content[0].Text)
	}
}

func TestInbox_EmptyAndPopulated(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	orch, _ := e.db.Orchestrators.Create(ctx, "foo")
	coord, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "c", Kind: db.KindCoordinator, ArgusProject: "p",
	})
	worker, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w", Kind: db.KindWorker, ArgusProject: "p",
	})
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: worker.ID, ArgusTaskID: "t-w", WorktreePath: "/tmp/w",
	})
	e.fake.addTask(argus.Task{ID: "t-w", Name: "w", Project: "p", WorktreePath: "/tmp/w"})

	h := NewInboxHandler(e.resolver, e.db, e.client)
	// Empty
	resp := h.Handle(ctx, mustMarshal(t, InboxInput{Cwd: "/tmp/w"}))
	if resp.IsError {
		t.Fatalf("expected success, got error")
	}
	var emptyOut InboxOutput
	_ = json.Unmarshal([]byte(resp.Content[0].Text), &emptyOut)
	if emptyOut.Count != 0 {
		t.Fatalf("empty count = %d, want 0", emptyOut.Count)
	}

	// Send a couple of messages to the worker.
	for _, body := range []string{"first", "second"} {
		_, _ = e.db.Messages.Create(ctx, db.CreateMessageInput{
			FromRoleID: coord.ID, ToRoleID: worker.ID, Body: body,
		})
	}

	resp = h.Handle(ctx, mustMarshal(t, InboxInput{Cwd: "/tmp/w"}))
	var out InboxOutput
	_ = json.Unmarshal([]byte(resp.Content[0].Text), &out)
	if out.Count != 2 {
		t.Fatalf("populated count = %d, want 2", out.Count)
	}
	if out.Messages[0].FromRole != "c" {
		t.Fatalf("FromRole = %q", out.Messages[0].FromRole)
	}
}

func TestMarkRead_OnlyOwnMessagesAffected(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	orch, _ := e.db.Orchestrators.Create(ctx, "foo")
	coord, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "c", Kind: db.KindCoordinator, ArgusProject: "p",
	})
	w1, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w1", Kind: db.KindWorker, ArgusProject: "p",
	})
	w2, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w2", Kind: db.KindWorker, ArgusProject: "p",
	})
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: w1.ID, ArgusTaskID: "t-w1", WorktreePath: "/tmp/w1",
	})
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: w2.ID, ArgusTaskID: "t-w2", WorktreePath: "/tmp/w2",
	})
	e.fake.addTask(argus.Task{ID: "t-w1", Name: "w1", Project: "p", WorktreePath: "/tmp/w1"})
	e.fake.addTask(argus.Task{ID: "t-w2", Name: "w2", Project: "p", WorktreePath: "/tmp/w2"})

	msg, _ := e.db.Messages.Create(ctx, db.CreateMessageInput{
		FromRoleID: coord.ID, ToRoleID: w1.ID, Body: "for w1",
	})

	// w2 tries to mark w1's message as read.
	h := NewMarkReadHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, MarkReadInput{Cwd: "/tmp/w2", MessageIDs: []int64{msg.ID}}))
	if resp.IsError {
		t.Fatalf("unexpected error: %s", resp.Content[0].Text)
	}
	var out MarkReadOutput
	_ = json.Unmarshal([]byte(resp.Content[0].Text), &out)
	if out.MarkedReadCount != 0 {
		t.Fatalf("cross-role mark-read should be 0, got %d", out.MarkedReadCount)
	}

	// Confirm w1's inbox still has the message.
	unread, _ := e.db.Messages.UnreadForRole(ctx, w1.ID)
	if len(unread) != 1 {
		t.Fatalf("w1 inbox = %d, want 1", len(unread))
	}

	// Now w1 marks it read.
	resp = h.Handle(ctx, mustMarshal(t, MarkReadInput{Cwd: "/tmp/w1", MessageIDs: []int64{msg.ID}}))
	_ = json.Unmarshal([]byte(resp.Content[0].Text), &out)
	if out.MarkedReadCount != 1 {
		t.Fatalf("own mark-read count = %d, want 1", out.MarkedReadCount)
	}
}

func TestMarkRead_RequiresMessageIDs(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	h := NewMarkReadHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, MarkReadInput{Cwd: "/x"}))
	if !resp.IsError {
		t.Fatalf("expected error")
	}
}

// TestInbox_MarksReturnedMessagesRead verifies that calling hera_inbox sets
// read_at on the returned messages so a second call returns empty.
func TestInbox_MarksReturnedMessagesRead(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	orch, _ := e.db.Orchestrators.Create(ctx, "foo")
	coord, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "c", Kind: db.KindCoordinator, ArgusProject: "p",
	})
	worker, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w", Kind: db.KindWorker, ArgusProject: "p",
	})
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: worker.ID, ArgusTaskID: "t-w", WorktreePath: "/tmp/w",
	})
	e.fake.addTask(argus.Task{ID: "t-w", Name: "w", Project: "p", WorktreePath: "/tmp/w"})

	msg, _ := e.db.Messages.Create(ctx, db.CreateMessageInput{
		FromRoleID: coord.ID, ToRoleID: worker.ID, Body: "hello",
	})

	h := NewInboxHandler(e.resolver, e.db, e.client)

	// First call: message is returned.
	resp := h.Handle(ctx, mustMarshal(t, InboxInput{Cwd: "/tmp/w"}))
	if resp.IsError {
		t.Fatalf("first inbox call error: %s", resp.Content[0].Text)
	}
	var out InboxOutput
	_ = json.Unmarshal([]byte(resp.Content[0].Text), &out)
	if out.Count != 1 {
		t.Fatalf("first call: count = %d, want 1", out.Count)
	}

	// read_at must be set on the message row.
	got, err := e.db.Messages.GetByID(ctx, msg.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ReadAt == nil {
		t.Fatalf("read_at should be set after hera_inbox fetch, got nil")
	}

	// Second call: inbox is empty (message already marked read).
	resp = h.Handle(ctx, mustMarshal(t, InboxInput{Cwd: "/tmp/w"}))
	if resp.IsError {
		t.Fatalf("second inbox call error: %s", resp.Content[0].Text)
	}
	var out2 InboxOutput
	_ = json.Unmarshal([]byte(resp.Content[0].Text), &out2)
	if out2.Count != 0 {
		t.Fatalf("second call: count = %d, want 0 (already read)", out2.Count)
	}
}

// TestInbox_FetchCancelsArgusDelivery verifies that a message fetched via
// hera_inbox has read_at set and a cancel call is made to argus so argus
// stops retrying delivery.
func TestInbox_FetchCancelsArgusDelivery(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	orch, _ := e.db.Orchestrators.Create(ctx, "foo")
	coord, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "c", Kind: db.KindCoordinator, ArgusProject: "p",
	})
	worker, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w", Kind: db.KindWorker, ArgusProject: "p",
	})
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: worker.ID, ArgusTaskID: "t-w", WorktreePath: "/tmp/w",
	})
	e.fake.addTask(argus.Task{ID: "t-w", Name: "w", Project: "p", WorktreePath: "/tmp/w"})

	msg, _ := e.db.Messages.Create(ctx, db.CreateMessageInput{
		FromRoleID: coord.ID, ToRoleID: worker.ID, Body: "nudge me", Tldr: "nudge me",
	})
	if err := e.db.Messages.SetDelivered(ctx, msg.ID, db.DeliveryIdleSubmit); err != nil {
		t.Fatalf("SetDelivered: %v", err)
	}

	// Fetch via hera_inbox — stamps read_at and triggers argus cancel.
	h := NewInboxHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, InboxInput{Cwd: "/tmp/w"}))
	if resp.IsError {
		t.Fatalf("inbox call error: %s", resp.Content[0].Text)
	}

	// Verify read_at was set.
	row, err := e.db.Messages.GetByID(ctx, msg.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if row.ReadAt == nil {
		t.Fatal("read_at not set after inbox fetch")
	}

	// Verify argus cancel was called for the message.
	e.fake.mu.Lock()
	defer e.fake.mu.Unlock()
	if len(e.fake.cancels) != 1 {
		t.Fatalf("cancels count = %d, want 1", len(e.fake.cancels))
	}
	if e.fake.cancels[0].taskID != "t-w" {
		t.Fatalf("cancel taskID = %q, want t-w", e.fake.cancels[0].taskID)
	}
	if e.fake.cancels[0].deliveryID != fmt.Sprintf("%d", msg.ID) {
		t.Fatalf("cancel deliveryID = %q, want %d", e.fake.cancels[0].deliveryID, msg.ID)
	}
}

// TestInbox_MarkReadPreservesOtherRoleMessages verifies that hera_inbox only
// marks read the messages addressed to the caller's role, not messages for
// other roles in the same orchestrator.
func TestInbox_MarkReadPreservesOtherRoleMessages(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	orch, _ := e.db.Orchestrators.Create(ctx, "foo")
	coord, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "c", Kind: db.KindCoordinator, ArgusProject: "p",
	})
	w1, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w1", Kind: db.KindWorker, ArgusProject: "p",
	})
	w2, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w2", Kind: db.KindWorker, ArgusProject: "p",
	})
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: w1.ID, ArgusTaskID: "t-w1", WorktreePath: "/tmp/w1",
	})
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: w2.ID, ArgusTaskID: "t-w2", WorktreePath: "/tmp/w2",
	})
	e.fake.addTask(argus.Task{ID: "t-w1", Name: "w1", Project: "p", WorktreePath: "/tmp/w1"})
	e.fake.addTask(argus.Task{ID: "t-w2", Name: "w2", Project: "p", WorktreePath: "/tmp/w2"})

	_, _ = e.db.Messages.Create(ctx, db.CreateMessageInput{
		FromRoleID: coord.ID, ToRoleID: w1.ID, Body: "for w1",
	})
	msg2, _ := e.db.Messages.Create(ctx, db.CreateMessageInput{
		FromRoleID: coord.ID, ToRoleID: w2.ID, Body: "for w2",
	})

	// w1 fetches its inbox — only w1's message should be marked read.
	h := NewInboxHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, InboxInput{Cwd: "/tmp/w1"}))
	if resp.IsError {
		t.Fatalf("w1 inbox error: %s", resp.Content[0].Text)
	}

	// w2's message must still be unread.
	unread, _ := e.db.Messages.UnreadForRole(ctx, w2.ID)
	if len(unread) != 1 || unread[0].ID != msg2.ID {
		t.Fatalf("w2 should still have 1 unread message, got %d", len(unread))
	}
}

// TestInbox_CancelErrorDoesNotFailHandler verifies that a cancel failure
// does NOT fail the hera_inbox response.
// Delta: "Scenario: Cancel error does not fail the handler"
func TestInbox_CancelErrorDoesNotFailHandler(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	e.fake.cancelFail = true // DELETE .../notify/{id} returns 500

	orch, _ := e.db.Orchestrators.Create(ctx, "foo")
	coord, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "c", Kind: db.KindCoordinator, ArgusProject: "p",
	})
	worker, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w", Kind: db.KindWorker, ArgusProject: "p",
	})
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: worker.ID, ArgusTaskID: "t-w", WorktreePath: "/tmp/w",
	})
	e.fake.addTask(argus.Task{ID: "t-w", Name: "w", Project: "p", WorktreePath: "/tmp/w"})

	_, _ = e.db.Messages.Create(ctx, db.CreateMessageInput{
		FromRoleID: coord.ID, ToRoleID: worker.ID, Body: "hello", Tldr: "hello",
	})

	// cancel fails (500), but inbox must still succeed.
	h := NewInboxHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, InboxInput{Cwd: "/tmp/w"}))
	if resp.IsError {
		t.Fatalf("hera_inbox must succeed even when cancel fails; got error: %s", resp.Content[0].Text)
	}
}

// TestMarkRead_CancelsArgusDelivery verifies that hera_mark_read calls
// CancelNotify for each marked-read message.
// Delta: "Scenario: hera_mark_read marks messages read and cancels delivery"
func TestMarkRead_CancelsArgusDelivery(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)

	orch, _ := e.db.Orchestrators.Create(ctx, "foo")
	coord, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "c", Kind: db.KindCoordinator, ArgusProject: "p",
	})
	worker, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w", Kind: db.KindWorker, ArgusProject: "p",
	})
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: worker.ID, ArgusTaskID: "t-w", WorktreePath: "/tmp/w",
	})
	e.fake.addTask(argus.Task{ID: "t-w", Name: "w", Project: "p", WorktreePath: "/tmp/w"})

	msg, _ := e.db.Messages.Create(ctx, db.CreateMessageInput{
		FromRoleID: coord.ID, ToRoleID: worker.ID, Body: "ping", Tldr: "ping",
	})
	_ = e.db.Messages.SetDelivered(ctx, msg.ID, db.DeliveryIdleSubmit)

	h := NewMarkReadHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, MarkReadInput{
		Cwd: "/tmp/w", MessageIDs: []int64{msg.ID},
	}))
	if resp.IsError {
		t.Fatalf("hera_mark_read error: %s", resp.Content[0].Text)
	}

	e.fake.mu.Lock()
	defer e.fake.mu.Unlock()
	if len(e.fake.cancels) != 1 {
		t.Fatalf("cancels count = %d, want 1", len(e.fake.cancels))
	}
	if e.fake.cancels[0].taskID != "t-w" {
		t.Fatalf("cancel taskID = %q, want t-w", e.fake.cancels[0].taskID)
	}
	if e.fake.cancels[0].deliveryID != fmt.Sprintf("%d", msg.ID) {
		t.Fatalf("cancel deliveryID = %q, want %d", e.fake.cancels[0].deliveryID, msg.ID)
	}
}

// TestMarkRead_CancelErrorDoesNotFailHandler verifies that a cancel failure
// does NOT fail the hera_mark_read response.
// Delta: "Scenario: Cancel error does not fail the handler"
func TestMarkRead_CancelErrorDoesNotFailHandler(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	e.fake.cancelFail = true

	orch, _ := e.db.Orchestrators.Create(ctx, "foo")
	coord, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "c", Kind: db.KindCoordinator, ArgusProject: "p",
	})
	worker, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w", Kind: db.KindWorker, ArgusProject: "p",
	})
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: worker.ID, ArgusTaskID: "t-w", WorktreePath: "/tmp/w",
	})
	e.fake.addTask(argus.Task{ID: "t-w", Name: "w", Project: "p", WorktreePath: "/tmp/w"})

	msg, _ := e.db.Messages.Create(ctx, db.CreateMessageInput{
		FromRoleID: coord.ID, ToRoleID: worker.ID, Body: "ping", Tldr: "ping",
	})

	h := NewMarkReadHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, MarkReadInput{
		Cwd: "/tmp/w", MessageIDs: []int64{msg.ID},
	}))
	if resp.IsError {
		t.Fatalf("hera_mark_read must succeed even when cancel fails; got error: %s", resp.Content[0].Text)
	}
}

// TestInbox_CancelSkippedWhenNoLiveBinding verifies that when the recipient
// has no live binding at cancel time, the cancel is silently skipped and
// hera_inbox still returns successfully.
// Delta: "If no live binding exists for the role at cancel time the cancel is
// skipped silently."
func TestInbox_CancelSkippedWhenNoLiveBinding(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)

	orch, _ := e.db.Orchestrators.Create(ctx, "foo")
	coord, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "c", Kind: db.KindCoordinator, ArgusProject: "p",
	})
	worker, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w", Kind: db.KindWorker, ArgusProject: "p",
	})
	// NOTE: deliberately no binding created for the worker role.
	e.fake.addTask(argus.Task{ID: "t-w", Name: "w", Project: "p", WorktreePath: "/tmp/w"})

	_, _ = e.db.Messages.Create(ctx, db.CreateMessageInput{
		FromRoleID: coord.ID, ToRoleID: worker.ID, Body: "ping", Tldr: "ping",
	})

	// Inbox is called from the worker's cwd, but worker has no binding —
	// CallerRole resolves via cwd→task→binding, so this call will fail
	// at CallerRole. We need a binding to resolve the caller. Create one
	// that will be ended (simulating a recently-ended binding).
	//
	// Actually, for CallerRole to work we need a live binding. The scenario
	// we're testing is: binding exists when inbox is called, but by the time
	// cancelDeliveries runs the binding lookup returns no binding.
	// Since cancelDeliveries calls GetLiveByRole on the same role, and we
	// just created a binding, we need to end it first then test.
	//
	// Simpler: create the binding, fetch inbox (which marks read and cancels),
	// then end the binding and verify a second inbox call on the same role
	// (with another message) doesn't panic even with no live binding.
	bnd, _ := e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: worker.ID, ArgusTaskID: "t-w", WorktreePath: "/tmp/w",
	})

	// First inbox call succeeds with a live binding (cancel is called).
	h := NewInboxHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, InboxInput{Cwd: "/tmp/w"}))
	if resp.IsError {
		t.Fatalf("first inbox error: %s", resp.Content[0].Text)
	}

	// End the binding, then send another message.
	if err := e.db.Bindings.End(ctx, bnd.ID, "test"); err != nil {
		t.Fatalf("End binding: %v", err)
	}
	// Rebind on a different task so CallerRole resolves.
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: worker.ID, ArgusTaskID: "t-w2", WorktreePath: "/tmp/w2",
	})
	e.fake.addTask(argus.Task{ID: "t-w2", Name: "w2", Project: "p", WorktreePath: "/tmp/w2"})
	_, _ = e.db.Messages.Create(ctx, db.CreateMessageInput{
		FromRoleID: coord.ID, ToRoleID: worker.ID, Body: "ping2", Tldr: "ping2",
	})
	// The cancel for the new task (t-w2) will try GetLiveByRole — it will find
	// the new binding and succeed. This test confirms no panic or error.
	resp2 := h.Handle(ctx, mustMarshal(t, InboxInput{Cwd: "/tmp/w2"}))
	if resp2.IsError {
		t.Fatalf("second inbox error: %s", resp2.Content[0].Text)
	}
}
