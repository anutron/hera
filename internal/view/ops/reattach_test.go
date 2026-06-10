package ops

import (
	"context"
	"errors"
	"testing"

	"github.com/anutron/hera/internal/argus"
)

// ReattachAgent delegates to argus.RestartTask and returns nil on success.
func TestReattachAgent_Success(t *testing.T) {
	svc, _, a, _, _ := newTestService()
	err := svc.ReattachAgent(context.Background(), "task-123")
	if err != nil {
		t.Fatalf("ReattachAgent on success must return nil; got %v", err)
	}
	if len(a.restartCalls) != 1 || a.restartCalls[0] != "task-123" {
		t.Fatalf("want RestartTask(task-123); got %v", a.restartCalls)
	}
}

// When argus returns ErrNoTaskRestart the ops layer surfaces ErrRestartNotSupported.
func TestReattachAgent_NoEndpoint_SurfacesNotSupported(t *testing.T) {
	svc, _, a, _, _ := newTestService()
	a.restartErr = argus.ErrNoTaskRestart

	err := svc.ReattachAgent(context.Background(), "task-123")
	if !errors.Is(err, ErrRestartNotSupported) {
		t.Fatalf("want ErrRestartNotSupported; got %v", err)
	}
}

// An empty task id is rejected without calling argus.
func TestReattachAgent_EmptyTaskID_ReturnsError(t *testing.T) {
	svc, _, a, _, _ := newTestService()

	err := svc.ReattachAgent(context.Background(), "")
	if err == nil {
		t.Fatalf("ReattachAgent with empty task id must return error")
	}
	if len(a.restartCalls) != 0 {
		t.Fatalf("empty task id must not call RestartTask; got %v", a.restartCalls)
	}
}

// BUG-020: when argus reports "worktree path missing" the ops layer surfaces
// the typed ErrWorktreeMissing sentinel so the view can offer a delete path.
func TestReattachAgent_WorktreeMissing_SurfacesSentinel(t *testing.T) {
	svc, _, a, _, _ := newTestService()
	a.restartErr = &argus.HTTPError{
		Method:     "POST",
		Path:       "/api/tasks/task-123/restart",
		StatusCode: 500,
		Body:       `{"error":"worktree path missing: /tmp/gone (delete the task or recreate the worktree)"}`,
	}

	err := svc.ReattachAgent(context.Background(), "task-123")
	if !errors.Is(err, ErrWorktreeMissing) {
		t.Fatalf("want ErrWorktreeMissing; got %v", err)
	}
	if errors.Is(err, ErrRestartNotSupported) {
		t.Fatalf("worktree-missing must not be ErrRestartNotSupported; got %v", err)
	}
}

// Other argus errors propagate as-is (wrapped).
func TestReattachAgent_ArgusError_Propagates(t *testing.T) {
	svc, _, a, _, _ := newTestService()
	a.restartErr = errors.New("argus: connection refused")

	err := svc.ReattachAgent(context.Background(), "task-123")
	if err == nil {
		t.Fatalf("ReattachAgent must propagate argus errors")
	}
	if errors.Is(err, ErrRestartNotSupported) {
		t.Fatalf("connection error must not be ErrRestartNotSupported; got %v", err)
	}
}
