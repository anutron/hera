package ops

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNewOrchestrator_EmptyName(t *testing.T) {
	s, _, _, _, _ := newTestService()
	_, err := s.NewOrchestrator(context.Background(), NewOrchestratorInput{})
	if v := asValidation(err); v == nil {
		t.Fatalf("expected ValidationError, got %v", err)
	} else if !strings.Contains(v.Message, "name is required") {
		t.Fatalf("message = %q", v.Message)
	}
}

func TestNewOrchestrator_EmptyProject(t *testing.T) {
	s, _, _, _, _ := newTestService()
	_, err := s.NewOrchestrator(context.Background(), NewOrchestratorInput{Name: "foo"})
	if v := asValidation(err); v == nil {
		t.Fatalf("expected ValidationError for missing project, got %v", err)
	} else if !strings.Contains(v.Message, "project is required") {
		t.Fatalf("message = %q, want \"project is required\"", v.Message)
	}
}

func TestNewOrchestrator_DuplicateActiveNameRejected(t *testing.T) {
	s, db, _, _, _ := newTestService()
	db.seedOrchestrator("foo", false)

	_, err := s.NewOrchestrator(context.Background(), NewOrchestratorInput{Name: "foo", Project: "my-project"})
	if v := asValidation(err); v == nil {
		t.Fatalf("expected ValidationError, got %v", err)
	} else if !strings.Contains(v.Message, "already exists") {
		t.Fatalf("message = %q", v.Message)
	}
}

func TestNewOrchestrator_ArchivedDuplicateAllowed(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	db.seedOrchestrator("foo", true)

	got, err := s.NewOrchestrator(context.Background(), NewOrchestratorInput{Name: "foo", Project: "my-project", Prompt: "ship F"})
	if err != nil {
		t.Fatalf("NewOrchestrator: %v", err)
	}
	if got == nil || got.ArgusTaskID == "" {
		t.Fatalf("expected created result, got %+v", got)
	}
	if len(argus.createCalls) != 1 {
		t.Fatalf("expected 1 CreateTask call, got %d", len(argus.createCalls))
	}
}

func TestNewOrchestrator_PropagatesArgusError(t *testing.T) {
	s, _, argus, _, _ := newTestService()
	argus.createErr = errors.New("boom")

	_, err := s.NewOrchestrator(context.Background(), NewOrchestratorInput{Name: "foo", Project: "my-project"})
	if err == nil {
		t.Fatalf("expected error")
	}
	if asValidation(err) != nil {
		t.Fatalf("argus failure should not surface as ValidationError: %v", err)
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error should wrap argus error: %v", err)
	}
}

// TestNewOrchestrator_CreatesOrchestratorRoleAndBinding verifies the born-bound
// pattern: the orchestrator row, coordinator role, and binding are all inserted
// before the function returns — no need for the spawned task to call
// hera_new_orchestrator.
func TestNewOrchestrator_CreatesOrchestratorRoleAndBinding(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	argus.createResp = &CreatedTask{ID: "coord-task-1", Name: "foo-coord"}
	argus.getTaskResp = &TaskDetails{
		ID:           "coord-task-1",
		WorktreePath: "/Users/x/.argus/worktrees/my-project/foo-coord",
	}

	res, err := s.NewOrchestrator(context.Background(), NewOrchestratorInput{
		Name:    "foo",
		Project: "my-project",
		Prompt:  "ship the feature",
	})
	if err != nil {
		t.Fatalf("NewOrchestrator: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil result")
	}
	if res.OrchestratorID == 0 {
		t.Fatal("OrchestratorID must be non-zero")
	}
	if res.RoleID == 0 {
		t.Fatal("RoleID must be non-zero")
	}
	if res.ArgusTaskID != "coord-task-1" {
		t.Fatalf("ArgusTaskID: want %q, got %q", "coord-task-1", res.ArgusTaskID)
	}

	// Orchestrator row must exist.
	db.mu.Lock()
	var orch *Orchestrator
	for _, o := range db.orchestrators {
		if o.Name == "foo" {
			orch = o
			break
		}
	}
	db.mu.Unlock()
	if orch == nil {
		t.Fatal("orchestrator row must be inserted")
	}
	if orch.ID != res.OrchestratorID {
		t.Fatalf("result.OrchestratorID %d != inserted orchestrator %d", res.OrchestratorID, orch.ID)
	}

	// Coordinator role must exist under the orchestrator.
	db.mu.Lock()
	var coordRole *Role
	for _, r := range db.roles {
		if r.Kind == KindCoordinator {
			coordRole = r
			break
		}
	}
	db.mu.Unlock()
	if coordRole == nil {
		t.Fatal("coordinator role must be inserted")
	}
	if coordRole.OrchestratorID != orch.ID {
		t.Fatalf("role.OrchestratorID: want %d, got %d", orch.ID, coordRole.OrchestratorID)
	}
	if coordRole.ArgusProject != "my-project" {
		t.Fatalf("role.ArgusProject: want %q, got %q", "my-project", coordRole.ArgusProject)
	}
	if coordRole.Prompt != "ship the feature" {
		t.Fatalf("role.Prompt: want %q, got %q", "ship the feature", coordRole.Prompt)
	}

	// Binding must exist with the resolved worktree path.
	db.mu.Lock()
	var bnd *Binding
	for _, b := range db.bindings {
		if b.RoleID == coordRole.ID {
			bnd = b
			break
		}
	}
	db.mu.Unlock()
	if bnd == nil {
		t.Fatal("binding must be inserted")
	}
	if bnd.ArgusTaskID != "coord-task-1" {
		t.Fatalf("binding.ArgusTaskID: want %q, got %q", "coord-task-1", bnd.ArgusTaskID)
	}
	if bnd.WorktreePath != "/Users/x/.argus/worktrees/my-project/foo-coord" {
		t.Fatalf("binding.WorktreePath: want %q, got %q",
			"/Users/x/.argus/worktrees/my-project/foo-coord", bnd.WorktreePath)
	}
}

// TestNewOrchestrator_TaskCreatedInProject verifies the argus task is created in
// the given project with coordinator meta and the task name is name+"-coord".
func TestNewOrchestrator_TaskCreatedInProject(t *testing.T) {
	s, _, argus, _, _ := newTestService()
	argus.createResp = &CreatedTask{ID: "t1", Name: "acme-coord"}
	argus.getTaskResp = &TaskDetails{ID: "t1", WorktreePath: "/wt"}

	_, err := s.NewOrchestrator(context.Background(), NewOrchestratorInput{
		Name: "acme", Project: "acme-api",
	})
	if err != nil {
		t.Fatalf("NewOrchestrator: %v", err)
	}
	if len(argus.createCalls) != 1 {
		t.Fatalf("expected 1 CreateTask call, got %d", len(argus.createCalls))
	}
	req := argus.createCalls[0]
	if req.Project != "acme-api" {
		t.Fatalf("Project: want %q, got %q", "acme-api", req.Project)
	}
	if req.Name != "acme-coord" {
		t.Fatalf("Name: want %q, got %q", "acme-coord", req.Name)
	}
	if req.Meta == nil || req.Meta["role"] != "coordinator" {
		t.Fatalf("Meta[\"role\"]: want \"coordinator\", got %v", req.Meta)
	}
}

// TestNewOrchestrator_PromptIncludesOrchNameAndUserText verifies that the task
// prompt contains the orchestrator name (orientation) and the user's prompt text.
// It must NOT contain hera_new_orchestrator — the task is born-bound.
func TestNewOrchestrator_PromptIncludesOrchNameAndUserText(t *testing.T) {
	s, _, argus, _, _ := newTestService()
	argus.createResp = &CreatedTask{ID: "t1", Name: "bar-coord"}
	argus.getTaskResp = &TaskDetails{ID: "t1", WorktreePath: "/wt"}

	_, err := s.NewOrchestrator(context.Background(), NewOrchestratorInput{
		Name: "bar", Project: "bar-project", Prompt: "ship the widget",
	})
	if err != nil {
		t.Fatalf("NewOrchestrator: %v", err)
	}
	prompt := argus.createCalls[0].Prompt
	if !strings.Contains(prompt, "bar") {
		t.Fatalf("task prompt must name the orchestrator; got %q", prompt)
	}
	if !strings.Contains(prompt, "ship the widget") {
		t.Fatalf("task prompt must include user text; got %q", prompt)
	}
	if strings.Contains(prompt, "hera_new_orchestrator") {
		t.Fatalf("born-bound task must NOT contain hera_new_orchestrator; got %q", prompt)
	}
}

// TestNewOrchestrator_EmptyUserPromptNoDoubleNewline verifies that when the
// user's prompt is empty, the task prompt does not contain a trailing double-newline.
func TestNewOrchestrator_EmptyUserPromptNoDoubleNewline(t *testing.T) {
	s, _, argus, _, _ := newTestService()
	_, err := s.NewOrchestrator(context.Background(), NewOrchestratorInput{Name: "foo", Project: "my-project"})
	if err != nil {
		t.Fatalf("NewOrchestrator: %v", err)
	}
	if len(argus.createCalls) != 1 {
		t.Fatalf("expected 1 CreateTask call")
	}
	prompt := argus.createCalls[0].Prompt
	if strings.Contains(prompt, "\n\n") {
		t.Fatalf("empty user prompt must not append double-newline: %q", prompt)
	}
}

// TestNewOrchestrator_GetTaskFailureSoftDegrades verifies that if GetTask fails,
// the binding is still inserted (with an empty worktree path) and no error is
// returned for the task-create-succeeded case.
func TestNewOrchestrator_GetTaskFailureSoftDegrades(t *testing.T) {
	s, db, argus, _, l := newTestService()
	argus.createResp = &CreatedTask{ID: "t9", Name: "foo-coord"}
	argus.getTaskErr = ErrArgusTaskGone

	res, err := s.NewOrchestrator(context.Background(), NewOrchestratorInput{Name: "foo", Project: "my-project"})
	if err != nil {
		t.Fatalf("NewOrchestrator should succeed even when GetTask fails; got: %v", err)
	}
	if res == nil || res.RoleID == 0 {
		t.Fatal("expected a role to be created even with GetTask failure")
	}

	db.mu.Lock()
	var bound *Binding
	for _, bnd := range db.bindings {
		if bnd.ArgusTaskID == "t9" {
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

	l.mu.Lock()
	msgs := l.messages
	l.mu.Unlock()
	hasWarn := false
	for _, m := range msgs {
		if strings.Contains(m, "GetTask") || strings.Contains(m, "worktree_path") || strings.Contains(m, "worktree") {
			hasWarn = true
			break
		}
	}
	if !hasWarn {
		t.Fatalf("expected a logged warning about worktree path; got messages: %v", msgs)
	}
}

// TestNewOrchestrator_RoleInsertFailure_NoRollback verifies that when CreateRole
// fails after the argus task was created, the error is returned, the orphaned
// task id is logged, and NO DeleteTask is issued.
func TestNewOrchestrator_RoleInsertFailure_NoRollback(t *testing.T) {
	s, db, argus, _, l := newTestService()
	argus.createResp = &CreatedTask{ID: "t9", Name: "foo-coord"}
	argus.getTaskResp = &TaskDetails{ID: "t9", WorktreePath: "/wt"}
	db.createRoleErr = errors.New("role insert boom")

	_, err := s.NewOrchestrator(context.Background(), NewOrchestratorInput{Name: "foo", Project: "my-project"})
	if err == nil {
		t.Fatal("expected error when role insert fails after CreateTask")
	}

	argus.mu.Lock()
	deletes := append([]string(nil), argus.deleteCalls...)
	argus.mu.Unlock()
	if len(deletes) != 0 {
		t.Fatalf("orphaned argus task MUST NOT be deleted on insert failure; got DeleteTask calls %v", deletes)
	}

	l.mu.Lock()
	msgs := append([]string(nil), l.messages...)
	l.mu.Unlock()
	loggedOrphan := false
	for _, m := range msgs {
		if strings.Contains(m, "t9") {
			loggedOrphan = true
			break
		}
	}
	if !loggedOrphan {
		t.Fatalf("expected the orphaned task id t9 to be logged; got messages: %v", msgs)
	}
}

// TestNewOrchestrator_BindingInsertFailure_NoRollback verifies that when
// CreateBinding fails after the role was inserted, the error is returned, the
// orphan is logged, and no DeleteTask is issued.
func TestNewOrchestrator_BindingInsertFailure_NoRollback(t *testing.T) {
	s, db, argus, _, l := newTestService()
	argus.createResp = &CreatedTask{ID: "t9", Name: "foo-coord"}
	argus.getTaskResp = &TaskDetails{ID: "t9", WorktreePath: "/wt"}
	db.createBindingErr = errors.New("binding insert boom")

	_, err := s.NewOrchestrator(context.Background(), NewOrchestratorInput{Name: "foo", Project: "my-project"})
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
		if strings.Contains(m, "t9") {
			loggedOrphan = true
			break
		}
	}
	if !loggedOrphan {
		t.Fatalf("expected the orphaned task id t9 to be logged; got messages: %v", msgs)
	}
}
