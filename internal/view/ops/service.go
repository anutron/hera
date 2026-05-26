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
	ArchiveRole(ctx context.Context, id int64) error
	UnarchiveRole(ctx context.Context, id int64) error
	RenameRole(ctx context.Context, id int64, newName string) error

	// GetLiveBindingByRole returns the live binding for a role or
	// ErrNotFound if the role currently has no live binding.
	GetLiveBindingByRole(ctx context.Context, roleID int64) (*Binding, error)

	// EndBinding marks a binding as ended with the given reason.
	EndBinding(ctx context.Context, bindingID int64, reason string) error
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
