package ops

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// SpawnWorkerInput is the (validated) input for the `w` rail operation.
type SpawnWorkerInput struct {
	// TargetOrchestratorID is the orchestrator the new worker role lands under.
	// Resolved from the rail selection by the bridge before calling SpawnWorker.
	TargetOrchestratorID int64

	// CoordRoleID is the coordinator role whose argus_project the worker
	// inherits. The op fetches this role to resolve both the project AND the
	// coordinator name used in the orientation prefix — so the prefix is
	// correct regardless of which rail row the caller selected.
	CoordRoleID int64

	// Prompt is the operator's text. Must be non-empty (after trimming).
	Prompt string

	// Project is an optional override for the argus project the worker task
	// and role are created in. When empty (after trimming), the coordinator
	// role's argus_project is used instead (today's behavior is preserved).
	Project string

	// Branch is an optional base ref for the worker's worktree. When empty
	// the worker branches off the effective project's default ref.
	Branch string

	// Backend is an optional argus backend for the worker. When empty the
	// effective project's default backend is used.
	Backend string
}

// SpawnWorker handles the `w` rail operation: validates the prompt,
// derives a unique worker role name from it, creates an argus task in
// the coordinator's argus_project with an orientation prefix, reads the
// task's worktree path, and inserts a worker role + binding so the rail
// can render the new worker immediately.
//
// Design doc D3–D7:
//   - Attachment is fully programmatic (no LLM MCP call required).
//   - Role name derived from prompt, uniqued within non-archived siblings (D5).
//   - Binding worktree path resolved via GetTask set at insert time (D6).
//   - Partial-failure handling: if GetTask fails, binding is still inserted
//     (possibly empty path); if DAO inserts fail after argus CreateTask
//     succeeds, the orphaned argus task is logged but NOT deleted (D7 / Risks).
//
// Returns ErrValidation for user-correctable input. Other errors are
// substrate failures (DB read or argus HTTP).
func (s *Service) SpawnWorker(ctx context.Context, in SpawnWorkerInput) (*SpawnWorkerResult, error) {
	prompt := strings.TrimSpace(in.Prompt)
	if prompt == "" {
		return nil, validation("prompt is required")
	}

	// Resolve the coordinator role to get its argus_project and name.
	coordRole, err := s.DB.GetRoleByID(ctx, in.CoordRoleID)
	if err != nil {
		return nil, fmt.Errorf("ops.SpawnWorker: load coord role %d: %w", in.CoordRoleID, err)
	}

	// Compute the effective project: use the override if provided, otherwise
	// fall back to the coordinator role's argus_project (D3).
	effectiveProject := strings.TrimSpace(in.Project)
	if effectiveProject == "" {
		effectiveProject = coordRole.ArgusProject
	}
	if effectiveProject == "" {
		return nil, fmt.Errorf("ops.SpawnWorker: coord role %d has empty argus_project and no project override provided", in.CoordRoleID)
	}

	// Derive a unique role name from the prompt (D5).
	baseName := deriveWorkerName(prompt)
	uniqueName, err := s.uniqueWorkerName(ctx, in.TargetOrchestratorID, baseName)
	if err != nil {
		return nil, fmt.Errorf("ops.SpawnWorker: derive unique name: %w", err)
	}

	// Build the orientation-suffixed prompt (D4). The coordinator name is
	// sourced from the coord ROLE we just loaded — never the caller-supplied
	// CoordName, which on an agent-row selection is the agent's name, not the
	// coordinator's. The delta scenario "Worker prompt carries an orientation
	// suffix" requires the suffix to name the COORDINATOR regardless of which
	// row was selected.
	taskPrompt := buildWorkerPrompt(coordRole.Name, prompt)

	// Create the argus task in the effective project with worker meta (D3).
	// Branch / Backend are optional: empty Branch lets argus branch off the
	// project's default ref; empty Backend uses the project's default backend.
	req := CreateTaskRequest{
		Project: effectiveProject,
		Prompt:  taskPrompt,
		Meta:    map[string]string{"role": "worker"},
		Branch:  strings.TrimSpace(in.Branch),
		Backend: strings.TrimSpace(in.Backend),
	}
	created, err := s.Argus.CreateTask(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("ops.SpawnWorker: argus CreateTask: %w", err)
	}

	// Read the worktree path via GetTask (D6). Soft-degrade on failure:
	// the binding is still inserted with whatever path we have (possibly
	// empty) so the spawn is not lost. The caller logs prominently.
	worktreePath := ""
	gt, gtErr := s.Argus.GetTask(ctx, created.ID)
	if gtErr != nil {
		s.Logger.Printf("ops.SpawnWorker: GetTask(%s) failed — binding will have empty worktree_path: %v", created.ID, gtErr)
	} else if gt != nil {
		worktreePath = gt.WorktreePath
	}

	// Insert the worker role (D3 step 5). If this fails we log and return —
	// the argus task is orphaned (visible as a freelancer) but we do NOT
	// delete it (Risks section).
	role, err := s.DB.CreateRole(ctx, CreateRoleInput{
		OrchestratorID: in.TargetOrchestratorID,
		Name:           uniqueName,
		Kind:           KindWorker,
		ArgusProject:   effectiveProject,
		Prompt:         prompt,
	})
	if err != nil {
		s.Logger.Printf("ops.SpawnWorker: CreateRole failed for argus task %s — task is orphaned: %v", created.ID, err)
		return nil, fmt.Errorf("ops.SpawnWorker: insert worker role: %w", err)
	}

	// Insert the binding (D3 step 5). Same orphan-tolerance as above.
	if _, err := s.DB.CreateBinding(ctx, CreateBindingInput{
		RoleID:         role.ID,
		OrchestratorID: in.TargetOrchestratorID,
		ArgusTaskID:    created.ID,
		WorktreePath:   worktreePath,
	}); err != nil {
		s.Logger.Printf("ops.SpawnWorker: CreateBinding failed for role %d / argus task %s — partial state: %v", role.ID, created.ID, err)
		return nil, fmt.Errorf("ops.SpawnWorker: insert binding: %w", err)
	}

	return &SpawnWorkerResult{
		RoleID:      role.ID,
		ArgusTaskID: created.ID,
	}, nil
}

// CoordProject loads the coordinator role identified by coordRoleID and
// returns its argus_project. Propagates any DB error so callers can surface
// a load failure as an error modal.
func (s *Service) CoordProject(ctx context.Context, coordRoleID int64) (string, error) {
	role, err := s.DB.GetRoleByID(ctx, coordRoleID)
	if err != nil {
		return "", fmt.Errorf("ops.CoordProject: load role %d: %w", coordRoleID, err)
	}
	return role.ArgusProject, nil
}

// uniqueWorkerName returns baseName if no non-archived role under
// orchestratorID has that name, or baseName-2, baseName-3, … until a free
// slot is found. The search is bounded by the number of existing siblings
// plus a safety cap so a degenerate DB state cannot loop forever.
func (s *Service) uniqueWorkerName(ctx context.Context, orchID int64, baseName string) (string, error) {
	roles, err := s.DB.ListRolesByOrchestrator(ctx, orchID)
	if err != nil {
		return "", fmt.Errorf("uniqueWorkerName: list roles: %w", err)
	}
	used := make(map[string]bool, len(roles))
	for _, r := range roles {
		used[r.Name] = true
	}
	if !used[baseName] {
		return baseName, nil
	}
	for i := 2; i <= len(roles)+2; i++ {
		candidate := fmt.Sprintf("%s-%d", baseName, i)
		if !used[candidate] {
			return candidate, nil
		}
	}
	// Fallback: append a raw count beyond the loop bound (should never reach
	// here in practice).
	return fmt.Sprintf("%s-%d", baseName, len(roles)+3), nil
}

// workerNameRe matches characters that belong in a slug: ASCII letters,
// digits, and hyphens. Everything else is treated as a word boundary or
// dropped.
var workerNameRe = regexp.MustCompile(`[a-z0-9]+`)

// deriveWorkerName produces a URL-slug-style name from the first ~40 chars
// of the prompt, mirroring argus's sanitizeName heuristic (D5). The slug
// is always lowercase. Non-word runs become single hyphens; leading/trailing
// hyphens are stripped. When the result is empty the fallback stem "worker"
// is returned so role names are always non-empty.
func deriveWorkerName(prompt string) string {
	// Work with the first 40 runes only (prompt head).
	runes := []rune(prompt)
	if len(runes) > 40 {
		runes = runes[:40]
	}
	// Lowercase.
	lower := strings.Map(func(r rune) rune {
		return unicode.ToLower(r)
	}, string(runes))

	// Extract word-char tokens and join with hyphens.
	tokens := workerNameRe.FindAllString(lower, -1)
	if len(tokens) == 0 {
		return "worker"
	}
	slug := strings.Join(tokens, "-")
	if slug == "" {
		return "worker"
	}
	return slug
}

// buildWorkerPrompt assembles the task prompt: the operator's prompt text
// verbatim first, followed by a short orientation suffix naming the coordinator
// and noting the worker may report progress via hera_send (D4). User prompt
// leads so argus derives the worktree branch name from the actual task, not the
// orientation boilerplate.
func buildWorkerPrompt(coordName, userPrompt string) string {
	suffix := "You are a worker agent."
	if coordName != "" {
		suffix = fmt.Sprintf("You are a worker agent under coordinator %q.", coordName)
	}
	return fmt.Sprintf(
		"%s\n\n---\n"+`%s You may report progress via hera_send. If this task requires changes to another repo or you need to spawn sub-agents, call hera_new_orchestrator(cwd=$PWD, name="...", coordinator_role_name="coord", prompt="...") to become a sub-coordinator, then use hera_spawn_worker(project="TARGET-PROJECT", ...) to dispatch workers in that project. When opening pull requests, use mcp__argus__iris_gh_pr_create (not gh pr create directly) so argus records the PR URL and the hera rail shows the PR indicator.`,
		userPrompt, suffix,
	)
}
