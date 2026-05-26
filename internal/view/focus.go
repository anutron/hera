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
type FocusMachine struct {
	state FocusState
}

// NewFocusMachine returns a machine starting in RAIL focus, matching the
// first-open requirement.
func NewFocusMachine() *FocusMachine {
	return &FocusMachine{state: FocusRAIL}
}

// State returns the current focus state.
func (f *FocusMachine) State() FocusState { return f.state }

// Advance moves focus one step right along RAIL → COORD → AGENT. From
// AGENT the call is a no-op (you can't advance past the rightmost pane).
func (f *FocusMachine) Advance() {
	switch f.state {
	case FocusRAIL:
		f.state = FocusCOORD
	case FocusCOORD:
		f.state = FocusAGENT
	}
}

// Retreat moves focus one step left along AGENT → COORD → RAIL. From RAIL
// the call is a no-op.
func (f *FocusMachine) Retreat() {
	switch f.state {
	case FocusAGENT:
		f.state = FocusCOORD
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
