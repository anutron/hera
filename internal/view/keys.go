package view

import (
	"context"

	"github.com/gdamore/tcell/v2"
)

// InputPoster is the subset of *argus.Client used to forward pane-focus
// keystrokes to the bound task's PTY input endpoint. Stage-J wiring uses
// the real client; tests use a fake that records calls.
type InputPoster interface {
	PostTaskInput(ctx context.Context, taskID string, payload []byte) (int, error)
}

// PaneTargets resolves which argus task IDs the COORD and AGENT panes
// should forward bytes to right now. Driven by the rail selection. An
// empty string means "no current target" — the router drops the keystroke
// rather than POSTing to an empty path.
type PaneTargets interface {
	CoordTaskID() string
	AgentTaskID() string
}

// MutationHandler receives the six rail-only mutation key events. Stage H
// implements the modal flows behind each; Stage G only fires the trigger.
// All methods are invoked synchronously from the key pump — the
// implementation MUST NOT block (open modals via QueueUpdateDraw, etc.).
type MutationHandler interface {
	OnNew()
	OnRename()
	OnDelete()
	OnArchive()
	OnListAll()
	OnHelp()
}

// BorderUpdater is invoked every time the focus state changes so the
// rendered surface can repaint the colored focus border. Stage F provides
// the concrete implementation against tview Box widgets.
type BorderUpdater interface {
	OnFocusChanged(state FocusState)
}

// KeyRouter is the top-level input capture handler. It owns the focus
// state machine and decides for each key event whether to (1) transition
// focus, (2) fire a mutation handler, (3) forward bytes to a task's PTY,
// or (4) propagate the event so the focused widget handles it (rail
// navigation in RAIL focus).
//
// HandleKey is the function passed to tview.Application.SetInputCapture.
type KeyRouter struct {
	Focus     *FocusMachine
	Targets   PaneTargets
	Poster    InputPoster
	Mutations MutationHandler
	Border    BorderUpdater

	// Ctx is the context used when calling Poster.PostTaskInput. Defaults
	// to context.Background() when nil.
	Ctx context.Context
}

// HandleKey is the tview-compatible input capture. Returns nil when the
// event has been fully consumed by the router; returns the original event
// when the focused widget should still handle it (e.g., j/k tree nav in
// RAIL).
func (r *KeyRouter) HandleKey(event *tcell.EventKey) *tcell.EventKey {
	if event == nil {
		return nil
	}

	// Focus-traversal keys take precedence over everything else and apply
	// in every focus state.
	if isFocusForward(event) {
		r.Focus.Advance()
		r.notifyBorder()
		return nil
	}
	if isFocusBackward(event) {
		r.Focus.Retreat()
		r.notifyBorder()
		return nil
	}
	if isCtrlQ(event) {
		prev := r.Focus.State()
		r.Focus.ToRAIL()
		if prev != FocusRAIL {
			r.notifyBorder()
		}
		return nil
	}

	if r.Focus.State() == FocusRAIL {
		return r.handleRail(event)
	}
	return r.handlePane(event)
}

// handleRail dispatches keys when focus is RAIL. Enter (with a live agent
// target) jumps to AGENT; the six mutation keys fire their handlers;
// everything else (including bare j/k/↑/↓ and Enter with no agent) is
// propagated for the focused widget to interpret.
func (r *KeyRouter) handleRail(event *tcell.EventKey) *tcell.EventKey {
	if event.Key() == tcell.KeyEnter {
		if r.Targets != nil && r.Targets.AgentTaskID() != "" {
			r.Focus.JumpToAGENT()
			r.notifyBorder()
			return nil
		}
		// No live agent (e.g. operator is on an archived coord row). Let
		// the rail handle Enter — Stage H wires the resurrect flow there.
		return event
	}

	if event.Key() == tcell.KeyCtrlD {
		if r.Mutations != nil {
			r.Mutations.OnDelete()
		}
		return nil
	}

	if event.Key() == tcell.KeyRune {
		switch event.Rune() {
		case 'n':
			if r.Mutations != nil {
				r.Mutations.OnNew()
			}
			return nil
		case 'r':
			if r.Mutations != nil {
				r.Mutations.OnRename()
			}
			return nil
		case 'a':
			if r.Mutations != nil {
				r.Mutations.OnArchive()
			}
			return nil
		case 'l':
			if r.Mutations != nil {
				r.Mutations.OnListAll()
			}
			return nil
		case '?':
			if r.Mutations != nil {
				r.Mutations.OnHelp()
			}
			return nil
		}
	}

	// Anything not bound here propagates to the focused widget (the rail
	// tree consumes j/k/↑/↓ for selection movement).
	return event
}

// handlePane forwards keystrokes to the currently bound COORD or AGENT
// task's PTY input endpoint. The byte encoding mirrors what a real
// terminal would emit for the same key, so the upstream PTY's bubbletea /
// readline interprets it natively.
func (r *KeyRouter) handlePane(event *tcell.EventKey) *tcell.EventKey {
	if r.Targets == nil || r.Poster == nil {
		return nil
	}
	var taskID string
	if r.Focus.State() == FocusCOORD {
		taskID = r.Targets.CoordTaskID()
	} else {
		taskID = r.Targets.AgentTaskID()
	}
	if taskID == "" {
		return nil
	}
	payload := encodeEventForPTY(event)
	if len(payload) == 0 {
		return nil
	}
	ctx := r.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	_, _ = r.Poster.PostTaskInput(ctx, taskID, payload)
	return nil
}

func (r *KeyRouter) notifyBorder() {
	if r.Border != nil {
		r.Border.OnFocusChanged(r.Focus.State())
	}
}

// isFocusForward matches Cmd/Ctrl-→. macOS terminals may report Cmd as
// ModMeta; Linux uses ModCtrl. We accept either to honour the spec
// wording "Cmd/Ctrl-←/→".
func isFocusForward(e *tcell.EventKey) bool {
	if e.Key() != tcell.KeyRight {
		return false
	}
	m := e.Modifiers()
	return m&tcell.ModCtrl != 0 || m&tcell.ModMeta != 0
}

func isFocusBackward(e *tcell.EventKey) bool {
	if e.Key() != tcell.KeyLeft {
		return false
	}
	m := e.Modifiers()
	return m&tcell.ModCtrl != 0 || m&tcell.ModMeta != 0
}

func isCtrlQ(e *tcell.EventKey) bool {
	return e.Key() == tcell.KeyCtrlQ
}

// encodeEventForPTY converts a tcell key event into the byte sequence the
// upstream PTY's stdin would have received from a real terminal. Best
// effort; uncovered keys return nil (the keystroke is dropped). The
// upstream task is responsible for its own line discipline.
func encodeEventForPTY(e *tcell.EventKey) []byte {
	switch e.Key() {
	case tcell.KeyRune:
		return []byte(string(e.Rune()))
	case tcell.KeyEnter:
		// CR is the PTY convention; the line discipline upstream
		// translates it to NL if it cares.
		return []byte{'\r'}
	case tcell.KeyTab:
		return []byte{'\t'}
	case tcell.KeyBacktab:
		return []byte("\x1b[Z")
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		return []byte{0x7f}
	case tcell.KeyEsc:
		return []byte{0x1b}
	case tcell.KeyUp:
		return []byte("\x1b[A")
	case tcell.KeyDown:
		return []byte("\x1b[B")
	case tcell.KeyRight:
		return []byte("\x1b[C")
	case tcell.KeyLeft:
		return []byte("\x1b[D")
	case tcell.KeyHome:
		return []byte("\x1b[H")
	case tcell.KeyEnd:
		return []byte("\x1b[F")
	case tcell.KeyPgUp:
		return []byte("\x1b[5~")
	case tcell.KeyPgDn:
		return []byte("\x1b[6~")
	case tcell.KeyDelete:
		return []byte("\x1b[3~")
	}
	// tcell encodes Ctrl-A through Ctrl-Z as KeyCtrlA..KeyCtrlZ, whose
	// integer values are the ASCII letter codes (65..90), NOT the
	// control-byte values (1..26). The upstream PTY wants the actual
	// control character a real terminal would have produced, so map
	// KeyCtrl<X> → byte (X − 'A' + 1).
	if k := e.Key(); k >= tcell.KeyCtrlA && k <= tcell.KeyCtrlZ {
		return []byte{byte(k - tcell.KeyCtrlA + 1)}
	}
	return nil
}
