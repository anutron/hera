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
	if got == nil || got.ID == "" {
		t.Fatalf("expected created task, got %+v", got)
	}
	if len(argus.createCalls) != 1 {
		t.Fatalf("expected 1 CreateTask call, got %d", len(argus.createCalls))
	}
}

func TestNewOrchestrator_SpawnsTaskWithBootstrapPrompt(t *testing.T) {
	s, _, argus, _, _ := newTestService()
	argus.createResp = &CreatedTask{ID: "argus-t1", Name: "foo-coord"}

	_, err := s.NewOrchestrator(context.Background(), NewOrchestratorInput{Name: "foo", Project: "my-project", Prompt: "ship F"})
	if err != nil {
		t.Fatalf("NewOrchestrator: %v", err)
	}
	if len(argus.createCalls) != 1 {
		t.Fatalf("expected 1 CreateTask call, got %d", len(argus.createCalls))
	}
	req := argus.createCalls[0]
	if req.Project != "my-project" {
		t.Fatalf("Project = %q, want %q", req.Project, "my-project")
	}
	if req.Name != "foo-coord" {
		t.Fatalf("Name = %q, want %q", req.Name, "foo-coord")
	}
	if !strings.Contains(req.Prompt, "hera_new_orchestrator") {
		t.Fatalf("prompt missing hera_new_orchestrator: %q", req.Prompt)
	}
	if !strings.Contains(req.Prompt, `name="foo"`) {
		t.Fatalf("prompt missing name: %q", req.Prompt)
	}
	// mission is always empty in the bootstrap call
	if !strings.Contains(req.Prompt, `mission=""`) {
		t.Fatalf("prompt must contain mission=\"\": %q", req.Prompt)
	}
	if !strings.Contains(req.Prompt, `coord_role_name="coord"`) {
		t.Fatalf("prompt missing coord_role_name: %q", req.Prompt)
	}
	// user prompt is appended after bootstrap
	if !strings.Contains(req.Prompt, "ship F") {
		t.Fatalf("prompt missing user prompt: %q", req.Prompt)
	}
}

func TestNewOrchestrator_EmptyPromptRendersEmptyMission(t *testing.T) {
	s, _, argus, _, _ := newTestService()
	_, err := s.NewOrchestrator(context.Background(), NewOrchestratorInput{Name: "foo", Project: "my-project"})
	if err != nil {
		t.Fatalf("NewOrchestrator: %v", err)
	}
	if len(argus.createCalls) != 1 {
		t.Fatalf("expected 1 CreateTask call")
	}
	req := argus.createCalls[0]
	if !strings.Contains(req.Prompt, `mission=""`) {
		t.Fatalf("expected empty mission rendering, got %q", req.Prompt)
	}
	// no user-prompt appended: there should be no double-newline
	if strings.Contains(req.Prompt, "\n\n") {
		t.Fatalf("empty user prompt must not append double-newline: %q", req.Prompt)
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

// TestNewOrchestrator_AutoSubmitCRSentAfterCreate verifies that PostTaskInput
// is called with "\r" for the created task so the bootstrap prompt executes.
func TestNewOrchestrator_AutoSubmitCRSentAfterCreate(t *testing.T) {
	s, _, argus, _, _ := newTestService()
	argus.createResp = &CreatedTask{ID: "t1", Name: "foo-coord"}

	_, err := s.NewOrchestrator(context.Background(), NewOrchestratorInput{Name: "foo", Project: "my-project"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	argus.mu.Lock()
	calls := argus.postInputCalls
	argus.mu.Unlock()

	if len(calls) != 1 {
		t.Fatalf("expected 1 PostTaskInput call; got %d", len(calls))
	}
	if calls[0].TaskID != "t1" || string(calls[0].Bytes) != "\r" {
		t.Fatalf("PostTaskInput must send \\r to task t1; got taskID=%q bytes=%q", calls[0].TaskID, calls[0].Bytes)
	}
}

// TestNewOrchestrator_AutoSubmitCRSoftFails verifies that a PostTaskInput
// failure does NOT propagate as a NewOrchestrator error (soft-fail).
func TestNewOrchestrator_AutoSubmitCRSoftFails(t *testing.T) {
	s, _, argus, _, logger := newTestService()
	argus.createResp = &CreatedTask{ID: "t1", Name: "foo-coord"}
	argus.postInputErr = errors.New("pty write failed")

	_, err := s.NewOrchestrator(context.Background(), NewOrchestratorInput{Name: "foo", Project: "my-project"})
	if err != nil {
		t.Fatalf("auto-submit CR failure must not propagate: %v", err)
	}

	logger.mu.Lock()
	msgs := logger.messages
	logger.mu.Unlock()
	if len(msgs) == 0 || !strings.Contains(msgs[0], "auto-submit") {
		t.Fatalf("expected auto-submit error logged; got %v", msgs)
	}
}
