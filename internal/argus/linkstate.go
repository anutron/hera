package argus

import (
	"sync"
	"sync/atomic"
)

// LinkState is the current health of hera's connection to the argus REST
// API. The zero value is LinkHealthy so package-level state starts healthy
// without initialization.
type LinkState int32

const (
	// LinkHealthy indicates argus is reachable and recently re-registered.
	LinkHealthy LinkState = iota
	// LinkRecovering indicates the watcher detected an argus restart and
	// the recovery routine is in flight (re-discovering port + force
	// re-registering tools and settings). MCP tool handlers must return
	// a structured "recovering" error during this window.
	LinkRecovering
	// LinkDown indicates recovery failed (e.g., socket unreachable). MCP
	// tool handlers must return a structured "down" error and surface
	// LastError so callers can diagnose. Hera does NOT exit from down; the
	// watcher keeps polling and recovery re-runs on the next restart signal.
	LinkDown
)

// String returns the lowercase wire string used in hera_status responses
// and in the `argus link <state>` MCP error messages.
func (s LinkState) String() string {
	switch s {
	case LinkHealthy:
		return "healthy"
	case LinkRecovering:
		return "recovering"
	case LinkDown:
		return "down"
	default:
		return "unknown"
	}
}

// state holds the current link state. Read/written via atomic ops so
// MCP handlers can gate without taking a lock on every tool call.
var state atomic.Int32

// lastError mirrors the most recent failure recorded during recovery so
// the `argus link down: <err>` MCP error and the `argus_link_error`
// field on hera_status can surface a real diagnostic. Guarded by a mutex
// because errors aren't comparable with atomic.Value's strict-type rule
// and atomic.Pointer[error] requires Go 1.19+ pointer wrapping that
// gains us nothing here.
var (
	lastErrMu sync.RWMutex
	lastErr   error
)

// GetLinkState returns the current link state. Safe for concurrent use.
func GetLinkState() LinkState {
	return LinkState(state.Load())
}

// SetLinkState updates the current link state. Safe for concurrent use.
// Callers that move the state to LinkDown should also call SetLinkError
// with the underlying failure so the down message surfaces context.
func SetLinkState(s LinkState) {
	state.Store(int32(s))
}

// LinkLastError returns the most recent error stored by SetLinkError,
// or nil if none has been recorded since the last clear.
func LinkLastError() error {
	lastErrMu.RLock()
	defer lastErrMu.RUnlock()
	return lastErr
}

// SetLinkError records the error driving a transition to LinkDown.
// Pass nil to clear the stored error when transitioning back to healthy.
func SetLinkError(err error) {
	lastErrMu.Lock()
	lastErr = err
	lastErrMu.Unlock()
}
