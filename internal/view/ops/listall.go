package ops

import "sync"

// ListAllState tracks whether the rail's "Archive" section is visible
// for the current WebSocket session. Visibility is in-memory only —
// a fresh WebSocket connection starts with the Archive section hidden
// per design.md D5 ("l listall").
//
// Safe for concurrent access; the rail event loop and the key handler
// can both read/write.
type ListAllState struct {
	mu      sync.RWMutex
	visible bool
}

// NewListAllState constructs a fresh state with visible=false (the
// first-open default).
func NewListAllState() *ListAllState { return &ListAllState{} }

// Visible reports whether the Archive section is currently visible.
func (s *ListAllState) Visible() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.visible
}

// Toggle flips visibility and returns the new state. This is the
// `l` rail-key handler's only concern — no DB write, no argus call.
func (s *ListAllState) Toggle() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.visible = !s.visible
	return s.visible
}
