package view

import (
	"context"
	"log/slog"

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
// All methods are invoked synchronously from the key pump (the tview event
// loop) — the implementation MUST NOT block AND MUST NOT call anything that
// bounces through tview's QueueUpdate from the calling goroutine (QueueUpdate
// blocks until the queued func runs on this same loop — a self-deadlock).
// The mutationBridge satisfies this by capturing the selection synchronously
// and handing every blocking phase (svc calls, modal opens, refresh) to a
// goroutine; see mutationBridge's concurrency contract.
type MutationHandler interface {
	OnNew()

	// OnNewWorker handles the `w` RAIL-focus-only key. It opens a single-field
	// input modal prompting for the new worker's prompt, then spawns a worker
	// agent under the coordinator implied by the current rail selection.
	OnNewWorker()

	OnRename()
	OnDelete()
	OnArchive()
	OnListAll()
	OnHelp()

	// Stage P extended keyset (D15). OnPrune (`^r`), OnOpenPR (`^p`), and
	// OnDelete (`^d`) are RAIL-focus-only, acting on the current selection; in
	// a pane the control byte forwards to the PTY (Ctrl-D=0x04, Ctrl-R=0x12,
	// Ctrl-P=0x10). OnStatusAdvance (`s`) / OnStatusRevert (`S`) are likewise
	// RAIL-focus-only — in a pane the rune forwards to the PTY.
	OnPrune()
	OnOpenPR()
	OnStatusAdvance()
	OnStatusRevert()

	// OnPin toggles the pinned state of the selected coordinator or agent
	// (`P`, RAIL-focus-only). Pin and archive are mutually exclusive; in a
	// pane the rune `P` forwards to the PTY instead.
	OnPin()

	// OnAdopt adopts the selected FREELANCER into a chosen coordinator (`J`,
	// RAIL-focus-only): it opens an orchestrator picker and creates an
	// operator-side worker binding (mirroring hera_join attach-mode). On a
	// non-freelancer row it surfaces visible feedback; in a pane the rune `J`
	// forwards to the PTY instead.
	OnAdopt()

	// OnResurrect is consulted on Enter-in-RAIL BEFORE pane-entry. It returns
	// true when it owns the Enter (an archived coord with the Archive section
	// visible — it shows a resurrect confirm), so the router must NOT enter a
	// pane; false otherwise (the router runs the normal pane-entry path).
	OnResurrect() bool
}

// ControlSender sends the argus key-surrender control frames the router needs
// directly (release). The `?` help frame is sent through the MutationHandler's
// OnHelp so the existing RAIL-only mutation routing is unchanged; Esc-release
// is a router concern because it is not a mutation key. *viewControl is the
// production implementation; tests inject a capturing fake. Nil is a safe
// no-op (the router simply doesn't release).
type ControlSender interface {
	SendRelease() error
}

// PaneScroller scrolls the currently-focused pane's scrollback by delta lines
// (positive = up into history, negative = down toward the live screen),
// driving the ⇧↑/⇧↓ keys (D15). It MUST NOT move the rail selection. The router
// only calls it when focus is in a pane (COORD/AGENT); nil makes ⇧↑/⇧↓ a no-op
// scroll (the event is still consumed in a pane so it never reaches the PTY).
type PaneScroller interface {
	ScrollFocusedPane(state FocusState, delta int)
}

// InPaneNavigator moves the rail selection to the next (dir>0) / previous
// (dir<0) pane-bindable agent and re-enters that selection's primary pane,
// keeping focus INSIDE a pane — driving the ⌘↑/⌘↓ (and ^↑/^↓) keys (D15). It
// returns the focus state the operator should land in (FocusCOORD / FocusAGENT,
// never FocusRAIL when a bindable row was reached). The router only calls it
// when focus is already in a pane; nil makes the keys a no-op (still consumed
// in a pane so they never reach the PTY).
type InPaneNavigator interface {
	InPaneNavigate(dir int) FocusState
}

// ModalGate is consulted on every key event so the router can yield to
// any active modal overlay (input field, confirm modal, help modal).
// When IsModalActive returns true HandleKey passes the event through
// unchanged so the focused modal widget consumes it directly.
type ModalGate interface {
	IsModalActive() bool
}

// RailFilter is consulted while RAIL is focused so the router can yield to the
// rail's search INPUT mode. When IsFiltering returns true HandleKey passes the
// event straight to the focused rail widget (like ModalGate) so typed keys —
// including ones that are otherwise mutation triggers (`n`/`a`/…) or the
// Esc-release — become filter input handled by the rail's HandleFilterKey
// instead of firing here. Nil means "never filtering" (the gate is off).
type RailFilter interface {
	IsFiltering() bool
}

// BorderUpdater is invoked every time the focus state changes so the
// rendered surface can repaint the colored focus border. Stage F provides
// the concrete implementation against tview Box widgets.
type BorderUpdater interface {
	OnFocusChanged(state FocusState)
}

// RailSelectHandler is fired when the operator presses Enter while RAIL
// is focused. The implementation reads the rail's currently-highlighted
// node and (when appropriate) rebinds the COORD or AGENT pane to that
// row's argus task before returning the focus state the operator should
// land in: FocusAGENT after rebinding a worker, FocusCOORD after
// rebinding a coordinator, or FocusRAIL when the row is not bindable
// (orchestrator header, dead binding) — in which case Enter propagates
// to the tree so it can fold/unfold the node.
type RailSelectHandler interface {
	OnRailSelectEnter() FocusState
}

// PaneByteForwarder enqueues pane-focus keystroke bytes for asynchronous,
// order-preserving, coalesced delivery to a task's PTY input endpoint. Enqueue
// MUST NOT block the caller (the tview input-handler goroutine). *PaneForwarder
// is the production implementation; tests inject a recording/blocking fake. A
// nil Forward makes the router fall back to a synchronous PostTaskInput.
type PaneByteForwarder interface {
	Enqueue(taskID string, payload []byte)
}

// KeyRouter is the top-level input capture handler. It owns the focus
// state machine and decides for each key event whether to (1) transition
// focus, (2) fire a mutation handler, (3) forward bytes to a task's PTY,
// or (4) propagate the event so the focused widget handles it (rail
// navigation in RAIL focus).
//
// HandleKey is the function passed to tview.Application.SetInputCapture.
type KeyRouter struct {
	Focus      *FocusMachine
	Targets    PaneTargets
	Poster     InputPoster
	Mutations  MutationHandler
	Border     BorderUpdater
	RailSelect RailSelectHandler
	Modal      ModalGate

	// Filter, when set, lets the router yield keys to the rail while it is in
	// search input mode (the `/` filter). Nil disables the gate (no yielding).
	Filter RailFilter

	// Forward, when set, receives pane-focus keystroke bytes for asynchronous
	// ordered+coalesced delivery — handlePane enqueues and returns immediately
	// so a slow round-trip never serializes typing on the event loop. When nil
	// the router falls back to the synchronous Poster path below.
	Forward PaneByteForwarder

	// Scroller scrolls the focused pane (⇧↑/⇧↓). InPaneNav flips the rail
	// selection while staying in a pane (⌘↑/⌘↓ / ^↑/^↓). Both are pane-focus-
	// only; nil makes the corresponding key a consumed no-op in a pane. See
	// PaneScroller / InPaneNavigator. (D15.)
	Scroller  PaneScroller
	InPaneNav InPaneNavigator

	// Control sends key-surrender control frames to argus. Esc-from-RAIL
	// routes here as a release frame; nil makes Esc-from-RAIL a no-op
	// (still consumed, never forwarded).
	Control ControlSender

	// Ctx is the context used when calling Poster.PostTaskInput. Defaults
	// to context.Background() when nil.
	Ctx context.Context

	// Log records why a focused-pane keystroke failed to reach its PTY. The
	// forward POST to argus's /api/tasks/{id}/input endpoint can fail at the
	// boundary (argus down, task gone, endpoint unsupported, auth) — without a
	// log that failure is INVISIBLE: focus moved into the pane but nothing
	// echoes, with no clue why (the reported E1 symptom). Logging the error
	// turns a silent drop into a diagnosable event. A nil Log is a safe no-op
	// (falls back to slog.Default()).
	Log *slog.Logger
}

// HandleKey is the tview-compatible input capture. Returns nil when the
// event has been fully consumed by the router; returns the original event
// when the focused widget should still handle it (e.g., j/k tree nav in
// RAIL).
func (r *KeyRouter) HandleKey(event *tcell.EventKey) *tcell.EventKey {
	if event == nil {
		return nil
	}

	// While any modal overlay is up the focused tview primitive (input
	// field, confirm modal, help text view) owns the keyboard. Yield
	// every event unchanged so the modal can read its own keys
	// (Enter / Esc / q / button focus) without the router stealing
	// them.
	if r.Modal != nil && r.Modal.IsModalActive() {
		return event
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
		// While the rail is in search input mode, yield every key to the focused
		// rail widget (its HandleFilterKey) so runes build the query and Esc /
		// Enter clear / accept it — instead of firing mutations or the
		// Esc-release frame here. Focus-traversal keys above still work (they are
		// handled before this), so the operator can leave the rail mid-search.
		if r.Filter != nil && r.Filter.IsFiltering() {
			return event
		}
		return r.handleRail(event)
	}
	return r.handlePane(event)
}

// handleRail dispatches keys when focus is RAIL. Enter consults the
// RailSelectHandler (when wired) to rebind the appropriate pane and
// returns the focus target; the six mutation keys fire their handlers;
// j/k are translated to KeyDown/KeyUp so the tree moves selection even
// if the focused widget's bare-rune handler is suppressed; everything
// else (↑/↓, PgUp/PgDn) propagates so the tree-view native handling
// applies.
func (r *KeyRouter) handleRail(event *tcell.EventKey) *tcell.EventKey {
	// Esc from RAIL hands the keyboard back to argus (key-surrender
	// contract, D12). The Esc byte is NOT forwarded to any task; in a pane
	// (handlePane) Esc is instead forwarded verbatim so vim/Claude can see
	// it. Consume the event either way.
	if event.Key() == tcell.KeyEsc {
		if r.Control != nil {
			_ = r.Control.SendRelease()
		}
		return nil
	}

	// Destructive + external verbs are RAIL-focus-only (reversed from the
	// earlier any-focus decision): `^d` delete, `^r` prune-completed, `^p`
	// open-PR act on the current rail selection and are intercepted here (never
	// forwarded to a PTY). In a pane (handlePane) the same control bytes are
	// forwarded verbatim so an agent gets EOF / reverse-search / history-prev.
	if event.Key() == tcell.KeyCtrlD {
		if r.Mutations != nil {
			r.Mutations.OnDelete()
		}
		return nil
	}
	if event.Key() == tcell.KeyCtrlR {
		if r.Mutations != nil {
			r.Mutations.OnPrune()
		}
		return nil
	}
	if event.Key() == tcell.KeyCtrlP {
		if r.Mutations != nil {
			r.Mutations.OnOpenPR()
		}
		return nil
	}

	if event.Key() == tcell.KeyEnter {
		// Resurrect-on-Enter takes precedence: an archived coord row with the
		// Archive section visible shows a resurrect confirm instead of entering
		// a pane. OnResurrect returns true when it owns the Enter (modal shown);
		// otherwise we fall through to the normal pane-entry path below.
		if r.Mutations != nil && r.Mutations.OnResurrect() {
			return nil
		}
		target := FocusRAIL
		if r.RailSelect != nil {
			target = r.RailSelect.OnRailSelectEnter()
		} else if r.Targets != nil && r.Targets.AgentTaskID() != "" {
			target = FocusAGENT
		}
		switch target {
		case FocusAGENT:
			r.Focus.JumpToAGENT()
			r.notifyBorder()
			return nil
		case FocusCOORD:
			if r.Focus.State() != FocusCOORD {
				r.Focus.Advance() // RAIL → COORD
				r.notifyBorder()
			}
			return nil
		}
		// Selection not bindable (orchestrator header, dead row). Let the
		// tree handle Enter so it can fold/unfold the node. (The archived-coord
		// resurrect flow is handled earlier via OnResurrect, before pane-entry.)
		return event
	}

	if event.Key() == tcell.KeyRune {
		switch event.Rune() {
		case 'j':
			// Translate to KeyDown so the focused tree-view moves selection
			// even when its default 'j' handling is suppressed by tview's
			// focus dispatch (the SDK-installed input capture takes the
			// rune events first).
			return tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone)
		case 'k':
			return tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone)
		case 'n':
			if r.Mutations != nil {
				r.Mutations.OnNew()
			}
			return nil
		case 'w':
			// Spawn worker under the selected coordinator (D1 RAIL-focus-only).
			// In COORD/AGENT focus this rune falls through to handlePane and is
			// forwarded verbatim to the PTY, so the gate is purely positional
			// (handleRail is only called when focus is RAIL).
			if r.Mutations != nil {
				r.Mutations.OnNewWorker()
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
		case 's':
			// `s` advances the selected agent's status; RAIL-focus-only
			// (in a pane it forwards to the PTY via handlePane).
			if r.Mutations != nil {
				r.Mutations.OnStatusAdvance()
			}
			return nil
		case 'S':
			if r.Mutations != nil {
				r.Mutations.OnStatusRevert()
			}
			return nil
		case 'P':
			// `P` toggles pin on the selected coord/agent; RAIL-focus-only
			// (in a pane the rune forwards to the PTY via handlePane).
			if r.Mutations != nil {
				r.Mutations.OnPin()
			}
			return nil
		case 'J':
			// `J` adopts the selected freelancer into a chosen coordinator;
			// RAIL-focus-only (in a pane the rune forwards to the PTY via
			// handlePane). Lowercase `j` is nav-down, handled above.
			if r.Mutations != nil {
				r.Mutations.OnAdopt()
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
	// tree consumes ↑/↓/PgUp/PgDn for selection movement).
	return event
}

// handlePane forwards keystrokes to the currently bound COORD or AGENT
// task's PTY input endpoint. The byte encoding mirrors what a real
// terminal would emit for the same key, so the upstream PTY's bubbletea /
// readline interprets it natively.
func (r *KeyRouter) handlePane(event *tcell.EventKey) *tcell.EventKey {
	// ⇧↑/⇧↓ scroll the focused pane's scrollback WITHOUT moving the rail
	// selection (D15). Intercepted here so the bytes never reach the PTY.
	if delta, ok := scrollDelta(event); ok {
		if r.Scroller != nil {
			r.Scroller.ScrollFocusedPane(r.Focus.State(), delta)
		}
		return nil
	}
	// ⌘↑/⌘↓ (and ^↑/^↓) move the rail selection to the next/prev agent while
	// keeping focus inside a pane bound to the new selection (D15). The
	// navigator re-enters the new selection's primary pane and returns the
	// focus state to land in; we apply it and repaint the border. Intercepted
	// so the bytes never reach the PTY and focus never falls back to RAIL.
	if dir, ok := inPaneNavDir(event); ok {
		if r.InPaneNav != nil {
			target := r.InPaneNav.InPaneNavigate(dir)
			switch target {
			case FocusCOORD:
				r.Focus.JumpToCOORD()
				r.notifyBorder()
			case FocusAGENT:
				r.Focus.JumpToAGENT()
				r.notifyBorder()
			}
		}
		return nil
	}

	if r.Targets == nil || r.Poster == nil {
		return nil
	}
	state := r.Focus.State()
	var taskID string
	if state == FocusCOORD {
		taskID = r.Targets.CoordTaskID()
	} else {
		taskID = r.Targets.AgentTaskID()
	}
	if taskID == "" {
		// Defense-in-depth: focus is in a pane but no task is bound to it, so
		// the keystroke has nowhere to go. Surface it — a persistently empty
		// target while a pane holds focus is the binding-not-loaded face of E1.
		r.logger().Debug("view.keys: focused pane has no bound task; dropping keystroke",
			"focus", state.String())
		return nil
	}
	payload := encodeEventForPTY(event)
	if len(payload) == 0 {
		return nil
	}
	// Fast path: hand the byte(s) to the async forwarder and return IMMEDIATELY.
	// The forwarder drains in FIFO order (preserving key order) on its own
	// goroutine and coalesces consecutive same-task bytes into one POST, so fast
	// typing never serializes behind per-keystroke HTTP round-trips on the tview
	// input-handler goroutine. The forwarder emits the E1 failure log itself.
	if r.Forward != nil {
		r.Forward.Enqueue(taskID, payload)
		return nil
	}
	// Synchronous fallback (no async forwarder wired, e.g. unit tests): POST on
	// the calling goroutine. A failure here is the reported E1 symptom — focus
	// moved into the pane but typed keys never echo. Log it (with enough context
	// to act) instead of swallowing.
	ctx := r.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if _, err := r.Poster.PostTaskInput(ctx, taskID, payload); err != nil && ctx.Err() == nil {
		r.logger().Warn("view.keys: forward keystroke to pane PTY failed",
			"focus", state.String(), "task_id", taskID, "bytes", len(payload), "err", err)
	}
	return nil
}

// logger returns the router's logger, defaulting to slog.Default() when none
// was wired. Centralized so callers don't repeat the nil check.
func (r *KeyRouter) logger() *slog.Logger {
	if r.Log != nil {
		return r.Log
	}
	return slog.Default()
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

// scrollDelta decodes ⇧↑ / ⇧↓ into a scroll delta: ⇧↑ scrolls UP into history
// (+1), ⇧↓ scrolls back DOWN toward the live screen (-1). Shift takes
// precedence over Ctrl/Meta when a terminal reports both, so the scroll keys
// stay distinct from the in-pane-nav keys. Returns ok=false for anything else.
func scrollDelta(e *tcell.EventKey) (int, bool) {
	if e.Modifiers()&tcell.ModShift == 0 {
		return 0, false
	}
	switch e.Key() {
	case tcell.KeyUp:
		return +1, true
	case tcell.KeyDown:
		return -1, true
	}
	return 0, false
}

// inPaneNavDir decodes ⌘↑/⌘↓ (ModMeta on macOS) or ^↑/^↓ (ModCtrl on Linux)
// into an in-pane navigation direction: up = previous agent (-1), down = next
// agent (+1). Mirrors the Cmd/Ctrl acceptance of the focus-ladder keys
// (isFocusForward/Backward). Shift must NOT be set (that's a scroll); callers
// check scrollDelta first, but we also guard here so a ⇧+⌘ combo never
// double-fires. Returns ok=false for anything else.
func inPaneNavDir(e *tcell.EventKey) (int, bool) {
	m := e.Modifiers()
	if m&tcell.ModShift != 0 {
		return 0, false
	}
	if m&tcell.ModCtrl == 0 && m&tcell.ModMeta == 0 {
		return 0, false
	}
	switch e.Key() {
	case tcell.KeyUp:
		return -1, true
	case tcell.KeyDown:
		return +1, true
	}
	return 0, false
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
