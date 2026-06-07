package ops

import "context"

// DB is the subset of hera's persistence layer the ops package needs.
// The production adapter (wired in daemon startup, Stage J) wraps the
// concrete *db.DB DAOs. Tests substitute a fake.
//
// Methods returning *Orchestrator / *Role return ErrNotFound when the
// row is absent. List* methods return the active (non-archived) set;
// archived rows are accessed by primary key via GetOrchestratorByID /
// GetRoleByID, which do not filter on archived_at.
type DB interface {
	GetOrchestratorByID(ctx context.Context, id int64) (*Orchestrator, error)
	GetOrchestratorByName(ctx context.Context, name string) (*Orchestrator, error)
	ListOrchestrators(ctx context.Context) ([]*Orchestrator, error)
	ArchiveOrchestrator(ctx context.Context, id int64) error
	UnarchiveOrchestrator(ctx context.Context, id int64) error
	RenameOrchestrator(ctx context.Context, id int64, newName string) error

	// PinOrchestrator sets pinned_at AND clears archived_at (mutually
	// exclusive); UnpinOrchestrator clears pinned_at. Backs the `P` toggle.
	PinOrchestrator(ctx context.Context, id int64) error
	UnpinOrchestrator(ctx context.Context, id int64) error

	GetRoleByID(ctx context.Context, id int64) (*Role, error)
	ListRolesByOrchestrator(ctx context.Context, orchID int64) ([]*Role, error)

	// CreateRole inserts a new active role under the given orchestrator.
	// Returns the existing active row if (orchestrator_id, name) already
	// exists with the same kind (the DAO is idempotent on same-kind
	// collisions). Callers that need a guaranteed-new row MUST pre-unique
	// the name before calling (SpawnWorker does this; J-adopt de-collides
	// via GetRoleByOrchestratorAndName).
	CreateRole(ctx context.Context, in CreateRoleInput) (*Role, error)

	// GetRoleByOrchestratorAndName returns the ACTIVE role with the given
	// (orchestrator, name) or ErrNotFound. Backs the de-collision of the
	// default adopt role name (archived siblings do not block).
	GetRoleByOrchestratorAndName(ctx context.Context, orchID int64, name string) (*Role, error)

	// CreateBinding inserts a new live binding row. Bindings are write-once
	// on worktree_path — callers MUST pass the resolved path at insert time.
	CreateBinding(ctx context.Context, in CreateBindingInput) (*Binding, error)

	// ListRolesByOrchestratorInclusive returns every role under an
	// orchestrator INCLUDING archived rows. Backs the unarchive branch
	// of the `a` toggle, which must locate the archived coord role.
	ListRolesByOrchestratorInclusive(ctx context.Context, orchID int64) ([]*Role, error)
	ArchiveRole(ctx context.Context, id int64) error
	UnarchiveRole(ctx context.Context, id int64) error
	RenameRole(ctx context.Context, id int64, newName string) error

	// PinRole sets pinned_at AND clears archived_at (mutually exclusive);
	// UnpinRole clears pinned_at. Backs the `P` toggle on an agent row.
	PinRole(ctx context.Context, id int64) error
	UnpinRole(ctx context.Context, id int64) error

	// GetLiveBindingByRole returns the live binding for a role or
	// ErrNotFound if the role currently has no live binding.
	GetLiveBindingByRole(ctx context.Context, roleID int64) (*Binding, error)

	// GetLatestBindingByRole returns the role's most recent binding (by
	// started_at) regardless of whether it has ended, or ErrNotFound if
	// the role has never had a binding. Backs the archived-row fallback:
	// archiving a task ENDS its binding (end_reason='argus_archived')
	// while preserving the argus_task_id, so live-only lookups miss
	// exactly the rows whose status/unarchive ops still need the task.
	GetLatestBindingByRole(ctx context.Context, roleID int64) (*Binding, error)

	// EndBinding marks a binding as ended with the given reason.
	EndBinding(ctx context.Context, bindingID int64, reason string) error

	// ListLiveBindings returns every live (non-ended) binding across all
	// orchestrators and roles. Backs `^r` prune-completed, which inspects
	// each bound task's argus status to find the completed fleet-wide.
	ListLiveBindings(ctx context.Context) ([]*Binding, error)

	// ListLiveBindingsByTask returns every live (ended_at NULL) binding
	// for an argus task id, across all roles and orchestrators (empty
	// slice if none). Backs ArchiveRole's shared-task guard: a task id
	// resolved via an ENDED binding may ALSO be the live-bound task of a
	// different role, and the archive cascade must not reach through the
	// stale binding and archive a task an active role depends on.
	ListLiveBindingsByTask(ctx context.Context, argusTaskID string) ([]*Binding, error)

	// UpsertRoleStatus sets the hera role's thread_status. Backs
	// MarkRoleDone (the `s` → done → confirm-no path in hera-view).
	UpsertRoleStatus(ctx context.Context, roleID int64, status string) error
}

// CreateTaskRequest is the ops layer's neutral description of an argus
// task spawn. The adapter (Stage J) translates this to argus.CreateTaskInput.
type CreateTaskRequest struct {
	Project string
	Name    string
	Prompt  string
	Meta    map[string]string
	// Branch is optional; an empty value uses the project's default branch.
	Branch string
	// Backend is optional; an empty value uses the project's default backend.
	Backend string
}

// CreatedTask is the ops layer's view of argus's create-task response.
type CreatedTask struct {
	ID   string
	Name string
}

// TaskDetails is the ops layer's view of an argus task, carrying the
// fields SpawnWorker needs after creation (primarily WorktreePath, which
// is required for the binding insert and unavailable from CreatedTask).
type TaskDetails struct {
	ID           string
	WorktreePath string
	// Idle mirrors argus's idle flag (the agent is waiting for input).
	Idle bool
}

// ArgusClient is the subset of internal/argus.Client the ops layer
// needs. Tests substitute a fake.
//
// Contract: ArchiveTask, UnarchiveTask, GetTaskStatus, and SetTaskStatus
// return an error wrapping ErrArgusTaskGone when argus reports the task no
// longer exists (HTTP 404 — argus prunes tasks by deleting them outright).
// The production adapter translates the typed argus 404 to the sentinel;
// the ops verbs decide per-operation whether that is a skip (archive /
// unarchive — nothing left to flip argus-side) or a friendly error
// (status stepping).
type ArgusClient interface {
	CreateTask(ctx context.Context, req CreateTaskRequest) (*CreatedTask, error)

	// GetTask fetches the full task record, including WorktreePath. Used by
	// SpawnWorker to read the created task's worktree path so the binding
	// can be populated at insert time. argus creates the worktree
	// synchronously inside POST /api/tasks, so GetTask immediately after
	// CreateTask returns a populated WorktreePath.
	GetTask(ctx context.Context, taskID string) (*TaskDetails, error)

	ArchiveTask(ctx context.Context, taskID string) error
	UnarchiveTask(ctx context.Context, taskID string) error

	// DeleteTask destroys an argus task, INCLUDING its git worktree and
	// branch (argus cleans both server-side). Backs `^d` delete and `^r`
	// prune. A 404 (task already gone) is treated as success by the client,
	// so a sibling already deleted out-of-band can't abort a cascade.
	DeleteTask(ctx context.Context, taskID string) error

	// GetTaskStatus returns the task's current workflow status string
	// ("pending" | "in_progress" | "in_review" | "complete"). Used by
	// `s`/`S` to compute the next/prev step and by `^r` to identify
	// completed agents.
	GetTaskStatus(ctx context.Context, taskID string) (string, error)

	// SetTaskStatus sets the task's workflow status, returning the
	// resolved status. Backs `s`/`S`.
	SetTaskStatus(ctx context.Context, taskID, status string) (string, error)

	// PutTaskMeta sets a meta key on an argus task. Backs the best-effort
	// meta:role=worker stamp on operator-side adoption (mirroring
	// hera_join attach-mode). A failure here is non-fatal to the binding.
	PutTaskMeta(ctx context.Context, taskID, key, value string) error

	// PostTaskInput writes raw bytes to a task's PTY input. Used by
	// NewOrchestrator to send an auto-submit CR after task creation so the
	// bootstrap prompt executes without manual confirmation. Soft-fail: a
	// failure is logged but does not propagate as a NewOrchestrator error.
	PostTaskInput(ctx context.Context, taskID string, bytes []byte) (int, error)

	// RestartTask asks argus to restart the agent session for a task whose
	// previous session has ended (the PTY exited). Argus re-spawns the agent
	// backend (e.g. claude --resume <last-session-id>) and routes its output
	// through the same task stream. Returns ErrNoTaskRestart when the daemon
	// does not support the endpoint.
	RestartTask(ctx context.Context, taskID string) error

	// ListProjects returns the names of every configured argus project.
	// Used to populate the Project dropdown in the new-coordinator form.
	ListProjects(ctx context.Context) ([]string, error)

	// ListBackends returns the names of every configured argus backend.
	// Used to populate the Backend dropdown in the new-coordinator form.
	ListBackends(ctx context.Context) ([]string, error)
}

// PRCreator opens a pull request for a role's worktree. The production
// implementation shells out (`gh pr create`) from the worktree path via
// os/exec — the hera daemon is unsandboxed under launchd, so it reaches the
// host git + gh, mirroring the WorktreeRemover precedent. Tests substitute a
// fake that records calls. Opening a real PR headless is not feasible in a
// unit test, so `^p` is validated against a fake; the exec implementation is
// covered by a construction smoke test only (see ops/pr.go).
type PRCreator interface {
	// CreatePR opens a PR for the worktree at path. Returns the PR URL (or
	// empty when the implementation cannot report one) and any error.
	CreatePR(ctx context.Context, worktreePath string) (string, error)
}

// WorktreeRemover deletes a git worktree directory. In production this is
// `git worktree remove --force <path>` invoked via os/exec by the
// unsandboxed hera daemon. Tests substitute a fake that records calls.
//
// Remove returns nil for soft no-ops (empty path or missing directory).
// Non-nil errors propagate to the caller — the daemon logs them but does
// not unwind partially-completed cascades (per design.md D5).
type WorktreeRemover interface {
	Remove(ctx context.Context, worktreePath string) error
}

// Logger is the smallest interface the ops package needs to audit-log
// destructive operations (every git worktree remove invocation is
// logged with the path per design.md Risks).
type Logger interface {
	Printf(format string, args ...any)
}

// Service is the entry point for rail-level mutation operations. One
// instance is constructed per WebSocket session by the view server.
type Service struct {
	DB              DB
	Argus           ArgusClient
	WorktreeRemover WorktreeRemover
	Logger          Logger
	ListAll         *ListAllState

	// PR opens pull requests for `^p`. May be nil — OpenPR then returns an
	// error rather than panicking (tests / daemon startup without a PR
	// flow wired).
	PR PRCreator
}

// NewService constructs a Service. ListAll may be nil — a fresh
// in-memory state is allocated in that case (since visibility resets
// per WebSocket session per design.md D5 "l listall").
func NewService(db DB, argus ArgusClient, wr WorktreeRemover, logger Logger) *Service {
	return &Service{
		DB:              db,
		Argus:           argus,
		WorktreeRemover: wr,
		Logger:          logger,
		ListAll:         NewListAllState(),
	}
}

// ListProjects returns the names of every configured argus project.
// Delegates straight to the ArgusClient.
func (s *Service) ListProjects(ctx context.Context) ([]string, error) {
	return s.Argus.ListProjects(ctx)
}

// ListBackends returns the names of every configured argus backend.
// Delegates straight to the ArgusClient.
func (s *Service) ListBackends(ctx context.Context) ([]string, error) {
	return s.Argus.ListBackends(ctx)
}
