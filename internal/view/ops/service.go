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

	GetRoleByID(ctx context.Context, id int64) (*Role, error)
	ListRolesByOrchestrator(ctx context.Context, orchID int64) ([]*Role, error)

	// ListRolesByOrchestratorInclusive returns every role under an
	// orchestrator INCLUDING archived rows. Backs the unarchive branch
	// of the `a` toggle, which must locate the archived coord role.
	ListRolesByOrchestratorInclusive(ctx context.Context, orchID int64) ([]*Role, error)
	ArchiveRole(ctx context.Context, id int64) error
	UnarchiveRole(ctx context.Context, id int64) error
	RenameRole(ctx context.Context, id int64, newName string) error

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
}

// CreateTaskRequest is the ops layer's neutral description of an argus
// task spawn. The adapter (Stage J) translates this to argus.CreateTaskInput.
type CreateTaskRequest struct {
	Project string
	Name    string
	Prompt  string
	Meta    map[string]string
}

// CreatedTask is the ops layer's view of argus's create-task response.
type CreatedTask struct {
	ID   string
	Name string
}

// ArgusClient is the subset of internal/argus.Client the ops layer
// needs. Tests substitute a fake.
type ArgusClient interface {
	CreateTask(ctx context.Context, req CreateTaskRequest) (*CreatedTask, error)
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
