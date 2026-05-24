package events

import (
	"encoding/json"

	"github.com/anutron/hera/internal/argus"
)

// LinkCreatedPayload is the parsed payload of a link.created event.
type LinkCreatedPayload struct {
	Child  string `json:"child"`
	Parent string `json:"parent"`
}

// ParseLinkCreated decodes a link.created event's payload.
func ParseLinkCreated(ev argus.Event) (LinkCreatedPayload, error) {
	var p LinkCreatedPayload
	err := json.Unmarshal(ev.Payload, &p)
	return p, err
}

// TaskStatusChangedPayload is the parsed payload of a task.status_changed
// event.
type TaskStatusChangedPayload struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// ParseTaskStatusChanged decodes a task.status_changed event's payload.
func ParseTaskStatusChanged(ev argus.Event) (TaskStatusChangedPayload, error) {
	var p TaskStatusChangedPayload
	err := json.Unmarshal(ev.Payload, &p)
	return p, err
}

// TaskCreatedPayload is the parsed payload of a task.created event.
type TaskCreatedPayload struct {
	Name    string `json:"name"`
	Project string `json:"project"`
	Status  string `json:"status"`
}

// ParseTaskCreated decodes a task.created event's payload.
func ParseTaskCreated(ev argus.Event) (TaskCreatedPayload, error) {
	var p TaskCreatedPayload
	err := json.Unmarshal(ev.Payload, &p)
	return p, err
}

// ResyncPayload is the parsed payload of a resync event.
type ResyncPayload struct {
	Reason string `json:"reason"`
	Cursor int64  `json:"cursor"`
	Oldest int64  `json:"oldest"`
}

// ParseResync decodes a resync event's payload.
func ParseResync(ev argus.Event) (ResyncPayload, error) {
	var p ResyncPayload
	err := json.Unmarshal(ev.Payload, &p)
	return p, err
}
