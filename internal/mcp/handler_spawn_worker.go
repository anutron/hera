package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"unicode"

	"github.com/anutron/hera/internal/argus"
	"github.com/anutron/hera/internal/db"
	"github.com/anutron/hera/internal/events"
)

// SpawnWorkerHandler implements the hera_spawn_worker MCP tool.
//
// Coordinator-only: the calling task must hold a live coordinator binding.
// The handler creates an argus task in the coordinator's project, inserts a
// worker role + binding pre-bound under the same orchestrator, and
// auto-submits the prompt via PostTaskInput("\r") so the worker starts
// without a manual Enter (reuses the BUG-030 fix).
//
// Partial-failure contract mirrors ops.SpawnWorker:
//   - GetTask failure: binding inserted with empty worktree_path; non-fatal.
//   - PutTaskMeta failure: non-fatal.
//   - PostTaskInput failure: non-fatal; prompt_auto_submitted=false in response.
//   - CreateRole/CreateBinding failure after CreateTask: argus task orphaned; error returned.
type SpawnWorkerHandler struct {
	resolver *Resolver
	db       *db.DB
	client   *argus.Client
}

// NewSpawnWorkerHandler constructs a SpawnWorkerHandler.
func NewSpawnWorkerHandler(r *Resolver, database *db.DB, client *argus.Client) *SpawnWorkerHandler {
	return &SpawnWorkerHandler{resolver: r, db: database, client: client}
}

// SpawnWorkerInput is the tool's input schema.
type SpawnWorkerInput struct {
	Cwd          string `json:"cwd"`
	Orchestrator string `json:"orchestrator,omitempty"`
	RoleName     string `json:"role_name,omitempty"`
	Mission      string `json:"mission,omitempty"`
	Prompt       string `json:"prompt"`
	Project      string `json:"project,omitempty"`
	Branch       string `json:"branch,omitempty"`
	Backend      string `json:"backend,omitempty"`
}

// SpawnWorkerOutput is the success payload.
type SpawnWorkerOutput struct {
	Orchestrator        string `json:"orchestrator"`
	RoleName            string `json:"role_name"`
	Kind                string `json:"kind"`
	Mission             string `json:"mission"`
	BindingID           int64  `json:"binding_id"`
	ArgusTaskID         string `json:"argus_task_id"`
	PromptAutoSubmitted bool   `json:"prompt_auto_submitted"`
}

// Handle implements Handler.
func (h *SpawnWorkerHandler) Handle(ctx context.Context, raw json.RawMessage) Response {
	if resp, gated := LinkGate(); gated {
		return resp
	}
	var in SpawnWorkerInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return ErrorResponse("hera_spawn_worker: invalid input JSON: " + err.Error())
	}
	if in.Cwd == "" {
		return ErrorResponse("hera_spawn_worker: cwd is required")
	}
	prompt := strings.TrimSpace(in.Prompt)
	if prompt == "" {
		return ErrorResponse("hera_spawn_worker: prompt is required")
	}

	_, role, _, err := h.resolver.CallerRole(ctx, in.Cwd, in.Orchestrator)
	if err != nil {
		return ErrorResponse("hera_spawn_worker: " + err.Error())
	}
	if role.Kind != db.KindCoordinator {
		return ErrorResponse(fmt.Sprintf(
			"hera_spawn_worker: caller role %q has kind %q; only coordinators may spawn workers",
			role.Name, role.Kind,
		))
	}

	// Determine project: input override first, then coordinator's own project.
	project := strings.TrimSpace(in.Project)
	if project == "" {
		project = role.ArgusProject
	}
	if project == "" {
		return ErrorResponse("hera_spawn_worker: no project resolved (coordinator has no argus_project and none was supplied)")
	}

	// Load orchestrator for the name used in orientation prefix.
	orch, err := h.db.Orchestrators.GetByID(ctx, role.OrchestratorID)
	if err != nil {
		return ErrorResponse("hera_spawn_worker: load orchestrator: " + err.Error())
	}

	// Derive a unique worker role name.
	baseName := in.RoleName
	if baseName == "" {
		baseName = swDeriveWorkerName(prompt)
	}
	uniqueName, err := h.swUniqueWorkerName(ctx, role.OrchestratorID, baseName)
	if err != nil {
		return ErrorResponse("hera_spawn_worker: derive unique name: " + err.Error())
	}

	// Prepend orientation prefix to the prompt (matches ops.buildWorkerPrompt).
	taskPrompt := fmt.Sprintf(
		"You are a worker agent under coordinator %q. You may report progress via hera_send.\n\n%s",
		role.Name, prompt,
	)

	// Create the argus task.
	created, err := h.client.CreateTask(ctx, argus.CreateTaskInput{
		Project: project,
		Prompt:  taskPrompt,
		Branch:  in.Branch,
		Backend: in.Backend,
	}, map[string]string{"role": "worker"})
	if err != nil {
		return ErrorResponse("hera_spawn_worker: create argus task: " + err.Error())
	}

	// Resolve worktree path. Soft-fail: binding is still inserted with empty path.
	worktreePath := ""
	if gt, gtErr := h.client.GetTask(ctx, created.ID); gtErr != nil {
		slog.Default().Warn("hera_spawn_worker: GetTask failed — binding will have empty worktree_path",
			"task_id", created.ID, "err", gtErr)
	} else if gt != nil {
		worktreePath = gt.WorktreePath
	}

	// Insert worker role.
	workerRole, err := h.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: role.OrchestratorID,
		Name:           uniqueName,
		Kind:           db.KindWorker,
		ArgusProject:   project,
		Mission:        in.Mission,
	})
	if err != nil {
		slog.Default().Warn("hera_spawn_worker: CreateRole failed — argus task orphaned",
			"task_id", created.ID, "err", err)
		return ErrorResponse("hera_spawn_worker: insert worker role: " + err.Error())
	}

	// Insert binding.
	bnd, err := h.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID:         workerRole.ID,
		OrchestratorID: role.OrchestratorID,
		ArgusTaskID:    created.ID,
		WorktreePath:   worktreePath,
	})
	if err != nil {
		slog.Default().Warn("hera_spawn_worker: CreateBinding failed — partial state",
			"role_id", workerRole.ID, "task_id", created.ID, "err", err)
		return ErrorResponse("hera_spawn_worker: insert binding: " + err.Error())
	}

	// Mirror meta:hera.role to the argus task. Best-effort.
	_ = h.client.PutTaskMeta(ctx, created.ID, events.MetaKeyRole, string(db.KindWorker))

	// Auto-submit the prompt via CR (BUG-030 fix reused). Best-effort.
	autoSubmitted := false
	if _, inputErr := h.client.PostTaskInput(ctx, created.ID, []byte("\r")); inputErr != nil {
		slog.Default().Warn("hera_spawn_worker: PostTaskInput CR failed — prompt not auto-submitted",
			"task_id", created.ID, "err", inputErr)
	} else {
		autoSubmitted = true
	}

	return jsonText(SpawnWorkerOutput{
		Orchestrator:        orch.Name,
		RoleName:            workerRole.Name,
		Kind:                string(workerRole.Kind),
		Mission:             workerRole.Mission,
		BindingID:           bnd.ID,
		ArgusTaskID:         created.ID,
		PromptAutoSubmitted: autoSubmitted,
	})
}

// swWorkerNameRe matches ASCII lowercase letters, digits, and hyphens.
var swWorkerNameRe = regexp.MustCompile(`[a-z0-9]+`)

// swDeriveWorkerName produces a URL-slug-style name from the first 40 chars
// of the prompt, mirroring ops.deriveWorkerName. Returns "worker" for empty input.
func swDeriveWorkerName(prompt string) string {
	runes := []rune(prompt)
	if len(runes) > 40 {
		runes = runes[:40]
	}
	lower := strings.Map(func(r rune) rune { return unicode.ToLower(r) }, string(runes))
	tokens := swWorkerNameRe.FindAllString(lower, -1)
	if len(tokens) == 0 {
		return "worker"
	}
	slug := strings.Join(tokens, "-")
	if slug == "" {
		return "worker"
	}
	return slug
}

// swUniqueWorkerName returns baseName if no non-archived role under
// orchestratorID has that name, or baseName-2, baseName-3, … until a free
// slot is found. Mirrors ops.uniqueWorkerName.
func (h *SpawnWorkerHandler) swUniqueWorkerName(ctx context.Context, orchID int64, baseName string) (string, error) {
	roles, err := h.db.Roles.ListByOrchestrator(ctx, orchID)
	if err != nil {
		return "", fmt.Errorf("swUniqueWorkerName: list roles: %w", err)
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
	return fmt.Sprintf("%s-%d", baseName, len(roles)+3), nil
}
