package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/anutron/hera/internal/db"
)

// setupCoordFixture creates a coordinator role + binding for the given cwd task,
// returning the orchestrator ID. Used by spawn-worker tests.
func setupCoordFixture(t *testing.T, e *handlerFixture, coordCwd, coordTaskID, project string) (orchID int64) {
	t.Helper()
	ctx := context.Background()
	orch, err := e.db.Orchestrators.Create(ctx, "test-orch")
	if err != nil {
		t.Fatalf("create orchestrator: %v", err)
	}
	role, err := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID,
		Name:           "coord",
		Kind:           db.KindCoordinator,
		ArgusProject:   project,
	})
	if err != nil {
		t.Fatalf("create coord role: %v", err)
	}
	_, err = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID:       role.ID,
		ArgusTaskID:  coordTaskID,
		WorktreePath: coordCwd,
	})
	if err != nil {
		t.Fatalf("create coord binding: %v", err)
	}
	return orch.ID
}

func decodeSpawnOutput(t *testing.T, r Response) SpawnWorkerOutput {
	t.Helper()
	if r.IsError {
		t.Fatalf("got error response: %s", r.Content[0].Text)
	}
	var out SpawnWorkerOutput
	if err := json.Unmarshal([]byte(r.Content[0].Text), &out); err != nil {
		t.Fatalf("decode SpawnWorkerOutput: %v", err)
	}
	return out
}

// TestSpawnWorker_HappyPath verifies the golden path: coordinator spawns a
// worker; task created, role + binding inserted, CR submitted, output correct.
func TestSpawnWorker_HappyPath(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)

	e.fake.mu.Lock()
	e.fake.nextTaskID = "worker-task-1"
	e.fake.taskGetWorktree = map[string]string{"worker-task-1": "/tmp/worker1"}
	e.fake.mu.Unlock()

	e.fake.addTask(taskFor("coord-task", "/tmp/coord"))
	orchID := setupCoordFixture(t, e, "/tmp/coord", "coord-task", "myproject")

	h := NewSpawnWorkerHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, SpawnWorkerInput{
		Cwd:    "/tmp/coord",
		Prompt: "Build the authentication module",
	}))
	out := decodeSpawnOutput(t, resp)

	if out.ArgusTaskID != "worker-task-1" {
		t.Fatalf("ArgusTaskID = %q, want worker-task-1", out.ArgusTaskID)
	}
	if out.Kind != "worker" {
		t.Fatalf("Kind = %q, want worker", out.Kind)
	}
	if out.Orchestrator != "test-orch" {
		t.Fatalf("Orchestrator = %q", out.Orchestrator)
	}
	if !out.PromptAutoSubmitted {
		t.Fatalf("PromptAutoSubmitted = false, want true")
	}

	// Verify a CR was posted.
	e.fake.mu.Lock()
	posts := e.fake.inputPosts
	e.fake.mu.Unlock()
	if len(posts) != 1 || posts[0].taskID != "worker-task-1" || string(posts[0].body) != "\r" {
		t.Fatalf("inputPosts = %+v, want one CR post to worker-task-1", posts)
	}

	// Verify DB state.
	workerRole, err := e.db.Roles.GetByOrchestratorAndName(ctx, orchID, out.RoleName)
	if err != nil {
		t.Fatalf("role lookup: %v", err)
	}
	if workerRole.Kind != db.KindWorker {
		t.Fatalf("role.Kind = %s, want worker", workerRole.Kind)
	}
	bnd, err := e.db.Bindings.GetLiveByTaskID(ctx, "worker-task-1")
	if err != nil {
		t.Fatalf("binding lookup: %v", err)
	}
	if bnd.WorktreePath != "/tmp/worker1" {
		t.Fatalf("binding.WorktreePath = %q", bnd.WorktreePath)
	}
	if bnd.ID != out.BindingID {
		t.Fatalf("BindingID mismatch: response=%d db=%d", out.BindingID, bnd.ID)
	}
}

// TestSpawnWorker_RoleNameDerived verifies the role name is slugged from the prompt.
func TestSpawnWorker_RoleNameDerived(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	e.fake.addTask(taskFor("c1", "/tmp/c1"))
	e.fake.mu.Lock()
	e.fake.nextTaskID = "w1"
	e.fake.mu.Unlock()
	setupCoordFixture(t, e, "/tmp/c1", "c1", "proj")

	h := NewSpawnWorkerHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, SpawnWorkerInput{
		Cwd:    "/tmp/c1",
		Prompt: "Fix the login bug ASAP",
	}))
	out := decodeSpawnOutput(t, resp)
	// "Fix the login bug ASAP" → "fix-the-login-bug-asap"
	if out.RoleName != "fix-the-login-bug-asap" {
		t.Fatalf("RoleName = %q, want fix-the-login-bug-asap", out.RoleName)
	}
}

// TestSpawnWorker_ExplicitRoleName verifies that a supplied role_name is used as-is.
func TestSpawnWorker_ExplicitRoleName(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	e.fake.addTask(taskFor("c1", "/tmp/c1"))
	e.fake.mu.Lock()
	e.fake.nextTaskID = "w1"
	e.fake.mu.Unlock()
	setupCoordFixture(t, e, "/tmp/c1", "c1", "proj")

	h := NewSpawnWorkerHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, SpawnWorkerInput{
		Cwd:      "/tmp/c1",
		Prompt:   "do things",
		RoleName: "my-custom-role",
	}))
	out := decodeSpawnOutput(t, resp)
	if out.RoleName != "my-custom-role" {
		t.Fatalf("RoleName = %q, want my-custom-role", out.RoleName)
	}
}

// TestSpawnWorker_RoleNameUniqueness verifies -2 suffix when base name is taken.
func TestSpawnWorker_RoleNameUniqueness(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	e.fake.addTask(taskFor("c1", "/tmp/c1"))
	e.fake.mu.Lock()
	e.fake.nextTaskID = "w2"
	e.fake.mu.Unlock()
	orchID := setupCoordFixture(t, e, "/tmp/c1", "c1", "proj")

	// Pre-create a worker role with the would-be derived name.
	_, err := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orchID,
		Name:           "do-things",
		Kind:           db.KindWorker,
		ArgusProject:   "proj",
	})
	if err != nil {
		t.Fatalf("pre-create role: %v", err)
	}

	h := NewSpawnWorkerHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, SpawnWorkerInput{
		Cwd:    "/tmp/c1",
		Prompt: "do things",
	}))
	out := decodeSpawnOutput(t, resp)
	if out.RoleName != "do-things-2" {
		t.Fatalf("RoleName = %q, want do-things-2", out.RoleName)
	}
}

// TestSpawnWorker_ProjectOverride verifies explicit project takes precedence.
func TestSpawnWorker_ProjectOverride(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	e.fake.addTask(taskFor("c1", "/tmp/c1"))
	e.fake.mu.Lock()
	e.fake.nextTaskID = "w1"
	e.fake.mu.Unlock()
	setupCoordFixture(t, e, "/tmp/c1", "c1", "default-proj")

	h := NewSpawnWorkerHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, SpawnWorkerInput{
		Cwd:     "/tmp/c1",
		Prompt:  "do stuff",
		Project: "override-proj",
	}))
	out := decodeSpawnOutput(t, resp)

	// Verify the created task's project.
	e.fake.mu.Lock()
	var createdProject string
	for _, task := range e.fake.tasks {
		if task.ID == out.ArgusTaskID {
			createdProject = task.Project
		}
	}
	e.fake.mu.Unlock()
	if createdProject != "override-proj" {
		t.Fatalf("task project = %q, want override-proj", createdProject)
	}
}

// TestSpawnWorker_AutoSubmitFails verifies that a PostTaskInput failure does
// not cause the handler to return an error: worker is still bound, just not
// auto-submitted.
func TestSpawnWorker_AutoSubmitFails(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	e.fake.addTask(taskFor("c1", "/tmp/c1"))
	e.fake.mu.Lock()
	e.fake.nextTaskID = "w1"
	e.fake.inputFail = true
	e.fake.mu.Unlock()
	setupCoordFixture(t, e, "/tmp/c1", "c1", "proj")

	h := NewSpawnWorkerHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, SpawnWorkerInput{
		Cwd:    "/tmp/c1",
		Prompt: "do work",
	}))
	if resp.IsError {
		t.Fatalf("expected success (auto-submit failure is non-fatal), got error: %s", resp.Content[0].Text)
	}
	out := decodeSpawnOutput(t, resp)
	if out.PromptAutoSubmitted {
		t.Fatalf("PromptAutoSubmitted = true, want false when PostTaskInput fails")
	}
	// Worker role and binding must still exist.
	if _, err := e.db.Bindings.GetLiveByTaskID(ctx, "w1"); err != nil {
		t.Fatalf("binding missing after auto-submit failure: %v", err)
	}
}

// TestSpawnWorker_GetTaskFails verifies soft-fail: binding inserted with empty
// worktree_path when GetTask errors.
func TestSpawnWorker_GetTaskFails(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	e.fake.addTask(taskFor("c1", "/tmp/c1"))
	e.fake.mu.Lock()
	e.fake.nextTaskID = "w1"
	e.fake.taskGetFail = true
	e.fake.mu.Unlock()
	setupCoordFixture(t, e, "/tmp/c1", "c1", "proj")

	h := NewSpawnWorkerHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, SpawnWorkerInput{
		Cwd:    "/tmp/c1",
		Prompt: "do work",
	}))
	if resp.IsError {
		t.Fatalf("expected success (GetTask failure is non-fatal): %s", resp.Content[0].Text)
	}
	bnd, err := e.db.Bindings.GetLiveByTaskID(ctx, "w1")
	if err != nil {
		t.Fatalf("binding missing: %v", err)
	}
	if bnd.WorktreePath != "" {
		t.Fatalf("WorktreePath = %q, want empty after GetTask failure", bnd.WorktreePath)
	}
}

// TestSpawnWorker_NonCoordRejected verifies that workers and freelancers cannot
// call hera_spawn_worker.
func TestSpawnWorker_NonCoordRejected(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	orch, _ := e.db.Orchestrators.Create(ctx, "foo")
	role, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w", Kind: db.KindWorker, ArgusProject: "p",
	})
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID:       role.ID,
		ArgusTaskID:  "t-w",
		WorktreePath: "/tmp/w",
	})
	e.fake.addTask(taskFor("t-w", "/tmp/w"))

	h := NewSpawnWorkerHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, SpawnWorkerInput{
		Cwd:    "/tmp/w",
		Prompt: "some prompt",
	}))
	if !resp.IsError {
		t.Fatalf("expected error for worker caller, got success")
	}
	if !strings.Contains(resp.Content[0].Text, "only coordinators") {
		t.Fatalf("error wording: %q", resp.Content[0].Text)
	}
}

// TestSpawnWorker_PromptRequired verifies that an empty prompt is rejected.
func TestSpawnWorker_PromptRequired(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	e.fake.addTask(taskFor("c1", "/tmp/c1"))
	setupCoordFixture(t, e, "/tmp/c1", "c1", "proj")

	h := NewSpawnWorkerHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, SpawnWorkerInput{
		Cwd:    "/tmp/c1",
		Prompt: "   ",
	}))
	if !resp.IsError {
		t.Fatalf("expected error for empty prompt, got success")
	}
	if !strings.Contains(resp.Content[0].Text, "prompt is required") {
		t.Fatalf("error wording: %q", resp.Content[0].Text)
	}
}

// TestSpawnWorker_UnknownCwd verifies rejection for an unrecognized cwd.
func TestSpawnWorker_UnknownCwd(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	h := NewSpawnWorkerHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, SpawnWorkerInput{
		Cwd:    "/nowhere",
		Prompt: "do stuff",
	}))
	if !resp.IsError {
		t.Fatalf("expected error for unknown cwd, got success")
	}
}

// TestSpawnWorker_ArgusTaskNameIsRoleName verifies that the created argus task
// is named after the worker role, not after the orientation preamble (BUG-047).
func TestSpawnWorker_ArgusTaskNameIsRoleName(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	e.fake.addTask(taskFor("c1", "/tmp/c1"))
	e.fake.mu.Lock()
	e.fake.nextTaskID = "w1"
	e.fake.mu.Unlock()
	setupCoordFixture(t, e, "/tmp/c1", "c1", "proj")

	h := NewSpawnWorkerHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, SpawnWorkerInput{
		Cwd:      "/tmp/c1",
		Prompt:   "Fix the auth bug",
		RoleName: "fix-auth-bug",
	}))
	out := decodeSpawnOutput(t, resp)

	// The argus task name must equal the role name, not the preamble.
	e.fake.mu.Lock()
	var taskName string
	for _, task := range e.fake.tasks {
		if task.ID == out.ArgusTaskID {
			taskName = task.Name
		}
	}
	e.fake.mu.Unlock()

	if taskName != "fix-auth-bug" {
		t.Fatalf("argus task Name = %q, want %q (role name); preamble must not become the title", taskName, "fix-auth-bug")
	}
}

// TestSpawnWorker_EmpowermentSentencePresent verifies that the orientation
// preamble includes the worker-promotion sentence so workers know they can
// become sub-coordinators when needed.
func TestSpawnWorker_EmpowermentSentencePresent(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	e.fake.addTask(taskFor("c1", "/tmp/c1"))
	e.fake.mu.Lock()
	e.fake.nextTaskID = "w1"
	e.fake.mu.Unlock()
	setupCoordFixture(t, e, "/tmp/c1", "c1", "proj")

	h := NewSpawnWorkerHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, SpawnWorkerInput{
		Cwd:    "/tmp/c1",
		Prompt: "do the work",
	}))
	decodeSpawnOutput(t, resp)

	e.fake.mu.Lock()
	inputs := e.fake.createInputs
	e.fake.mu.Unlock()

	if len(inputs) == 0 {
		t.Fatal("expected at least one CreateTask call")
	}
	taskPrompt := inputs[len(inputs)-1].Prompt
	if !strings.Contains(taskPrompt, "hera_new_orchestrator") {
		t.Fatalf("worker prompt must include hera_new_orchestrator empowerment sentence; got %q", taskPrompt)
	}
	if !strings.Contains(taskPrompt, "hera_spawn_worker") {
		t.Fatalf("worker prompt must include hera_spawn_worker empowerment sentence; got %q", taskPrompt)
	}
}

// TestSpawnWorker_MetaRoleSet verifies the role=worker meta is applied to the
// new argus task.
func TestSpawnWorker_MetaRoleSet(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	e.fake.addTask(taskFor("c1", "/tmp/c1"))
	e.fake.mu.Lock()
	e.fake.nextTaskID = "w1"
	e.fake.mu.Unlock()
	setupCoordFixture(t, e, "/tmp/c1", "c1", "proj")

	h := NewSpawnWorkerHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, SpawnWorkerInput{
		Cwd:    "/tmp/c1",
		Prompt: "Implement the parser",
	}))
	decodeSpawnOutput(t, resp)

	e.fake.mu.Lock()
	metas := e.fake.metaPuts
	e.fake.mu.Unlock()
	found := false
	for _, m := range metas {
		if m.taskID == "w1" && m.key == "role" && m.value == "worker" {
			found = true
		}
	}
	if !found {
		t.Fatalf("meta role=worker not set on new task; metaPuts=%+v", metas)
	}
}
