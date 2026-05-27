package argus

import (
	"errors"
	"fmt"
)

// ErrNoTaskSize is returned by GetTaskSize when argus reports 404 — the
// task exists but no PTY session is active, so no size is available. The
// caller treats this as a signal to fall back to a default surface size.
var ErrNoTaskSize = errors.New("argus: no PTY size for task")

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
