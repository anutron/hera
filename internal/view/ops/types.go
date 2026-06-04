// Package ops implements the rail-level mutation operations the hera-view
// surface exposes: new project (n), rename (r), delete (^d), archive (a),
// listall (l), help (?), and resurrect (Enter on archived coord).
//
// Operations are pure logic — modal presentation and key routing live in
// the surrounding tview app (Stages F + G). Each op is a method on
// Service which receives narrow interfaces (DB, ArgusClient,
// WorktreeRemover, Logger) so tests can substitute fakes and the
// production daemon wires the real DAOs + argus HTTP client (Stage J).
package ops

// RoleKind enumerates the kinds the ops layer cares about. Mirrors
// db.RoleKind by value so adapters can flow strings straight through.
type RoleKind string

const (
	KindCoordinator RoleKind = "coordinator"
	KindWorker      RoleKind = "worker"
	KindFreelance   RoleKind = "freelance"
)

// Orchestrator is the ops-layer view of one orchestrator row. Archived
// is true when the underlying row has archived_at set.
type Orchestrator struct {
	ID       int64
	Name     string
	Archived bool
}

// Role is the ops-layer view of one role row.
type Role struct {
	ID             int64
	OrchestratorID int64
	Name           string
	Kind           RoleKind
	ArgusProject   string
	Mission        string
	Constraints    string
	Archived       bool
}

// Binding is the ops-layer view of one binding row. Only the fields the
// ops layer needs are exposed; if more are needed later add them here.
type Binding struct {
	ID           int64
	RoleID       int64
	ArgusTaskID  string
	WorktreePath string
}

// CreateRoleInput is the ops-layer input for inserting a new role.
// Mirrors db.CreateRoleInput without importing internal/db.
type CreateRoleInput struct {
	OrchestratorID int64
	Name           string
	Kind           RoleKind
	ArgusProject   string
	Mission        string
	Constraints    string
}

// CreateBindingInput is the ops-layer input for inserting a new binding.
// Mirrors db.CreateBindingInput without importing internal/db.
type CreateBindingInput struct {
	RoleID         int64
	OrchestratorID int64
	ArgusTaskID    string
	WorktreePath   string
}

// SpawnWorkerResult carries the identifiers of the role and argus task
// created by SpawnWorker. The bridge uses them for auto-selection.
type SpawnWorkerResult struct {
	// RoleID is the new worker role's database id. Used by the bridge to
	// call SelectByRoleID after the broadcaster-driven rail repopulate.
	RoleID int64
	// ArgusTaskID is the created argus task's id (as returned by
	// argus POST /api/tasks).
	ArgusTaskID string
}
