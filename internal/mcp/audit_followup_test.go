package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anutron/hera/internal/argus"
	"github.com/anutron/hera/internal/db"
)

// failingMetaArgus is a fake argus that returns 500 on PUT /api/tasks/{id}/meta
// but otherwise behaves like the regular handlers fixture. Used to exercise
// the soft-fail meta-mirror paths in hera_new_orchestrator and hera_status.
type failingMetaArgus struct {
	mu        *fakeArgusForHandlers
	srv       *httptest.Server
	client    *argus.Client
}

func setupFailingMetaHandlers(t *testing.T) *handlerFixture {
	t.Helper()
	fake := &fakeArgusForHandlers{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/tasks", func(w http.ResponseWriter, r *http.Request) {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		_ = json.NewEncoder(w).Encode(struct {
			Tasks []argus.Task `json:"tasks"`
		}{fake.tasks})
	})
	mux.HandleFunc("/api/tasks/", func(w http.ResponseWriter, r *http.Request) {
		// Always fail the meta PUT to exercise the soft-fail branches.
		if strings.HasSuffix(r.URL.Path, "/meta") && r.Method == "PUT" {
			http.Error(w, `{"error":"argus is sad"}`, http.StatusInternalServerError)
			return
		}
		http.NotFound(w, r)
	})
	srv := httptest.NewServer(mux)
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

func TestNewOrchestrator_MetaMirrorFailure_StillSucceeds(t *testing.T) {
	// Spec: role meta write on binding is best-effort; failure must not
	// undo the binding row.
	ctx := context.Background()
	e := setupFailingMetaHandlers(t)
	e.fake.addTask(argus.Task{ID: "t-coord", Project: "p", WorktreePath: "/tmp/coord"})

	h := NewNewOrchestratorHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, NewOrchestratorInput{
		Cwd: "/tmp/coord", Name: "foo", CoordinatorRoleName: "coord",
	}))
	if resp.IsError {
		t.Fatalf("expected success despite meta failure, got error: %s", resp.Content[0].Text)
	}
	// Binding must exist in the DB.
	if _, err := e.db.Bindings.GetLiveByTaskID(ctx, "t-coord"); err != nil {
		t.Fatalf("binding should exist after soft-failed meta mirror: %v", err)
	}
}

func TestStatus_MetaMirrorFailure_ReturnsMetaMirroredFalse(t *testing.T) {
	// Spec: hera_status meta-mirror is best-effort; failure surfaces
	// as meta_mirrored=false but the call still returns success and
	// the local role_status row is updated.
	ctx := context.Background()
	e := setupFailingMetaHandlers(t)
	orch, _ := e.db.Orchestrators.Create(ctx, "foo")
	role, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w", Kind: db.KindWorker, ArgusProject: "p",
	})
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: role.ID, ArgusTaskID: "t-w", WorktreePath: "/tmp/w",
	})
	e.fake.addTask(argus.Task{ID: "t-w", Project: "p", WorktreePath: "/tmp/w"})

	h := NewStatusHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, StatusInput{Cwd: "/tmp/w", Status: "working"}))
	if resp.IsError {
		t.Fatalf("expected success despite meta failure, got error: %s", resp.Content[0].Text)
	}

	var out StatusOutput
	if err := json.Unmarshal([]byte(resp.Content[0].Text), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.MetaMirrored {
		t.Fatalf("expected meta_mirrored=false on argus meta failure, got true")
	}
	// Local status MUST still be updated.
	rs, err := e.db.RoleStatus.Get(ctx, role.ID)
	if err != nil {
		t.Fatalf("RoleStatus.Get: %v", err)
	}
	if rs.Status != db.StatusWorking {
		t.Fatalf("local role_status not updated: %s", rs.Status)
	}
}

func TestResolver_TaskForCwd_NormalizesTrailingSlash(t *testing.T) {
	// filepath.Clean normalization: trailing slash should match an argus
	// WorktreePath that has no trailing slash.
	ctx := context.Background()
	e := setupHandlers(t)
	e.fake.addTask(argus.Task{ID: "t-1", Project: "p", WorktreePath: "/tmp/wt"})

	if _, err := e.resolver.TaskForCwd(ctx, "/tmp/wt/"); err != nil {
		t.Fatalf("trailing-slash cwd should match: %v", err)
	}
	if _, err := e.resolver.TaskForCwd(ctx, "/tmp/wt"); err != nil {
		t.Fatalf("exact cwd should match: %v", err)
	}
	if _, err := e.resolver.TaskForCwd(ctx, "/tmp/other"); !errors.Is(err, ErrCwdUnknown) {
		t.Fatalf("non-matching cwd should return ErrCwdUnknown, got %v", err)
	}
}
