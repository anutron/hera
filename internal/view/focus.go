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
// coordPresent and agentPresent reflect which panes are currently composed in
// the body — the three-mode layout (D13). The traversal ladder is RAIL plus
// whichever panes are present, in COORD-then-AGENT order:
//
//   - coordinator mode: coordPresent=true,  agentPresent=false → RAIL ↔ COORD
//   - agent mode:       coordPresent=true,  agentPresent=true  → RAIL ↔ COORD ↔ AGENT
//   - freelance mode:   coordPresent=false, agentPresent=true  → RAIL ↔ AGENT
//
// Focus is never allowed to rest on an absent pane: Advance / Retreat step
// only through present states, and SetCoordPresent / SetAgentPresent bump
// focus to the nearest present pane (or RAIL) when the focused pane is torn
// down.
type FocusMachine struct {
	state        FocusState
	coordPresent bool
	agentPresent bool
}

// NewFocusMachine returns a machine starting in RAIL focus, matching the
// first-open requirement. Both panes default present (the normal agent-mode
// split layout).
func NewFocusMachine() *FocusMachine {
	return &FocusMachine{state: FocusRAIL, coordPresent: true, agentPresent: true}
}

// State returns the current focus state.
func (f *FocusMachine) State() FocusState { return f.state }

// SetCoordPresent records whether the COORD (HERA) pane is in the layout.
// When the coord pane is removed while focus rests on it, focus is bumped to
// the nearest remaining pane (AGENT if present, else RAIL). Returns true when
// the focus state changed as a side effect (caller should repaint).
func (f *FocusMachine) SetCoordPresent(v bool) bool {
	f.coordPresent = v
	return f.rebalance()
}

// SetAgentPresent records whether the AGENT pane is in the layout (false in
// coordinator mode, where the body is rail + a single full-width HERA pane).
// When the agent pane is removed while focus rests on it, focus is bumped to
// the nearest remaining pane (COORD if present, else RAIL). Returns true when
// the focus state changed as a side effect.
func (f *FocusMachine) SetAgentPresent(v bool) bool {
	f.agentPresent = v
	return f.rebalance()
}

// rebalance bumps focus off any now-absent pane to the nearest present one so
// no keystroke is ever forwarded to a torn-down pane. Returns true when the
// state changed.
func (f *FocusMachine) rebalance() bool {
	prev := f.state
	if f.state == FocusCOORD && !f.coordPresent {
		if f.agentPresent {
			f.state = FocusAGENT
		} else {
			f.state = FocusRAIL
		}
	}
	if f.state == FocusAGENT && !f.agentPresent {
		if f.coordPresent {
			f.state = FocusCOORD
		} else {
			f.state = FocusRAIL
		}
	}
	return f.state != prev
}

// Advance moves focus one step right along the present-pane ladder
// (RAIL → COORD → AGENT, skipping absent panes). From the rightmost present
// pane the call is a no-op.
func (f *FocusMachine) Advance() {
	switch f.state {
	case FocusRAIL:
		switch {
		case f.coordPresent:
			f.state = FocusCOORD
		case f.agentPresent:
			f.state = FocusAGENT
		}
	case FocusCOORD:
		if f.agentPresent {
			f.state = FocusAGENT
		}
	}
}

// Retreat moves focus one step left along the present-pane ladder
// (AGENT → COORD → RAIL, skipping absent panes). From RAIL the call is a
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
// "Enter from RAIL against an agent / freelancer row" binding.
func (f *FocusMachine) JumpToAGENT() { f.state = FocusAGENT }

// JumpToCOORD forces focus directly to the COORD (HERA) pane. Drives the
// "Enter from RAIL against a coordinator row" binding.
func (f *FocusMachine) JumpToCOORD() { f.state = FocusCOORD }
