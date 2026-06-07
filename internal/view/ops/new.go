package ops

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// NewOrchestratorInput is the (validated) result of confirming the
// new-coordinator modal.
type NewOrchestratorInput struct {
	// Name is the orchestrator name. Must be non-empty and unique among
	// non-archived orchestrators.
	Name string
	// Project is the argus project slug the coordinator task is created in.
	// Required.
	Project string
	// Branch is the git branch the coordinator task runs from.
	// Optional; an empty string uses the project's default branch.
	Branch string
	// Backend is the argus backend name to use for the coordinator task.
	// Optional; an empty string uses the project's default backend.
	Backend string
	// Prompt is the coordinator's startup instructions (free-form prose).
	// Optional; appended after the orientation prefix when non-empty.
	Prompt string
}

// NewOrchestrator handles the `n` rail operation. It validates the modal
// input, then creates the orchestrator + coordinator role + binding + argus
// task programmatically — the spawned task is born-bound and does NOT need
// to call hera_new_orchestrator itself.
//
// This mirrors the SpawnWorker pattern: all hera DB rows are inserted before
// the argus task starts running, so the Coord pane has a live binding to
// display as soon as the rail repopulates.
//
// Partial-failure handling (same as SpawnWorker):
//   - If CreateRole fails after CreateTask succeeds, the argus task is orphaned
//     (visible as a freelancer) but NOT deleted (see SpawnWorker Risks).
//   - If CreateBinding fails after CreateRole succeeds, same orphan-tolerance.
//   - GetTask failure is soft-degraded: the binding is inserted with an empty
//     worktree_path and a warning is logged.
//
// Returns ErrValidation for user-correctable input problems (empty or
// duplicate name, empty project); any other error is a substrate failure
// (DB read or argus POST) that the caller surfaces to the operator.
func (s *Service) NewOrchestrator(ctx context.Context, in NewOrchestratorInput) (*NewOrchestratorResult, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, validation("name is required")
	}

	project := strings.TrimSpace(in.Project)
	if project == "" {
		return nil, validation("project is required")
	}

	// Uniqueness: only against non-archived orchestrators. Archived rows
	// with the same name do not block creation — that's the
	// "archived orchestrator with same name does not block creation"
	// scenario from the hera-coordination delta spec.
	existing, err := s.DB.GetOrchestratorByName(ctx, name)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("ops.NewOrchestrator: lookup: %w", err)
	}
	if existing != nil && !existing.Archived {
		return nil, validation(fmt.Sprintf("orchestrator %q already exists", name))
	}

	// Create the orchestrator row (idempotent on same-name active).
	orch, err := s.DB.CreateOrchestrator(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("ops.NewOrchestrator: create orchestrator: %w", err)
	}

	// Create the argus task in the given project with coordinator meta.
	req := CreateTaskRequest{
		Project: project,
		Name:    name + "-coord",
		Prompt:  buildCoordPrompt(name, in.Prompt),
		Branch:  in.Branch,
		Backend: in.Backend,
		Meta:    map[string]string{"role": "coordinator"},
	}
	task, err := s.Argus.CreateTask(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("ops.NewOrchestrator: argus CreateTask: %w", err)
	}

	// Read the worktree path via GetTask. Soft-degrade on failure: the binding
	// is still inserted with whatever path we have (possibly empty) so the spawn
	// is not lost. The caller logs prominently.
	worktreePath := ""
	gt, gtErr := s.Argus.GetTask(ctx, task.ID)
	if gtErr != nil {
		s.Logger.Printf("ops.NewOrchestrator: GetTask(%s) failed — binding will have empty worktree_path: %v", task.ID, gtErr)
	} else if gt != nil {
		worktreePath = gt.WorktreePath
	}

	// Insert the coordinator role. If this fails we log and return —
	// the argus task is orphaned (visible as a freelancer) but we do NOT
	// delete it (same Risks as SpawnWorker).
	role, err := s.DB.CreateRole(ctx, CreateRoleInput{
		OrchestratorID: orch.ID,
		Name:           "coord",
		Kind:           KindCoordinator,
		ArgusProject:   project,
		Prompt:         in.Prompt,
	})
	if err != nil {
		s.Logger.Printf("ops.NewOrchestrator: CreateRole failed for argus task %s — task is orphaned: %v", task.ID, err)
		return nil, fmt.Errorf("ops.NewOrchestrator: insert coordinator role: %w", err)
	}

	// Insert the binding. Same orphan-tolerance as above.
	if _, err := s.DB.CreateBinding(ctx, CreateBindingInput{
		RoleID:         role.ID,
		OrchestratorID: orch.ID,
		ArgusTaskID:    task.ID,
		WorktreePath:   worktreePath,
	}); err != nil {
		s.Logger.Printf("ops.NewOrchestrator: CreateBinding failed for role %d / argus task %s — partial state: %v", role.ID, task.ID, err)
		return nil, fmt.Errorf("ops.NewOrchestrator: insert binding: %w", err)
	}

	return &NewOrchestratorResult{
		OrchestratorID: orch.ID,
		RoleID:         role.ID,
		ArgusTaskID:    task.ID,
	}, nil
}

// buildCoordPrompt assembles the task prompt for a born-bound coordinator:
// a short orientation note followed by the operator's instructions.
func buildCoordPrompt(orchName, userPrompt string) string {
	prefix := fmt.Sprintf(
		"You are the coordinator for hera orchestrator %q. You may use hera_* MCP tools to manage workers.",
		orchName,
	)
	if userPrompt != "" {
		return prefix + "\n\n" + userPrompt
	}
	return prefix
}
