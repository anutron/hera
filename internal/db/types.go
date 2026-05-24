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
	DeliveryUserInbox       DeliveryMode = "user_inbox"
	DeliveryQueuedNoBinding DeliveryMode = "queued_no_binding"
)

// ToKind distinguishes role-addressed messages from user-pseudo-recipient
// messages.
type ToKind string

const (
	ToKindRole ToKind = "role"
	ToKindUser ToKind = "user"
)

// Orchestrator is one coordination group.
type Orchestrator struct {
	ID        int64
	Name      string
	CreatedAt time.Time
}

// Role is a participant in an orchestrator. Mission and Constraints are
// optional; both default to empty strings.
type Role struct {
	ID             int64
	OrchestratorID int64
	Name           string
	Kind           RoleKind
	ArgusProject   string
	Mission        string
	Constraints    string
	CreatedAt      time.Time
}

// Binding is one (role, argus task) incarnation.
type Binding struct {
	ID           int64
	RoleID       int64
	ArgusTaskID  string
	WorktreePath string
	StartedAt    time.Time
	EndedAt      *time.Time
	EndReason    string
}

// Message is one ludwig-bus message. ToRoleID is nil when ToKind=user.
type Message struct {
	ID           int64
	FromRoleID   int64
	ToRoleID     *int64
	ToKind       ToKind
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
