package argus

import "fmt"

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
