package events

// Canonical argus event type strings. Kept as constants so the rest of
// hera refers to them symbolically (renames in argus become compile
// errors here, not runtime mismatches).
//
// Source of truth: argus/internal/model/event.go and the table in
// argus/docs/plugins.md.
const (
	TypeTaskCreated       = "task.created"
	TypeTaskStatusChanged = "task.status_changed"
	TypeTaskCompleted     = "task.completed"
	TypeTaskArchived      = "task.archived"
	TypeTaskDeleted       = "task.deleted"
	TypeTaskRenamed       = "task.renamed"
	TypeTaskForked        = "task.forked"

	TypeMessageSent  = "message.sent"
	TypeMessageAcked = "message.acked"

	TypeLinkCreated = "link.created"
	TypeLinkRemoved = "link.removed"

	TypeSessionStarted = "session.started"
	TypeSessionExited  = "session.exited"
	TypeSessionIdle    = "session.idle"

	TypeResync = "resync"
)

// Meta keys hera reads from argus task metadata.
const (
	MetaKeyRole         = "role"
	MetaKeyPrompt       = "prompt"
	MetaKeyThreadStatus = "thread_status"

	// Namespace used when fetching task meta. Hera's writes are
	// auto-namespaced server-side per substrate gap-1 fix.
	MetaNamespace = "hera"
)
