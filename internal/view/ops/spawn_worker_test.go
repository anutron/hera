package ops

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestSpawnWorker_EmptyPrompt_ValidationError asserts that an empty or
// whitespace-only prompt is rejected with a validation error and that no
// argus task, role, or binding is created.
func TestSpawnWorker_EmptyPrompt_ValidationError(t *testing.T) {
	s, _, argus, _, _ := newTestService()
	orch := s.DB.(*fakeDB).seedOrchestrator("foo", false)
	coordRole := s.DB.(*fakeDB).seedRole(orch.ID, "coord", KindCoordinator, "foo-frontend", false)

	for _, prompt := range []string{"", "   ", "\t\n"} {
		argus.createCalls = nil
		_, err := s.SpawnWorker(context.Background(), SpawnWorkerInput{
			TargetOrchestratorID: orch.ID,
			CoordRoleID:          coordRole.ID,
			Prompt:               prompt,
		})
		if v := asValidation(err); v == nil {
			t.Fatalf("prompt=%q: expected ValidationError, got %v", prompt, err)
		}
		if len(argus.createCalls) != 0 {
			t.Fatalf("prompt=%q: no argus task should be created on validation error; got %d calls", prompt, len(argus.createCalls))
		}
		if len(s.DB.(*fakeDB).roles) != 1 {
			t.Fatalf("prompt=%q: no new role should be inserted; got %d roles", prompt, len(s.DB.(*fakeDB).roles))
		}
	}
}

// TestSpawnWorker_CreatesTaskInCoordsProject asserts that the argus task is
// created in the coordinator's argus_project with meta:hera.role=worker.
func TestSpawnWorker_CreatesTaskInCoordsProject(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", false)
	coordRole := db.seedRole(orch.ID, "coord", KindCoordinator, "foo-frontend", false)
	argus.createResp = &CreatedTask{ID: "T9", Name: "build-the-sidebar"}
	argus.getTaskResp = &TaskDetails{ID: "T9", WorktreePath: "/Users/x/.argus/worktrees/foo-frontend/build-the-sidebar"}

	_, err := s.SpawnWorker(context.Background(), SpawnWorkerInput{
		TargetOrchestratorID: orch.ID,
		CoordRoleID:          coordRole.ID,
		Prompt:               "build the sidebar",
	})
	if err != nil {
		t.Fatalf("SpawnWorker: %v", err)
	}

	if len(argus.createCalls) != 1 {
		t.Fatalf("expected 1 CreateTask call, got %d", len(argus.createCalls))
	}
	req := argus.createCalls[0]
	if req.Project != "foo-frontend" {
		t.Fatalf("Project: want %q, got %q", "foo-frontend", req.Project)
	}
	// Meta must carry hera.role=worker.
	if req.Meta == nil || req.Meta["role"] != "worker" {
		t.Fatalf("Meta[\"role\"]: want \"worker\", got %v", req.Meta)
	}
}

// TestSpawnWorker_InsertsRoleAndBindingWithWorktreePath asserts that after
// the argus task is created and GetTask resolves the worktree path, a worker
// role and a binding carrying that path are inserted.
func TestSpawnWorker_InsertsRoleAndBindingWithWorktreePath(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", false)
	coordRole := db.seedRole(orch.ID, "coord", KindCoordinator, "foo-frontend", false)
	argus.createResp = &CreatedTask{ID: "T9", Name: "build-the-sidebar"}
	argus.getTaskResp = &TaskDetails{
		ID:           "T9",
		WorktreePath: "/Users/x/.argus/worktrees/foo-frontend/build-the-sidebar",
	}

	res, err := s.SpawnWorker(context.Background(), SpawnWorkerInput{
		TargetOrchestratorID: orch.ID,
		CoordRoleID:          coordRole.ID,
		Prompt:               "build the sidebar",
	})
	if err != nil {
		t.Fatalf("SpawnWorker: %v", err)
	}
	if res == nil {
		t.Fatal("SpawnWorker: expected non-nil result")
	}
	if res.RoleID == 0 {
		t.Fatal("SpawnWorker: RoleID should be non-zero")
	}
	if res.ArgusTaskID != "T9" {
		t.Fatalf("ArgusTaskID: want %q, got %q", "T9", res.ArgusTaskID)
	}

	// Verify the role was inserted.
	db.mu.Lock()
	var newRole *Role
	for _, r := range db.roles {
		if r.Kind == KindWorker {
			newRole = r
			break
		}
	}
	db.mu.Unlock()

	if newRole == nil {
		t.Fatal("expected a worker role to be inserted")
	}
	if newRole.OrchestratorID != orch.ID {
		t.Fatalf("role.OrchestratorID: want %d, got %d", orch.ID, newRole.OrchestratorID)
	}
	if newRole.ArgusProject != "foo-frontend" {
		t.Fatalf("role.ArgusProject: want %q, got %q", "foo-frontend", newRole.ArgusProject)
	}
	if newRole.Prompt != "build the sidebar" {
		t.Fatalf("role.Prompt: want %q, got %q", "build the sidebar", newRole.Prompt)
	}

	// Verify the binding was inserted with the worktree path.
	db.mu.Lock()
	var newBinding *Binding
	for _, bnd := range db.bindings {
		if bnd.RoleID == newRole.ID {
			newBinding = bnd
			break
		}
	}
	db.mu.Unlock()

	if newBinding == nil {
		t.Fatal("expected a binding to be inserted")
	}
	if newBinding.ArgusTaskID != "T9" {
		t.Fatalf("binding.ArgusTaskID: want %q, got %q", "T9", newBinding.ArgusTaskID)
	}
	if newBinding.WorktreePath != "/Users/x/.argus/worktrees/foo-frontend/build-the-sidebar" {
		t.Fatalf("binding.WorktreePath: want %q, got %q",
			"/Users/x/.argus/worktrees/foo-frontend/build-the-sidebar", newBinding.WorktreePath)
	}
}

// TestSpawnWorker_PromptSetFromOperatorText asserts that the worker role's prompt
// is set to the operator's prompt text (not the orientation-prefixed version).
func TestSpawnWorker_PromptSetFromOperatorText(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", false)
	coordRole := db.seedRole(orch.ID, "coord", KindCoordinator, "foo-frontend", false)
	argus.createResp = &CreatedTask{ID: "T1", Name: "migrate-the-schema"}
	argus.getTaskResp = &TaskDetails{ID: "T1", WorktreePath: "/wt/path"}

	_, err := s.SpawnWorker(context.Background(), SpawnWorkerInput{
		TargetOrchestratorID: orch.ID,
		CoordRoleID:          coordRole.ID,
		Prompt:               "migrate the schema",
	})
	if err != nil {
		t.Fatalf("SpawnWorker: %v", err)
	}

	db.mu.Lock()
	var workerRole *Role
	for _, r := range db.roles {
		if r.Kind == KindWorker {
			workerRole = r
			break
		}
	}
	db.mu.Unlock()

	if workerRole == nil {
		t.Fatal("expected worker role")
	}
	if workerRole.Prompt != "migrate the schema" {
		t.Fatalf("Prompt: want %q, got %q", "migrate the schema", workerRole.Prompt)
	}
}

// TestSpawnWorker_OrientationPrefixedPrompt asserts that the created argus
// task's prompt is the orientation prefix followed by the operator's text,
// and that the prefix names the coordinator and mentions hera_send.
func TestSpawnWorker_OrientationPrefixedPrompt(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", false)
	// The coord role's name is what appears in the prefix.
	coordRole := db.seedRole(orch.ID, "foo-coord", KindCoordinator, "foo-frontend", false)
	argus.createResp = &CreatedTask{ID: "T1", Name: "migrate-the-schema"}
	argus.getTaskResp = &TaskDetails{ID: "T1", WorktreePath: "/wt/path"}

	_, err := s.SpawnWorker(context.Background(), SpawnWorkerInput{
		TargetOrchestratorID: orch.ID,
		CoordRoleID:          coordRole.ID,
		Prompt:               "migrate the schema",
	})
	if err != nil {
		t.Fatalf("SpawnWorker: %v", err)
	}

	if len(argus.createCalls) != 1 {
		t.Fatalf("expected 1 CreateTask call, got %d", len(argus.createCalls))
	}
	prompt := argus.createCalls[0].Prompt

	// Prefix must name the coordinator (sourced from the coord role).
	if !strings.Contains(prompt, "foo-coord") {
		t.Fatalf("task prompt must name coordinator \"foo-coord\"; got %q", prompt)
	}
	// Prefix must mention hera_send.
	if !strings.Contains(prompt, "hera_send") {
		t.Fatalf("task prompt must mention hera_send; got %q", prompt)
	}
	// Prefix must include the worker-promotion empowerment sentence.
	if !strings.Contains(prompt, "hera_new_orchestrator") {
		t.Fatalf("task prompt must include worker-promotion sentence (hera_new_orchestrator); got %q", prompt)
	}
	if !strings.Contains(prompt, "hera_spawn_worker") {
		t.Fatalf("task prompt must include worker-promotion sentence (hera_spawn_worker); got %q", prompt)
	}
	// Operator's text must appear verbatim in the prompt (before the suffix).
	if !strings.Contains(prompt, "migrate the schema") {
		t.Fatalf("task prompt must contain operator text; got %q", prompt)
	}
}

// TestSpawnWorker_PrefixNamesCoordRoleNotSelectedRow asserts the orientation
// prefix names the COORDINATOR (resolved from the coord role) regardless of
// which row was selected. The bug this guards: when an AGENT row is selected,
// the prefix must NOT say the agent's name. The op sources the name from the
// coord role it loads, so any caller-supplied name is irrelevant.
func TestSpawnWorker_PrefixNamesCoordRoleNotSelectedRow(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("acme", false)
	coordRole := db.seedRole(orch.ID, "acme-coord", KindCoordinator, "acme-api", false)
	// A sibling agent row whose name would have been (wrongly) used as the
	// prefix coordinator name under the old caller-supplied path.
	db.seedRole(orch.ID, "some-agent", KindWorker, "acme-api", false)
	argus.createResp = &CreatedTask{ID: "T7", Name: "do-thing"}
	argus.getTaskResp = &TaskDetails{ID: "T7", WorktreePath: "/wt"}

	_, err := s.SpawnWorker(context.Background(), SpawnWorkerInput{
		TargetOrchestratorID: orch.ID,
		CoordRoleID:          coordRole.ID,
		Prompt:               "do the thing",
	})
	if err != nil {
		t.Fatalf("SpawnWorker: %v", err)
	}

	prompt := argus.createCalls[0].Prompt
	if !strings.Contains(prompt, "acme-coord") {
		t.Fatalf("prefix must name the coordinator \"acme-coord\"; got %q", prompt)
	}
	if strings.Contains(prompt, "some-agent") {
		t.Fatalf("prefix must NOT name the selected agent row \"some-agent\"; got %q", prompt)
	}
}

// TestSpawnWorker_RoleNameDerivedFromPrompt asserts that the worker role name
// is derived from the prompt as a slug of the head.
func TestSpawnWorker_RoleNameDerivedFromPrompt(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", false)
	coordRole := db.seedRole(orch.ID, "coord", KindCoordinator, "foo-frontend", false)
	argus.createResp = &CreatedTask{ID: "T1", Name: "auto"}
	argus.getTaskResp = &TaskDetails{ID: "T1", WorktreePath: "/wt"}

	_, err := s.SpawnWorker(context.Background(), SpawnWorkerInput{
		TargetOrchestratorID: orch.ID,
		CoordRoleID:          coordRole.ID,
		Prompt:               "build the sidebar",
	})
	if err != nil {
		t.Fatalf("SpawnWorker: %v", err)
	}

	db.mu.Lock()
	var workerRole *Role
	for _, r := range db.roles {
		if r.Kind == KindWorker {
			workerRole = r
			break
		}
	}
	db.mu.Unlock()

	if workerRole == nil {
		t.Fatal("expected a worker role")
	}
	// Name must be a slug: non-empty, all lowercase, no spaces.
	if workerRole.Name == "" {
		t.Fatal("role name must not be empty")
	}
	if strings.Contains(workerRole.Name, " ") {
		t.Fatalf("role name must not contain spaces; got %q", workerRole.Name)
	}
}

// TestSpawnWorker_CollisionSuffix asserts that when the derived name collides
// with an existing non-archived role, the new role gets a -2, -3 suffix.
func TestSpawnWorker_CollisionSuffix(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", false)
	coordRole := db.seedRole(orch.ID, "coord", KindCoordinator, "foo-frontend", false)
	// Existing sibling with the name that the prompt would derive.
	db.seedRole(orch.ID, "build-the-sidebar", KindWorker, "foo-frontend", false)

	argus.createResp = &CreatedTask{ID: "T2", Name: "auto"}
	argus.getTaskResp = &TaskDetails{ID: "T2", WorktreePath: "/wt"}

	_, err := s.SpawnWorker(context.Background(), SpawnWorkerInput{
		TargetOrchestratorID: orch.ID,
		CoordRoleID:          coordRole.ID,
		Prompt:               "build the sidebar",
	})
	if err != nil {
		t.Fatalf("SpawnWorker: %v", err)
	}

	db.mu.Lock()
	var workerRoles []*Role
	for _, r := range db.roles {
		if r.Kind == KindWorker {
			cp := *r
			workerRoles = append(workerRoles, &cp)
		}
	}
	db.mu.Unlock()

	// There should be 2 worker roles: the existing one and the new suffixed one.
	if len(workerRoles) != 2 {
		t.Fatalf("expected 2 worker roles (original + new suffixed), got %d", len(workerRoles))
	}
	var names []string
	for _, r := range workerRoles {
		names = append(names, r.Name)
	}
	// The new role must have a -2 (or similar) suffix.
	hasSuffix := false
	for _, n := range names {
		if n == "build-the-sidebar-2" {
			hasSuffix = true
		}
	}
	if !hasSuffix {
		t.Fatalf("expected new role name to be \"build-the-sidebar-2\"; got names: %v", names)
	}
}

// TestSpawnWorker_GetTaskFailureSoftDegrades asserts that if GetTask fails,
// the binding is still inserted (possibly with an empty worktree path) and
// the spawn does not return an error for the task-create-succeeded case.
func TestSpawnWorker_GetTaskFailureSoftDegrades(t *testing.T) {
	s, db, argus, _, l := newTestService()
	orch := db.seedOrchestrator("foo", false)
	coordRole := db.seedRole(orch.ID, "coord", KindCoordinator, "foo-frontend", false)
	argus.createResp = &CreatedTask{ID: "T9", Name: "worker"}
	argus.getTaskErr = ErrArgusTaskGone // simulate GetTask failure

	res, err := s.SpawnWorker(context.Background(), SpawnWorkerInput{
		TargetOrchestratorID: orch.ID,
		CoordRoleID:          coordRole.ID,
		Prompt:               "do the thing",
	})
	if err != nil {
		t.Fatalf("SpawnWorker should succeed even when GetTask fails; got: %v", err)
	}
	if res == nil || res.RoleID == 0 {
		t.Fatal("expected a role to be created even with GetTask failure")
	}

	// A binding should still exist, AND its worktree path MUST be empty
	// (GetTask could not resolve it).
	db.mu.Lock()
	var bound *Binding
	for _, bnd := range db.bindings {
		if bnd.ArgusTaskID == "T9" {
			cp := *bnd
			bound = &cp
			break
		}
	}
	db.mu.Unlock()

	if bound == nil {
		t.Fatal("binding should be inserted even when GetTask fails")
	}
	if bound.WorktreePath != "" {
		t.Fatalf("binding inserted on GetTask failure must have an EMPTY worktree_path; got %q", bound.WorktreePath)
	}

	// A warning must be logged.
	l.mu.Lock()
	msgs := l.messages
	l.mu.Unlock()
	hasWarn := false
	for _, m := range msgs {
		if strings.Contains(m, "worktree") || strings.Contains(m, "GetTask") || strings.Contains(m, "worktree_path") {
			hasWarn = true
			break
		}
	}
	if !hasWarn {
		t.Fatalf("expected a logged warning about worktree path; got messages: %v", msgs)
	}
}

// TestSpawnWorker_ArgusCreateFailure_ReturnsError asserts that if argus
// CreateTask fails, an error is returned and no role or binding is inserted.
func TestSpawnWorker_ArgusCreateFailure_ReturnsError(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", false)
	coordRole := db.seedRole(orch.ID, "coord", KindCoordinator, "foo-frontend", false)
	argus.createErr = ErrArgusTaskGone

	_, err := s.SpawnWorker(context.Background(), SpawnWorkerInput{
		TargetOrchestratorID: orch.ID,
		CoordRoleID:          coordRole.ID,
		Prompt:               "do the thing",
	})
	if err == nil {
		t.Fatal("expected error when argus CreateTask fails")
	}

	// No role or binding should be inserted.
	db.mu.Lock()
	workerCount := 0
	for _, r := range db.roles {
		if r.Kind == KindWorker {
			workerCount++
		}
	}
	bindCount := len(db.bindings)
	db.mu.Unlock()

	if workerCount != 0 {
		t.Fatalf("no worker role should be inserted on argus failure; got %d", workerCount)
	}
	if bindCount != 0 {
		t.Fatalf("no binding should be inserted on argus failure; got %d", bindCount)
	}
}

// TestSpawnWorker_RoleInsertFailureAfterCreate_NoRollback asserts D2/Risks:
// when the role insert fails AFTER the argus task was created, SpawnWorker
// returns the error, logs the orphaned task id, and does NOT issue a DeleteTask
// (the orphan is recoverable as a freelancer, never destroyed by a fallible
// rollback).
func TestSpawnWorker_RoleInsertFailureAfterCreate_NoRollback(t *testing.T) {
	s, db, argus, _, l := newTestService()
	orch := db.seedOrchestrator("foo", false)
	coordRole := db.seedRole(orch.ID, "coord", KindCoordinator, "foo-frontend", false)
	argus.createResp = &CreatedTask{ID: "T9", Name: "worker"}
	argus.getTaskResp = &TaskDetails{ID: "T9", WorktreePath: "/wt"}
	db.createRoleErr = errors.New("role insert boom")

	_, err := s.SpawnWorker(context.Background(), SpawnWorkerInput{
		TargetOrchestratorID: orch.ID,
		CoordRoleID:          coordRole.ID,
		Prompt:               "do the thing",
	})
	if err == nil {
		t.Fatal("expected error when role insert fails after CreateTask")
	}

	argus.mu.Lock()
	deletes := append([]string(nil), argus.deleteCalls...)
	argus.mu.Unlock()
	if len(deletes) != 0 {
		t.Fatalf("orphaned argus task MUST NOT be deleted on insert failure; got DeleteTask calls %v", deletes)
	}

	// The orphaned task id must be logged.
	l.mu.Lock()
	msgs := append([]string(nil), l.messages...)
	l.mu.Unlock()
	loggedOrphan := false
	for _, m := range msgs {
		if strings.Contains(m, "T9") {
			loggedOrphan = true
			break
		}
	}
	if !loggedOrphan {
		t.Fatalf("expected the orphaned task id T9 to be logged; got messages: %v", msgs)
	}
}

// TestSpawnWorker_BindingInsertFailureAfterCreate_NoRollback is the binding-
// insert sibling of the role-insert case: the role inserts, the binding fails,
// SpawnWorker errors, logs the orphan, and issues no DeleteTask.
func TestSpawnWorker_BindingInsertFailureAfterCreate_NoRollback(t *testing.T) {
	s, db, argus, _, l := newTestService()
	orch := db.seedOrchestrator("foo", false)
	coordRole := db.seedRole(orch.ID, "coord", KindCoordinator, "foo-frontend", false)
	argus.createResp = &CreatedTask{ID: "T9", Name: "worker"}
	argus.getTaskResp = &TaskDetails{ID: "T9", WorktreePath: "/wt"}
	db.createBindingErr = errors.New("binding insert boom")

	_, err := s.SpawnWorker(context.Background(), SpawnWorkerInput{
		TargetOrchestratorID: orch.ID,
		CoordRoleID:          coordRole.ID,
		Prompt:               "do the thing",
	})
	if err == nil {
		t.Fatal("expected error when binding insert fails after CreateTask")
	}

	argus.mu.Lock()
	deletes := append([]string(nil), argus.deleteCalls...)
	argus.mu.Unlock()
	if len(deletes) != 0 {
		t.Fatalf("orphaned argus task MUST NOT be deleted on binding insert failure; got DeleteTask calls %v", deletes)
	}

	l.mu.Lock()
	msgs := append([]string(nil), l.messages...)
	l.mu.Unlock()
	loggedOrphan := false
	for _, m := range msgs {
		if strings.Contains(m, "T9") {
			loggedOrphan = true
			break
		}
	}
	if !loggedOrphan {
		t.Fatalf("expected the orphaned task id T9 to be logged; got messages: %v", msgs)
	}
}

// TestSpawnWorker_EmptySlugFallsBackToWorkerStem asserts that a prompt whose
// slug is empty (e.g. all punctuation) falls back to the "worker" stem.
func TestSpawnWorker_EmptySlugFallsBackToWorkerStem(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", false)
	coordRole := db.seedRole(orch.ID, "coord", KindCoordinator, "foo-frontend", false)
	argus.createResp = &CreatedTask{ID: "T1", Name: "auto"}
	argus.getTaskResp = &TaskDetails{ID: "T1", WorktreePath: "/wt"}

	_, err := s.SpawnWorker(context.Background(), SpawnWorkerInput{
		TargetOrchestratorID: orch.ID,
		CoordRoleID:          coordRole.ID,
		Prompt:               "!!! ???", // all non-word chars → empty slug
	})
	if err != nil {
		t.Fatalf("SpawnWorker: %v", err)
	}

	db.mu.Lock()
	var workerRole *Role
	for _, r := range db.roles {
		if r.Kind == KindWorker {
			workerRole = r
			break
		}
	}
	db.mu.Unlock()

	if workerRole == nil {
		t.Fatal("expected worker role")
	}
	if workerRole.Name == "" {
		t.Fatal("role name must not be empty even for all-punctuation prompts")
	}
	// Must start with the fallback stem "worker".
	if !strings.HasPrefix(workerRole.Name, "worker") {
		t.Fatalf("expected fallback stem \"worker\", got %q", workerRole.Name)
	}
}
