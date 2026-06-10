package argus

import (
	"errors"
	"fmt"
	"strings"
)

// ErrNoTaskSize is returned by GetTaskSize when argus reports 404 — the
// task exists but no PTY session is active, so no size is available. The
// caller treats this as a signal to fall back to a default surface size.
var ErrNoTaskSize = errors.New("argus: no PTY size for task")

// ErrNoTaskRestart is returned by RestartTask when argus reports 404 or 405
// — the endpoint does not exist on this daemon version. The caller surfaces
// a human-readable "update argus" message rather than an opaque HTTP error.
var ErrNoTaskRestart = errors.New("argus: restart not supported by this daemon")

// ErrNoTaskInput is returned by PostTaskInput when argus reports 404 for the
// task's PTY input endpoint. This means the task's session has ended (the agent
// exited), the task was deleted, or argus has no active PTY for it. Callers use
// this to detect a dead PTY rather than silently swallowing keystrokes.
var ErrNoTaskInput = errors.New("argus: task PTY input unavailable")

// HTTPError is the error type doJSON returns when argus responds with a
// non-2xx status. Callers that need to discriminate by status code
// (e.g., the MCP registrar's heartbeat-404 fallback to recovery) should
// use errors.As to unwrap it out of the chain.
type HTTPError struct {
	Method     string
	Path       string
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("argus: %s %s: HTTP %d: %s", e.Method, e.Path, e.StatusCode, e.Body)
}

// worktreeMissingMarker is the stable substring argus emits in the error body
// of /restart (and /delete) when a task's worktree directory no longer exists
// on disk — e.g. an earlier bulk cleanup removed it. Argus phrases the full
// message as `worktree path missing: <path> (delete the task or recreate the
// worktree)`; hera matches only the stable prefix.
const worktreeMissingMarker = "worktree path missing"

// IsWorktreeMissing reports whether err is (or wraps) an argus HTTP error whose
// body indicates the task's worktree directory is gone (BUG-020). Such a task
// cannot be restarted (the agent backend has no working tree to resume in), so
// callers surface a "delete the orphan" recovery path instead of an opaque 500,
// and the delete path treats it as a soft skip so the DB rows still clear.
//
// The signal is the argus error body — the formatted JSON envelope is the only
// place argus reports it — so this is a substring match on the body, NOT on the
// fully-formatted error string (the typed *HTTPError is unwrapped via errors.As
// first; only its Body is matched).
func IsWorktreeMissing(err error) bool {
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		return false
	}
	return strings.Contains(httpErr.Body, worktreeMissingMarker)
}

// alreadyExistsMarker is the stable substring argus emits in the error body of
// /restart when a concurrent Start already created the session for this task —
// argus phrases it `session already exists for task <id>`. It surfaces as a 500
// because two restart calls raced past the non-atomic 409 guard (one spawned
// the session, the loser's Start then hit the duplicate). hera matches the
// stable prefix.
const alreadyExistsMarker = "session already exists"

// IsAlreadyRunning reports whether err indicates argus already has a LIVE
// session for the task — either a 409 ("task already running": the agent is
// live, so no restart was needed) or the Start-race 500 carrying
// alreadyExistsMarker. For a reattach this is SUCCESS, not a failure: the goal
// is a live session the proxy subscription can pick up, and one already exists.
//
// BUG-022: a ⊘ mixed-coord reattach (and any other reattach) could surface this
// as a red error modal even though the session connected — the manual reattach
// raced the focus-driven auto-reattach, or the archived coord's session was
// never dead (archiving an argus task does not stop its PTY). Treating an
// already-live session as success suppresses that false-failure modal while
// genuine failures (worktree missing, restart unsupported, network) still
// surface.
//
// The typed *HTTPError is unwrapped via errors.As; the 409 is matched by status
// and the race-500 by its body marker (NOT by status alone — a genuine start
// failure also 500s).
func IsAlreadyRunning(err error) bool {
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		return false
	}
	if httpErr.StatusCode == 409 {
		return true
	}
	return httpErr.StatusCode == 500 && strings.Contains(httpErr.Body, alreadyExistsMarker)
}

// IsNotFound reports whether err is (or wraps) an argus HTTP 404 — the
// addressed resource no longer exists on the argus side. Callers use this
// to treat operations against pruned tasks as no-ops instead of failures
// (argus prunes tasks by deleting them outright, so any recorded task id
// can dangle). Typed check via errors.As — never string-match the
// formatted message.
func IsNotFound(err error) bool {
	var httpErr *HTTPError
	return errors.As(err, &httpErr) && httpErr.StatusCode == 404
}
