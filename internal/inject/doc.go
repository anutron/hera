// Package inject delivers messages into argus task PTYs.
//
// Inject(ctx, taskID, senderRoleName, body) consults the idle tracker, picks
// idle-submit (body + "\n") or busy-buffer (body, no "\n"), and POSTs to
// argus's /api/tasks/{id}/input endpoint.
//
// The formatted body always carries the prefix
// "[hera from <senderRoleName>] " so the recipient agent can identify
// the source of the message.
//
// See openspec/changes/hera-v1/design.md decision D3.
package inject
