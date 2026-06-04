package db

import "time"

// RoleKind enumerates the valid kinds for a role row.
type RoleKind string

const (
	KindCoordinator RoleKind = "coordinator"
	KindWorker      RoleKind = "worker"
	KindFreelance   RoleKind = "freelance"
)

// RoleStatusValue enumerates the valid status strings.
type RoleStatusValue string

const (
	StatusIdle    RoleStatusValue = "idle"
	StatusWorking RoleStatusValue = "working"
	StatusBlocked RoleStatusValue = "blocked"
	StatusDone    RoleStatusValue = "done"
)

// DeliveryMode enumerates how a message was (or was not) delivered to the
// recipient's PTY.
type DeliveryMode string

const (
	DeliveryPending         DeliveryMode = "pending"
	DeliveryIdleSubmit      DeliveryMode = "idle_submit"
	DeliveryBusyBuffer      DeliveryMode = "busy_buffer"
	DeliveryQueuedNoBinding DeliveryMode = "queued_no_binding"
)

// Orchestrator is one coordination group. ArchivedAt is non-nil for
// archived orchestrators; PinnedAt is non-nil for pinned orchestrators.
// Pin and archive are mutually exclusive (a pinned row has ArchivedAt nil
// and vice versa), enforced by the Pin/Unpin/Archive DAO verbs.
type Orchestrator struct {
	ID         int64
	Name       string
	CreatedAt  time.Time
	ArchivedAt *time.Time
	PinnedAt   *time.Time
}

// Role is a participant in an orchestrator. Mission and Constraints are
// optional; both default to empty strings. ArchivedAt is non-nil for
// archived roles.
type Role struct {
	ID             int64
	OrchestratorID int64
	Name           string
	Kind           RoleKind
	ArgusProject   string
	Mission        string
	Constraints    string
	CreatedAt      time.Time
	ArchivedAt     *time.Time
	PinnedAt       *time.Time
}

// Binding is one (role, argus task) incarnation. OrchestratorID is
// denormalized from the role's orchestrator so the bindings table can
// enforce per-orchestrator uniqueness on (argus_task_id, orchestrator_id)
// and (worktree_path, orchestrator_id) directly.
type Binding struct {
	ID             int64
	RoleID         int64
	OrchestratorID int64
	ArgusTaskID    string
	WorktreePath   string
	StartedAt      time.Time
	EndedAt        *time.Time
	EndReason      string
}

// Message is one hera-bus message.
type Message struct {
	ID           int64
	FromRoleID   int64
	ToRoleID     int64
	Body         string
	InReplyTo    *int64
	SentAt       time.Time
	ReadAt       *time.Time
	DeliveryMode DeliveryMode
	DeliveredAt  *time.Time
}

// RoleStatus is the current status for a role.
type RoleStatus struct {
	RoleID    int64
	Status    RoleStatusValue
	UpdatedAt time.Time
}
