package ops

import (
	"context"
	"errors"
	"fmt"

	"github.com/anutron/hera/internal/argus"
)

// ErrRestartNotSupported is returned by ReattachAgent when the argus daemon
// does not support POST /api/tasks/{id}/restart. The caller surfaces a
// human-readable message so the operator knows to update argus rather than
// seeing an opaque HTTP error.
var ErrRestartNotSupported = errors.New("argus restart not supported — update argus to enable reattach")

// ReattachAgent asks argus to restart the agent session for a dead-session
// task (one whose previous PTY session has ended but whose record still
// exists). Argus re-spawns the agent backend with the last session ID so the
// conversation history is preserved, and routes the new session's PTY output
// through the same task output stream — hera's proxy subscription picks it up
// automatically via the normal reconnect backoff loop.
//
// Returns ErrRestartNotSupported when the daemon does not implement the
// endpoint (pre-restart-API argus). Other errors propagate as-is.
func (s *Service) ReattachAgent(ctx context.Context, argusTaskID string) error {
	if argusTaskID == "" {
		return fmt.Errorf("ops.ReattachAgent: task id is required")
	}
	err := s.Argus.RestartTask(ctx, argusTaskID)
	if err == nil {
		return nil
	}
	if errors.Is(err, argus.ErrNoTaskRestart) {
		return ErrRestartNotSupported
	}
	// BUG-020: argus reports "worktree path missing" when the task's worktree
	// was deleted out-of-band. The task is an unrecoverable orphan — surface the
	// typed sentinel so the view can offer a delete recovery path. The raw argus
	// detail is preserved in the chain for logs.
	if argus.IsWorktreeMissing(err) {
		return fmt.Errorf("%w (%v)", ErrWorktreeMissing, err)
	}
	return fmt.Errorf("ops.ReattachAgent: %w", err)
}
