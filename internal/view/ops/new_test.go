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

func TestNewOrchestrator_DuplicateActiveNameRejected(t *testing.T) {
	s, db, _, _, _ := newTestService()
	db.seedOrchestrator("foo", false)

	_, err := s.NewOrchestrator(context.Background(), NewOrchestratorInput{Name: "foo"})
	if v := asValidation(err); v == nil {
		t.Fatalf("expected ValidationError, got %v", err)
	} else if !strings.Contains(v.Message, "already exists") {
		t.Fatalf("message = %q", v.Message)
	}
}

func TestNewOrchestrator_ArchivedDuplicateAllowed(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	db.seedOrchestrator("foo", true)

	got, err := s.NewOrchestrator(context.Background(), NewOrchestratorInput{Name: "foo", Mission: "ship F"})
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

	_, err := s.NewOrchestrator(context.Background(), NewOrchestratorInput{Name: "foo", Mission: "ship F"})
	if err != nil {
		t.Fatalf("NewOrchestrator: %v", err)
	}
	if len(argus.createCalls) != 1 {
		t.Fatalf("expected 1 CreateTask call, got %d", len(argus.createCalls))
	}
	req := argus.createCalls[0]
	if req.Project != "foo" {
		t.Fatalf("Project = %q, want %q", req.Project, "foo")
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
	if !strings.Contains(req.Prompt, `mission="ship F"`) {
		t.Fatalf("prompt missing mission: %q", req.Prompt)
	}
	if !strings.Contains(req.Prompt, `coord_role_name="coord"`) {
		t.Fatalf("prompt missing coord_role_name: %q", req.Prompt)
	}
}

func TestNewOrchestrator_EmptyMissionRendersEmpty(t *testing.T) {
	s, _, argus, _, _ := newTestService()
	_, err := s.NewOrchestrator(context.Background(), NewOrchestratorInput{Name: "foo"})
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
}

func TestNewOrchestrator_PropagatesArgusError(t *testing.T) {
	s, _, argus, _, _ := newTestService()
	argus.createErr = errors.New("boom")

	_, err := s.NewOrchestrator(context.Background(), NewOrchestratorInput{Name: "foo"})
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
