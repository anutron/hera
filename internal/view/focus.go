// Package view implements the hera-view argus plugin view: a three-column
// tview surface (rail + coord pane + agent pane) served over a WebSocket by
// the hera daemon. This file holds the focus state machine.
package view

// FocusState is one of three positions on the rail → coord → agent ladder.
// The bottom-bar hints and the colored-border focus indicator both key off
// it. Mutation keys (n/r/^d/a/l/?) are recognized only in RAIL; in COORD
// and AGENT they are forwarded as ordinary bytes to the bound task's PTY.
type FocusState int

const (
	// FocusRAIL — the left navigation rail has focus.
	FocusRAIL FocusState = iota
	// FocusCOORD — the middle coordinator-PTY pane has focus.
	FocusCOORD
	// FocusAGENT — the right agent-PTY pane has focus.
	FocusAGENT
)

// String returns the canonical bottom-bar label for the state.
func (s FocusState) String() string {
	switch s {
	case FocusRAIL:
		return "RAIL"
	case FocusCOORD:
		return "COORD"
	case FocusAGENT:
		return "AGENT"
	default:
		return "?"
	}
}

// FocusMachine is the three-state focus machine. All transitions are
// in-process and synchronous; not safe for concurrent callers (the tview
// input pump is single-threaded by contract).
//
// coordPresent reflects whether the COORD pane is currently in the layout.
// In freelance mode (D11) the body is rail + a single full-width agent pane
// with no coord, so the COORD position is skipped on traversal and focus is
// never allowed to rest there.
type FocusMachine struct {
	state        FocusState
	coordPresent bool
}

// NewFocusMachine returns a machine starting in RAIL focus, matching the
// first-open requirement. coordPresent defaults true (the normal three-
// column layout).
func NewFocusMachine() *FocusMachine {
	return &FocusMachine{state: FocusRAIL, coordPresent: true}
}

// State returns the current focus state.
func (f *FocusMachine) State() FocusState { return f.state }

// SetCoordPresent records whether the COORD pane is in the layout. When the
// coord pane is removed while focus rests on it, focus is bumped to AGENT so
// no keystroke is forwarded to a torn-down pane. Returns true when the focus
// state changed as a side effect (caller should repaint).
func (f *FocusMachine) SetCoordPresent(v bool) bool {
	f.coordPresent = v
	if !v && f.state == FocusCOORD {
		f.state = FocusAGENT
		return true
	}
	return false
}

// Advance moves focus one step right along RAIL → COORD → AGENT. In
// freelance mode (no coord) RAIL advances straight to AGENT. From AGENT the
// call is a no-op (you can't advance past the rightmost pane).
func (f *FocusMachine) Advance() {
	switch f.state {
	case FocusRAIL:
		if f.coordPresent {
			f.state = FocusCOORD
		} else {
			f.state = FocusAGENT
		}
	case FocusCOORD:
		f.state = FocusAGENT
	}
}

// Retreat moves focus one step left along AGENT → COORD → RAIL. In freelance
// mode (no coord) AGENT retreats straight to RAIL. From RAIL the call is a
// no-op.
func (f *FocusMachine) Retreat() {
	switch f.state {
	case FocusAGENT:
		if f.coordPresent {
			f.state = FocusCOORD
		} else {
			f.state = FocusRAIL
		}
	case FocusCOORD:
		f.state = FocusRAIL
	}
}

// ToRAIL forces focus back to RAIL regardless of current state. Drives
// the Ctrl-Q "escape to rail" binding.
func (f *FocusMachine) ToRAIL() { f.state = FocusRAIL }

// JumpToAGENT forces focus directly to AGENT, skipping COORD. Drives the
// "Enter from RAIL against a live agent row" binding.
func (f *FocusMachine) JumpToAGENT() { f.state = FocusAGENT }
