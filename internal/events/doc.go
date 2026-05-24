// Package events subscribes to argus's SSE event stream, persists the cursor
// across daemon restarts, and dispatches events to typed handlers.
//
// The auto-adopt handler enforces the stricter rule from the spec: a new
// argus task is adopted as a worker only when (a) link.created names a
// parent currently bound to a hera coordinator role AND (b) the new task
// has meta:hera.role=worker.
//
// See openspec/changes/hera-v1/design.md decision D4.
package events
