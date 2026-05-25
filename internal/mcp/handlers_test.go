package mcp

import (
	"context"
	"encoding/json"
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
		_ = json.NewEncoder(w).Encode(struct {
			Tasks []argus.Task `json:"tasks"`
		}{f.tasks})
	})
	mux.HandleFunc("/api/tasks/", func(w http.ResponseWriter, r *http.Request) {
		// support /api/tasks/{id}/meta PUT
		rest := strings.TrimPrefix(r.URL.Path, "/api/tasks/")
		segs := strings.SplitN(rest, "/", 2)
		taskID := segs[0]
		sub := ""
		if len(segs) == 2 {
			sub = segs[1]
		}
		if sub == "meta" && r.Method == "PUT" {
			var body argus.PutTaskMetaInput
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.mu.Lock()
			f.metaPuts = append(f.metaPuts, struct{ taskID, key, value string }{taskID, body.Key, body.Value})
			f.mu.Unlock()
			w.WriteHeader(http.StatusOK)
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
	t.Cleanup(func() { database.Close() })

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
		Kind: "freelance", Mission: "extract X", Constraints: "do not change keybindings",
		Status: "working",
	}))
	out := decodeJoinOutput(t, resp)
	if out.RoleName != "refactor-x" {
		t.Fatalf("out = %+v", out)
	}
	if out.Mission != "extract X" {
		t.Fatalf("mission = %q", out.Mission)
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
		ArgusProject: "p", Mission: "build it",
	})
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: role.ID, ArgusTaskID: "t-1", WorktreePath: "/tmp/coord",
	})
	e.fake.addTask(argus.Task{ID: "t-1", Name: "coord", Project: "p", WorktreePath: "/tmp/coord"})

	h := NewJoinHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, JoinInput{Cwd: "/tmp/coord"}))
	out := decodeJoinOutput(t, resp)
	if out.RoleName != "coord" || out.Mission != "build it" {
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

	h := NewInboxHandler(e.resolver, e.db)
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
	h := NewMarkReadHandler(e.resolver, e.db)
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
	h := NewMarkReadHandler(e.resolver, e.db)
	resp := h.Handle(ctx, mustMarshal(t, MarkReadInput{Cwd: "/x"}))
	if !resp.IsError {
		t.Fatalf("expected error")
	}
}
