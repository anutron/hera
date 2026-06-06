package view

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// fakePoster records every PostTaskInput call for assertion. The router
// invokes Forward synchronously (it does not spawn a goroutine in tests),
// so call inspection after HandleKey is race-free.
type fakePoster struct {
	mu    sync.Mutex
	calls []postCall
}

type postCall struct {
	TaskID  string
	Payload []byte
}

func (f *fakePoster) PostTaskInput(_ context.Context, taskID string, payload []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, postCall{TaskID: taskID, Payload: append([]byte(nil), payload...)})
	return len(payload), nil
}

func (f *fakePoster) Calls() []postCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]postCall, len(f.calls))
	copy(out, f.calls)
	return out
}

type fakeTargets struct {
	coord, agent string
}

func (f *fakeTargets) CoordTaskID() string { return f.coord }
func (f *fakeTargets) AgentTaskID() string { return f.agent }

type fakeMutations struct {
	new, newWorker, rename, del, archive, listAll, help int
	prune, openPR, statusAdvance, statusRevert          int
	pin                                                 int
	adopt                                               int
	resurrect                                           int

	// resurrectHandled is what OnResurrect returns — true means it consumed
	// the Enter (showed a resurrect confirm) so the router must NOT fall
	// through to pane-entry.
	resurrectHandled bool
}

func (f *fakeMutations) OnNew()           { f.new++ }
func (f *fakeMutations) OnNewWorker()     { f.newWorker++ }
func (f *fakeMutations) OnRename()        { f.rename++ }
func (f *fakeMutations) OnDelete()        { f.del++ }
func (f *fakeMutations) OnArchive()       { f.archive++ }
func (f *fakeMutations) OnListAll()       { f.listAll++ }
func (f *fakeMutations) OnHelp()          { f.help++ }
func (f *fakeMutations) OnPrune()         { f.prune++ }
func (f *fakeMutations) OnOpenPR()        { f.openPR++ }
func (f *fakeMutations) OnStatusAdvance() { f.statusAdvance++ }
func (f *fakeMutations) OnStatusRevert()  { f.statusRevert++ }
func (f *fakeMutations) OnPin()           { f.pin++ }
func (f *fakeMutations) OnAdopt()         { f.adopt++ }
func (f *fakeMutations) OnResurrect() bool {
	f.resurrect++
	return f.resurrectHandled
}

type fakeBorder struct {
	states []FocusState
}

func (f *fakeBorder) OnFocusChanged(s FocusState) { f.states = append(f.states, s) }

// fakeControl records the control frames the router asks for (release / help)
// so tests can assert the key-surrender contract without a real WebSocket.
type fakeControl struct {
	releases int
	helps    int
}

func (f *fakeControl) SendRelease() error { f.releases++; return nil }
func (f *fakeControl) SendHelp() error    { f.helps++; return nil }

func newRouter() (*KeyRouter, *fakePoster, *fakeMutations, *fakeBorder) {
	p := &fakePoster{}
	m := &fakeMutations{}
	b := &fakeBorder{}
	r := &KeyRouter{
		Focus:     NewFocusMachine(),
		Targets:   &fakeTargets{coord: "coord-1", agent: "agent-1"},
		Poster:    p,
		Mutations: m,
		Border:    b,
	}
	return r, p, m, b
}

// newRouterWithControl is newRouter plus a fake control sender wired in, for
// the Esc-release / help-frame contract tests.
func newRouterWithControl() (*KeyRouter, *fakePoster, *fakeControl) {
	r, p, _, _ := newRouter()
	c := &fakeControl{}
	r.Control = c
	return r, p, c
}

// --- Focus state machine ---

func TestFocusMachine_InitialState(t *testing.T) {
	f := NewFocusMachine()
	if f.State() != FocusRAIL {
		t.Fatalf("initial state: want RAIL, got %s", f.State())
	}
}

func TestFocusMachine_Advance_RAILtoCOORDtoAGENT(t *testing.T) {
	f := NewFocusMachine()
	f.Advance()
	if f.State() != FocusCOORD {
		t.Fatalf("after one Advance: want COORD, got %s", f.State())
	}
	f.Advance()
	if f.State() != FocusAGENT {
		t.Fatalf("after two Advance: want AGENT, got %s", f.State())
	}
	f.Advance()
	if f.State() != FocusAGENT {
		t.Fatalf("Advance from AGENT must be no-op: got %s", f.State())
	}
}

func TestFocusMachine_Retreat_AGENTtoCOORDtoRAIL(t *testing.T) {
	f := NewFocusMachine()
	f.JumpToAGENT()
	f.Retreat()
	if f.State() != FocusCOORD {
		t.Fatalf("after Retreat from AGENT: want COORD, got %s", f.State())
	}
	f.Retreat()
	if f.State() != FocusRAIL {
		t.Fatalf("after Retreat from COORD: want RAIL, got %s", f.State())
	}
	f.Retreat()
	if f.State() != FocusRAIL {
		t.Fatalf("Retreat from RAIL must be no-op: got %s", f.State())
	}
}

func TestFocusMachine_ToRAIL(t *testing.T) {
	f := NewFocusMachine()
	f.JumpToAGENT()
	f.ToRAIL()
	if f.State() != FocusRAIL {
		t.Fatalf("ToRAIL from AGENT: want RAIL, got %s", f.State())
	}
}

func TestFocusMachine_JumpToAGENT(t *testing.T) {
	f := NewFocusMachine()
	f.JumpToAGENT()
	if f.State() != FocusAGENT {
		t.Fatalf("JumpToAGENT from RAIL: want AGENT, got %s", f.State())
	}
}

// TestFocusMachine_FreelanceSkipsCoord proves that with no coord pane
// (freelance mode), traversal jumps RAIL ↔ AGENT directly, never landing on
// COORD.
func TestFocusMachine_FreelanceSkipsCoord(t *testing.T) {
	f := NewFocusMachine()
	f.SetCoordPresent(false)

	f.Advance()
	if f.State() != FocusAGENT {
		t.Fatalf("Advance from RAIL with no coord: want AGENT, got %s", f.State())
	}
	f.Advance()
	if f.State() != FocusAGENT {
		t.Fatalf("Advance from AGENT must be no-op: got %s", f.State())
	}
	f.Retreat()
	if f.State() != FocusRAIL {
		t.Fatalf("Retreat from AGENT with no coord: want RAIL, got %s", f.State())
	}
}

// TestFocusMachine_SetCoordPresentBumpsOffCoord proves that removing the
// coord pane while focus rests on COORD bumps focus to AGENT so no keystroke
// is forwarded to a torn-down pane.
func TestFocusMachine_SetCoordPresentBumpsOffCoord(t *testing.T) {
	f := NewFocusMachine()
	f.Advance() // RAIL → COORD
	if f.State() != FocusCOORD {
		t.Fatalf("setup: want COORD, got %s", f.State())
	}
	changed := f.SetCoordPresent(false)
	if !changed {
		t.Fatalf("SetCoordPresent(false) on COORD should report a state change")
	}
	if f.State() != FocusAGENT {
		t.Fatalf("after coord removed: want AGENT, got %s", f.State())
	}
}

// TestFocusMachine_CoordinatorModeSkipsAgent proves the new present-pane
// ladder: in coordinator mode (COORD present, AGENT absent) traversal steps
// RAIL ↔ COORD only and never reaches a non-existent AGENT pane.
func TestFocusMachine_CoordinatorModeSkipsAgent(t *testing.T) {
	f := NewFocusMachine()
	f.SetAgentPresent(false) // coordinator mode: RAIL + HERA only

	f.Advance()
	if f.State() != FocusCOORD {
		t.Fatalf("Advance from RAIL in coordinator mode: want COORD, got %s", f.State())
	}
	f.Advance()
	if f.State() != FocusCOORD {
		t.Fatalf("Advance from COORD must NOT reach absent AGENT: want COORD, got %s", f.State())
	}
	f.Retreat()
	if f.State() != FocusRAIL {
		t.Fatalf("Retreat from COORD in coordinator mode: want RAIL, got %s", f.State())
	}
}

// TestFocusMachine_SetAgentPresentBumpsOffAgent proves that removing the
// agent pane while focus rests on AGENT bumps focus back to the nearest
// present pane (COORD if present, else RAIL) so no keystroke is forwarded to
// a torn-down pane.
func TestFocusMachine_SetAgentPresentBumpsOffAgent(t *testing.T) {
	f := NewFocusMachine()
	f.JumpToAGENT()
	if f.State() != FocusAGENT {
		t.Fatalf("setup: want AGENT, got %s", f.State())
	}
	// Coord still present: bump to COORD.
	if changed := f.SetAgentPresent(false); !changed {
		t.Fatalf("SetAgentPresent(false) on AGENT should report a state change")
	}
	if f.State() != FocusCOORD {
		t.Fatalf("after agent removed (coord present): want COORD, got %s", f.State())
	}

	// Now drop the coord too while on COORD: bump to RAIL.
	if changed := f.SetCoordPresent(false); !changed {
		t.Fatalf("SetCoordPresent(false) on COORD should report a state change")
	}
	if f.State() != FocusRAIL {
		t.Fatalf("after both panes removed: want RAIL, got %s", f.State())
	}
}

// --- KeyRouter focus traversal ---

func TestKeyRouter_CtrlRight_RAILtoCOORD(t *testing.T) {
	r, _, _, b := newRouter()
	out := r.HandleKey(tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModCtrl))
	if out != nil {
		t.Fatalf("Ctrl-Right must be consumed; got non-nil event")
	}
	if r.Focus.State() != FocusCOORD {
		t.Fatalf("after Ctrl-Right in RAIL: want COORD, got %s", r.Focus.State())
	}
	if len(b.states) != 1 || b.states[0] != FocusCOORD {
		t.Fatalf("border updater should have been notified with COORD; got %v", b.states)
	}
}

func TestKeyRouter_MetaRight_RAILtoCOORD(t *testing.T) {
	r, _, _, _ := newRouter()
	r.HandleKey(tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModMeta))
	if r.Focus.State() != FocusCOORD {
		t.Fatalf("Cmd/Meta-Right must also drive focus; got %s", r.Focus.State())
	}
}

func TestKeyRouter_CtrlRight_COORDtoAGENT(t *testing.T) {
	r, _, _, _ := newRouter()
	r.Focus.Advance() // RAIL → COORD
	r.HandleKey(tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModCtrl))
	if r.Focus.State() != FocusAGENT {
		t.Fatalf("Ctrl-Right from COORD: want AGENT, got %s", r.Focus.State())
	}
}

func TestKeyRouter_CtrlLeft_AGENTtoCOORD(t *testing.T) {
	r, _, _, _ := newRouter()
	r.Focus.JumpToAGENT()
	r.HandleKey(tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModCtrl))
	if r.Focus.State() != FocusCOORD {
		t.Fatalf("Ctrl-Left from AGENT: want COORD, got %s", r.Focus.State())
	}
}

func TestKeyRouter_CtrlLeft_COORDtoRAIL(t *testing.T) {
	r, _, _, _ := newRouter()
	r.Focus.Advance() // → COORD
	r.HandleKey(tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModCtrl))
	if r.Focus.State() != FocusRAIL {
		t.Fatalf("Ctrl-Left from COORD: want RAIL, got %s", r.Focus.State())
	}
}

func TestKeyRouter_EnterFromRAIL_JumpsToAGENT(t *testing.T) {
	r, _, _, _ := newRouter()
	out := r.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	if out != nil {
		t.Fatalf("Enter (with live agent) must be consumed in RAIL")
	}
	if r.Focus.State() != FocusAGENT {
		t.Fatalf("Enter in RAIL with agent target: want AGENT, got %s", r.Focus.State())
	}
}

func TestKeyRouter_EnterFromRAIL_NoAgent_Propagates(t *testing.T) {
	r, _, _, _ := newRouter()
	r.Targets = &fakeTargets{coord: "c", agent: ""}
	out := r.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	if out == nil {
		t.Fatalf("Enter in RAIL with empty agent target must propagate (for archived-coord resurrect)")
	}
	if r.Focus.State() != FocusRAIL {
		t.Fatalf("focus must remain RAIL when Enter has no agent target; got %s", r.Focus.State())
	}
}

func TestKeyRouter_CtrlQ_FromAGENTtoRAIL(t *testing.T) {
	r, p, _, _ := newRouter()
	r.Focus.JumpToAGENT()
	r.HandleKey(tcell.NewEventKey(tcell.KeyCtrlQ, 0, tcell.ModCtrl))
	if r.Focus.State() != FocusRAIL {
		t.Fatalf("Ctrl-Q from AGENT: want RAIL, got %s", r.Focus.State())
	}
	if len(p.Calls()) != 0 {
		t.Fatalf("Ctrl-Q must NOT be forwarded as a byte; got %d post calls", len(p.Calls()))
	}
}

func TestKeyRouter_CtrlQ_FromCOORDtoRAIL(t *testing.T) {
	r, _, _, _ := newRouter()
	r.Focus.Advance() // → COORD
	r.HandleKey(tcell.NewEventKey(tcell.KeyCtrlQ, 0, tcell.ModCtrl))
	if r.Focus.State() != FocusRAIL {
		t.Fatalf("Ctrl-Q from COORD: want RAIL, got %s", r.Focus.State())
	}
}

// --- Pane-focus keystroke forwarding ---

func TestKeyRouter_TypingInCOORD_ForwardsRuneByte(t *testing.T) {
	r, p, _, _ := newRouter()
	r.Focus.Advance() // → COORD
	r.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone))
	calls := p.Calls()
	if len(calls) != 1 {
		t.Fatalf("want 1 forwarded byte, got %d", len(calls))
	}
	if calls[0].TaskID != "coord-1" {
		t.Fatalf("forwarded TaskID: want coord-1, got %s", calls[0].TaskID)
	}
	if string(calls[0].Payload) != "x" {
		t.Fatalf("forwarded payload: want 'x', got %q", calls[0].Payload)
	}
}

func TestKeyRouter_TypingInAGENT_ForwardsToAgent(t *testing.T) {
	r, p, _, _ := newRouter()
	r.Focus.JumpToAGENT()
	r.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'q', tcell.ModNone))
	calls := p.Calls()
	if len(calls) != 1 {
		t.Fatalf("want 1 forwarded byte, got %d", len(calls))
	}
	if calls[0].TaskID != "agent-1" {
		t.Fatalf("forwarded TaskID: want agent-1, got %s", calls[0].TaskID)
	}
}

// Enter in pane focus forwards a CR (PTY convention), not a focus event.
func TestKeyRouter_EnterInCOORD_ForwardsCR(t *testing.T) {
	r, p, _, _ := newRouter()
	r.Focus.Advance() // → COORD
	r.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	calls := p.Calls()
	if len(calls) != 1 || string(calls[0].Payload) != "\r" {
		t.Fatalf("Enter in COORD must forward CR; got %d calls, payload=%q", len(calls), payloadOf(calls))
	}
	if r.Focus.State() != FocusCOORD {
		t.Fatalf("Enter in COORD must not change focus; got %s", r.Focus.State())
	}
}

func TestKeyRouter_BackspaceInAGENT_Forwards7F(t *testing.T) {
	r, p, _, _ := newRouter()
	r.Focus.JumpToAGENT()
	r.HandleKey(tcell.NewEventKey(tcell.KeyBackspace2, 0, tcell.ModNone))
	calls := p.Calls()
	if len(calls) != 1 || len(calls[0].Payload) != 1 || calls[0].Payload[0] != 0x7f {
		t.Fatalf("Backspace must forward 0x7f; got %d calls, payload=%v", len(calls), payloadOf(calls))
	}
}

func TestKeyRouter_ArrowInPane_ForwardsCSI(t *testing.T) {
	r, p, _, _ := newRouter()
	r.Focus.JumpToAGENT()
	// Bare arrow (no Ctrl/Meta) goes to the PTY as CSI escape.
	r.HandleKey(tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone))
	calls := p.Calls()
	if len(calls) != 1 || string(calls[0].Payload) != "\x1b[A" {
		t.Fatalf("Up in pane focus must forward CSI A; got %d calls, payload=%q", len(calls), payloadOf(calls))
	}
}

// TestKeyRouter_AltRune_ForwardsEscPrefixed proves that Alt+rune in pane
// focus is forwarded as ESC + the rune byte (the xterm convention for Meta
// keys), not dropped and not sent without the ESC prefix.
func TestKeyRouter_AltRune_ForwardsEscPrefixed(t *testing.T) {
	r, p, _, _ := newRouter()
	r.Focus.JumpToAGENT()
	r.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModAlt))
	calls := p.Calls()
	if len(calls) != 1 {
		t.Fatalf("Alt+a in pane must forward exactly 1 call; got %d", len(calls))
	}
	want := []byte{0x1b, 'a'}
	if !bytes.Equal(calls[0].Payload, want) {
		t.Fatalf("Alt+a must forward ESC+'a' (0x1b 0x61); got %q", calls[0].Payload)
	}
}

// TestKeyRouter_ShiftEnterInPane_ForwardsEscCR proves that Shift+Enter in
// pane focus is forwarded as ESC+CR (newline-insert) rather than plain CR.
// TUIs such as bubbletea use ESC+CR to insert a newline without submitting.
func TestKeyRouter_ShiftEnterInPane_ForwardsEscCR(t *testing.T) {
	r, p, _, _ := newRouter()
	r.Focus.JumpToAGENT()
	r.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModShift))
	calls := p.Calls()
	if len(calls) != 1 {
		t.Fatalf("Shift+Enter in pane must forward exactly 1 call; got %d", len(calls))
	}
	want := []byte{0x1b, '\r'}
	if !bytes.Equal(calls[0].Payload, want) {
		t.Fatalf("Shift+Enter must forward ESC+CR; got %q", calls[0].Payload)
	}
}

// TestKeyRouter_AltBackspaceInPane_ForwardsEscDEL proves that Alt+Backspace
// in pane focus is forwarded as ESC+DEL (word-delete) rather than plain DEL.
func TestKeyRouter_AltBackspaceInPane_ForwardsEscDEL(t *testing.T) {
	r, p, _, _ := newRouter()
	r.Focus.JumpToAGENT()
	r.HandleKey(tcell.NewEventKey(tcell.KeyBackspace2, 0, tcell.ModAlt))
	calls := p.Calls()
	if len(calls) != 1 {
		t.Fatalf("Alt+Backspace in pane must forward exactly 1 call; got %d", len(calls))
	}
	want := []byte{0x1b, 0x7f}
	if !bytes.Equal(calls[0].Payload, want) {
		t.Fatalf("Alt+Backspace must forward ESC+DEL; got %q", calls[0].Payload)
	}
}

func TestKeyRouter_NoAgentTaskID_DropsKeystroke(t *testing.T) {
	r, p, _, _ := newRouter()
	r.Targets = &fakeTargets{coord: "", agent: ""}
	r.Focus.JumpToAGENT()
	r.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'z', tcell.ModNone))
	if len(p.Calls()) != 0 {
		t.Fatalf("missing task ID must drop keystroke, not forward; got %d calls", len(p.Calls()))
	}
}

// failingPoster always errors so we can assert the router surfaces (rather than
// swallows) a failed forward to the pane's PTY — the diagnostic gap behind the
// reported E1 "focus moves into the pane but keystrokes never echo, with no
// error anywhere."
type failingPoster struct {
	calls int
	err   error
}

func (f *failingPoster) PostTaskInput(_ context.Context, _ string, _ []byte) (int, error) {
	f.calls++
	return 0, f.err
}

// TestKeyRouter_ForwardFailure_IsLogged_NotSwallowed proves that when the
// forward POST to a focused pane's PTY input endpoint fails, the router logs a
// warning carrying the focus, task id, and error — instead of silently
// dropping it. Before the fix the error was discarded (`_, _ = …`), so a key
// that never reached the agent left no trace; this test fails in that world.
func TestKeyRouter_ForwardFailure_IsLogged_NotSwallowed(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	r, _, _, _ := newRouter()
	fp := &failingPoster{err: errors.New("argus down")}
	r.Poster = fp
	r.Log = logger
	r.Targets = &fakeTargets{coord: "coord-1", agent: "agent-1"}
	r.Focus.JumpToAGENT()

	r.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'Z', tcell.ModNone))

	if fp.calls != 1 {
		t.Fatalf("router must attempt the forward exactly once; got %d", fp.calls)
	}
	out := buf.String()
	if !strings.Contains(out, "forward keystroke to pane PTY failed") {
		t.Fatalf("forward failure must be logged, not swallowed; log was: %q", out)
	}
	if !strings.Contains(out, "agent-1") || !strings.Contains(out, "argus down") {
		t.Fatalf("forward-failure log must carry task id + error; log was: %q", out)
	}
}

// TestKeyRouter_ForwardSuccess_NotLoggedAsFailure guards against noisy logging:
// a successful forward (the common case) must NOT emit the failure warning.
func TestKeyRouter_ForwardSuccess_NotLoggedAsFailure(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	r, p, _, _ := newRouter()
	r.Log = logger
	r.Focus.JumpToAGENT()
	r.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'Z', tcell.ModNone))

	if len(p.Calls()) != 1 {
		t.Fatalf("want 1 forwarded byte, got %d", len(p.Calls()))
	}
	if strings.Contains(buf.String(), "forward keystroke to pane PTY failed") {
		t.Fatalf("successful forward must NOT log a failure; log was: %q", buf.String())
	}
}

// blockingForwarder blocks forever in Enqueue unless released, so a router
// test can prove handlePane returns without waiting on the forward path.
type blockingForwarder struct {
	entered chan struct{}
	release chan struct{}
}

func (b *blockingForwarder) Enqueue(taskID string, payload []byte) {
	close(b.entered)
	<-b.release
}

// recordingForwarder records Enqueue calls so a router test can assert the
// router enqueues (rather than POSTs synchronously) when a forwarder is wired.
type recordingForwarder struct {
	mu    sync.Mutex
	calls []postCall
}

func (r *recordingForwarder) Enqueue(taskID string, payload []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, postCall{TaskID: taskID, Payload: append([]byte(nil), payload...)})
}

func (r *recordingForwarder) Calls() []postCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]postCall, len(r.calls))
	copy(out, r.calls)
	return out
}

// TestKeyRouter_Forward_EnqueuesNotSynchronousPost proves that when an async
// Forward is wired, handlePane enqueues the bytes (and does NOT call the
// synchronous Poster directly).
func TestKeyRouter_Forward_EnqueuesNotSynchronousPost(t *testing.T) {
	r, p, _, _ := newRouter()
	fwd := &recordingForwarder{}
	r.Forward = fwd
	r.Focus.JumpToAGENT()

	r.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone))

	calls := fwd.Calls()
	if len(calls) != 1 || calls[0].TaskID != "agent-1" || string(calls[0].Payload) != "x" {
		t.Fatalf("router must enqueue 'x' for agent-1; got %+v", calls)
	}
	if len(p.Calls()) != 0 {
		t.Fatalf("with a Forward wired the router must NOT call the synchronous Poster; got %d", len(p.Calls()))
	}
}

// TestKeyRouter_Forward_NonBlocking proves HandleKey returns promptly even when
// the wired forwarder's Enqueue is stuck — the tview input-handler goroutine
// must never block on the forward path.
func TestKeyRouter_Forward_NonBlocking(t *testing.T) {
	r, _, _, _ := newRouter()
	bf := &blockingForwarder{entered: make(chan struct{}), release: make(chan struct{})}
	r.Forward = bf
	r.Focus.JumpToAGENT()

	done := make(chan struct{})
	go func() {
		r.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone))
		close(done)
	}()

	// A correct forwarder Enqueue never blocks; this fake blocks deliberately,
	// proving the test would catch a router that blocks the event loop on the
	// forward path. With a non-blocking Enqueue (the production PaneForwarder)
	// HandleKey returns immediately.
	<-bf.entered
	close(bf.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("HandleKey did not return after Enqueue unblocked")
	}
}

// --- Mutation key gating ---

func TestKeyRouter_MutationKey_n_InRAIL_FiresHandler(t *testing.T) {
	r, p, m, _ := newRouter()
	r.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'n', tcell.ModNone))
	if m.new != 1 {
		t.Fatalf("n in RAIL must fire OnNew; got count %d", m.new)
	}
	if len(p.Calls()) != 0 {
		t.Fatalf("n in RAIL must NOT be forwarded as byte; got %d calls", len(p.Calls()))
	}
}

func TestKeyRouter_MutationKey_r_InRAIL_FiresHandler(t *testing.T) {
	r, _, m, _ := newRouter()
	r.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'r', tcell.ModNone))
	if m.rename != 1 {
		t.Fatalf("r in RAIL must fire OnRename; got count %d", m.rename)
	}
}

func TestKeyRouter_MutationKey_CtrlD_InRAIL_FiresHandler(t *testing.T) {
	r, _, m, _ := newRouter()
	r.HandleKey(tcell.NewEventKey(tcell.KeyCtrlD, 0, tcell.ModCtrl))
	if m.del != 1 {
		t.Fatalf("Ctrl-D in RAIL must fire OnDelete; got count %d", m.del)
	}
}

func TestKeyRouter_MutationKey_a_InRAIL_FiresHandler(t *testing.T) {
	r, _, m, _ := newRouter()
	r.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModNone))
	if m.archive != 1 {
		t.Fatalf("a in RAIL must fire OnArchive; got count %d", m.archive)
	}
}

func TestKeyRouter_MutationKey_l_InRAIL_FiresHandler(t *testing.T) {
	r, _, m, _ := newRouter()
	r.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'l', tcell.ModNone))
	if m.listAll != 1 {
		t.Fatalf("l in RAIL must fire OnListAll; got count %d", m.listAll)
	}
}

func TestKeyRouter_MutationKey_Question_InRAIL_FiresHandler(t *testing.T) {
	r, _, m, _ := newRouter()
	r.HandleKey(tcell.NewEventKey(tcell.KeyRune, '?', tcell.ModNone))
	if m.help != 1 {
		t.Fatalf("? in RAIL must fire OnHelp; got count %d", m.help)
	}
}

// Mutation keys are NO-OPs (in the mutation sense) in pane focus — they
// fall through to byte forwarding.
func TestKeyRouter_MutationKey_n_InCOORD_NoMutationOnlyForward(t *testing.T) {
	r, p, m, _ := newRouter()
	r.Focus.Advance() // → COORD
	r.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'n', tcell.ModNone))
	if m.new != 0 {
		t.Fatalf("n in COORD must NOT fire OnNew; got count %d", m.new)
	}
	calls := p.Calls()
	if len(calls) != 1 || string(calls[0].Payload) != "n" || calls[0].TaskID != "coord-1" {
		t.Fatalf("n in COORD must forward 'n' to coord task; got %d calls, payload=%q taskid=%v", len(calls), payloadOf(calls), taskIDsOf(calls))
	}
}

func TestKeyRouter_MutationKey_r_InAGENT_NoMutationOnlyForward(t *testing.T) {
	r, p, m, _ := newRouter()
	r.Focus.JumpToAGENT()
	r.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'r', tcell.ModNone))
	if m.rename != 0 {
		t.Fatalf("r in AGENT must NOT fire OnRename; got count %d", m.rename)
	}
	calls := p.Calls()
	if len(calls) != 1 || string(calls[0].Payload) != "r" || calls[0].TaskID != "agent-1" {
		t.Fatalf("r in AGENT must forward 'r' to agent task; got %d calls, payload=%q taskid=%v", len(calls), payloadOf(calls), taskIDsOf(calls))
	}
}

func TestKeyRouter_MutationKey_Question_InAGENT_NoMutationOnlyForward(t *testing.T) {
	r, p, m, _ := newRouter()
	r.Focus.JumpToAGENT()
	r.HandleKey(tcell.NewEventKey(tcell.KeyRune, '?', tcell.ModNone))
	if m.help != 0 {
		t.Fatalf("? in AGENT must NOT fire OnHelp; got count %d", m.help)
	}
	calls := p.Calls()
	if len(calls) != 1 || string(calls[0].Payload) != "?" {
		t.Fatalf("? in AGENT must forward '?' to agent task; got %d calls, payload=%q", len(calls), payloadOf(calls))
	}
}

// `^d`/`^r`/`^p` are RAIL-focus-only (reversed from the earlier any-focus
// decision): in COORD/AGENT they forward their control byte to the bound PTY
// (Ctrl-D=0x04) and do NOT fire the mutation, so an agent gets EOF normally.
func TestKeyRouter_MutationKey_CtrlD_InCOORD_ForwardsByteNotDelete(t *testing.T) {
	r, p, m, _ := newRouter()
	r.Focus.Advance() // → COORD
	r.HandleKey(tcell.NewEventKey(tcell.KeyCtrlD, 0, tcell.ModCtrl))
	if m.del != 0 {
		t.Fatalf("Ctrl-D in COORD must NOT fire OnDelete (RAIL-only); got count %d", m.del)
	}
	calls := p.Calls()
	if len(calls) != 1 || len(calls[0].Payload) != 1 || calls[0].Payload[0] != 0x04 || calls[0].TaskID != "coord-1" {
		t.Fatalf("Ctrl-D in COORD must forward 0x04 to the coord task; got %d calls, payload=%v", len(calls), payloadOf(calls))
	}
}

// --- Rail navigation keys propagate (so tview's tree can handle them) ---

func TestKeyRouter_RailNavKey_J_PropagatesAsKeyDown(t *testing.T) {
	r, p, m, _ := newRouter()
	out := r.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone))
	if out == nil {
		t.Fatalf("j in RAIL must propagate so the tree can move selection down")
	}
	if out.Key() != tcell.KeyDown {
		t.Fatalf("j in RAIL must propagate as KeyDown (so tree-view tabular navigation kicks in); got %v", out.Key())
	}
	if m.new+m.rename+m.del+m.archive+m.listAll+m.help != 0 {
		t.Fatalf("j must not fire any mutation handler")
	}
	if len(p.Calls()) != 0 {
		t.Fatalf("j in RAIL must not be forwarded to a task")
	}
}

func TestKeyRouter_RailNavKey_K_PropagatesAsKeyUp(t *testing.T) {
	r, _, _, _ := newRouter()
	out := r.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'k', tcell.ModNone))
	if out == nil {
		t.Fatalf("k in RAIL must propagate")
	}
	if out.Key() != tcell.KeyUp {
		t.Fatalf("k in RAIL must propagate as KeyUp; got %v", out.Key())
	}
}

func TestKeyRouter_RailNavKey_DownArrow_Propagates(t *testing.T) {
	r, _, _, _ := newRouter()
	out := r.HandleKey(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone))
	if out == nil {
		t.Fatalf("bare Down in RAIL must propagate")
	}
}

func TestKeyRouter_RailNavKey_UpArrow_Propagates(t *testing.T) {
	r, _, _, _ := newRouter()
	out := r.HandleKey(tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone))
	if out == nil {
		t.Fatalf("bare Up in RAIL must propagate")
	}
}

// --- RailSelectHandler drives Enter target ---

type fakeRailSelect struct {
	calls  int
	target FocusState
}

func (f *fakeRailSelect) OnRailSelectEnter() FocusState {
	f.calls++
	return f.target
}

func TestKeyRouter_EnterInRAIL_WithHandler_AGENT(t *testing.T) {
	r, _, _, b := newRouter()
	sel := &fakeRailSelect{target: FocusAGENT}
	r.RailSelect = sel
	out := r.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	if out != nil {
		t.Fatalf("Enter with handler returning AGENT must be consumed")
	}
	if sel.calls != 1 {
		t.Fatalf("handler must have been invoked once; got %d", sel.calls)
	}
	if r.Focus.State() != FocusAGENT {
		t.Fatalf("Enter+AGENT target: want AGENT, got %s", r.Focus.State())
	}
	if len(b.states) != 1 || b.states[0] != FocusAGENT {
		t.Fatalf("border updater notification: want [AGENT], got %v", b.states)
	}
}

func TestKeyRouter_EnterInRAIL_WithHandler_COORD(t *testing.T) {
	r, _, _, b := newRouter()
	sel := &fakeRailSelect{target: FocusCOORD}
	r.RailSelect = sel
	out := r.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	if out != nil {
		t.Fatalf("Enter with handler returning COORD must be consumed")
	}
	if r.Focus.State() != FocusCOORD {
		t.Fatalf("Enter+COORD target: want COORD, got %s", r.Focus.State())
	}
	if len(b.states) != 1 || b.states[0] != FocusCOORD {
		t.Fatalf("border updater notification: want [COORD], got %v", b.states)
	}
}

func TestKeyRouter_EnterInRAIL_WithHandler_RAIL_Propagates(t *testing.T) {
	r, _, _, b := newRouter()
	sel := &fakeRailSelect{target: FocusRAIL}
	r.RailSelect = sel
	out := r.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	if out == nil {
		t.Fatalf("Enter with handler returning RAIL must propagate so the tree-view folds/unfolds")
	}
	if r.Focus.State() != FocusRAIL {
		t.Fatalf("Enter+RAIL target: focus must remain RAIL, got %s", r.Focus.State())
	}
	if len(b.states) != 0 {
		t.Fatalf("no border update expected when focus did not change; got %v", b.states)
	}
}

// --- Gap 1: Enter consults OnResurrect before pane-entry ---

// When OnResurrect handles the Enter (archived coord + Archive visible), the
// router must consume the event and MUST NOT call the pane-entry handler.
func TestKeyRouter_EnterInRAIL_Resurrect_Handled(t *testing.T) {
	r, _, m, _ := newRouter()
	m.resurrectHandled = true
	sel := &fakeRailSelect{target: FocusCOORD}
	r.RailSelect = sel

	out := r.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))

	if out != nil {
		t.Fatalf("Enter handled by resurrect must be consumed")
	}
	if m.resurrect != 1 {
		t.Fatalf("Enter must consult OnResurrect once; got %d", m.resurrect)
	}
	if sel.calls != 0 {
		t.Fatalf("resurrect-handled Enter must NOT enter a pane; OnRailSelectEnter called %d times", sel.calls)
	}
	if r.Focus.State() != FocusRAIL {
		t.Fatalf("focus must stay RAIL while the resurrect confirm is up; got %s", r.Focus.State())
	}
}

// When OnResurrect declines (returns false), Enter falls through to the
// existing pane-entry handler (no regression on a LIVE coord).
func TestKeyRouter_EnterInRAIL_Resurrect_NotHandled_EntersPane(t *testing.T) {
	r, _, m, _ := newRouter()
	m.resurrectHandled = false
	sel := &fakeRailSelect{target: FocusCOORD}
	r.RailSelect = sel

	out := r.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))

	if out != nil {
		t.Fatalf("Enter entering a pane must be consumed")
	}
	if m.resurrect != 1 {
		t.Fatalf("Enter must consult OnResurrect once; got %d", m.resurrect)
	}
	if sel.calls != 1 {
		t.Fatalf("declined resurrect must fall through to OnRailSelectEnter; got %d calls", sel.calls)
	}
	if r.Focus.State() != FocusCOORD {
		t.Fatalf("live coord Enter must enter the COORD pane; got %s", r.Focus.State())
	}
}

// --- Focus-traversal keys never forwarded as bytes ---

func TestKeyRouter_CtrlLeft_InAGENT_NotForwarded(t *testing.T) {
	r, p, _, _ := newRouter()
	r.Focus.JumpToAGENT()
	r.HandleKey(tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModCtrl))
	if len(p.Calls()) != 0 {
		t.Fatalf("Ctrl-Left must not be forwarded as bytes; got %d calls", len(p.Calls()))
	}
	if r.Focus.State() != FocusCOORD {
		t.Fatalf("Ctrl-Left from AGENT must move focus to COORD; got %s", r.Focus.State())
	}
}

func TestKeyRouter_CtrlRight_InCOORD_NotForwarded(t *testing.T) {
	r, p, _, _ := newRouter()
	r.Focus.Advance() // → COORD
	r.HandleKey(tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModCtrl))
	if len(p.Calls()) != 0 {
		t.Fatalf("Ctrl-Right must not be forwarded as bytes; got %d calls", len(p.Calls()))
	}
	if r.Focus.State() != FocusAGENT {
		t.Fatalf("Ctrl-Right from COORD must move focus to AGENT; got %s", r.Focus.State())
	}
}

// --- Key-surrender contract: Esc release + ? help frame (D12) ---

// Esc while focus is RAIL hands the keyboard back to argus via a release
// frame; the Esc byte MUST NOT be forwarded to any task.
func TestKeyRouter_Esc_InRAIL_SendsRelease_NotForwarded(t *testing.T) {
	r, p, c := newRouterWithControl()
	out := r.HandleKey(tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone))
	if out != nil {
		t.Fatalf("Esc in RAIL must be consumed; got non-nil event")
	}
	if c.releases != 1 {
		t.Fatalf("Esc in RAIL must send exactly one release frame; got %d", c.releases)
	}
	if len(p.Calls()) != 0 {
		t.Fatalf("Esc in RAIL must NOT be forwarded to a task; got %d calls", len(p.Calls()))
	}
}

// Esc while focus is AGENT (or COORD) is forwarded to the bound PTY verbatim
// (0x1b) and MUST NOT release the view.
func TestKeyRouter_Esc_InAGENT_ForwardedToPTY_NoRelease(t *testing.T) {
	r, p, c := newRouterWithControl()
	r.Focus.JumpToAGENT()
	r.HandleKey(tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone))
	if c.releases != 0 {
		t.Fatalf("Esc in AGENT must NOT release the view; got %d releases", c.releases)
	}
	calls := p.Calls()
	if len(calls) != 1 || len(calls[0].Payload) != 1 || calls[0].Payload[0] != 0x1b || calls[0].TaskID != "agent-1" {
		t.Fatalf("Esc in AGENT must forward 0x1b to the agent task; got %d calls, payload=%v", len(calls), payloadOf(calls))
	}
}

func TestKeyRouter_Esc_InCOORD_ForwardedToPTY_NoRelease(t *testing.T) {
	r, p, c := newRouterWithControl()
	r.Focus.Advance() // → COORD
	r.HandleKey(tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone))
	if c.releases != 0 {
		t.Fatalf("Esc in COORD must NOT release the view; got %d releases", c.releases)
	}
	calls := p.Calls()
	if len(calls) != 1 || len(calls[0].Payload) != 1 || calls[0].Payload[0] != 0x1b || calls[0].TaskID != "coord-1" {
		t.Fatalf("Esc in COORD must forward 0x1b to the coord task; got %d calls, payload=%v", len(calls), payloadOf(calls))
	}
}

// ? while focus is RAIL pops argus's help overlay via a help frame and MUST
// NOT be forwarded to a task. (It routes through the mutation handler's
// OnHelp; with a control sender wired, the bridge sends the frame — but at the
// router level we still confirm ? in RAIL is consumed and not forwarded.)
func TestKeyRouter_Question_InRAIL_NotForwarded(t *testing.T) {
	r, p, _ := newRouterWithControl()
	out := r.HandleKey(tcell.NewEventKey(tcell.KeyRune, '?', tcell.ModNone))
	if out != nil {
		t.Fatalf("? in RAIL must be consumed; got non-nil event")
	}
	if len(p.Calls()) != 0 {
		t.Fatalf("? in RAIL must NOT be forwarded to a task; got %d calls", len(p.Calls()))
	}
}

// --- Border updates fire on focus change ---

func TestKeyRouter_BorderUpdates_OnEveryFocusChange(t *testing.T) {
	r, _, _, b := newRouter()
	r.HandleKey(tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModCtrl)) // → COORD
	r.HandleKey(tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModCtrl)) // → AGENT
	r.HandleKey(tcell.NewEventKey(tcell.KeyCtrlQ, 0, tcell.ModCtrl)) // → RAIL
	want := []FocusState{FocusCOORD, FocusAGENT, FocusRAIL}
	if len(b.states) != len(want) {
		t.Fatalf("border updater: want %d notifications, got %d (%v)", len(want), len(b.states), b.states)
	}
	for i, s := range want {
		if b.states[i] != s {
			t.Fatalf("border update %d: want %s, got %s", i, s, b.states[i])
		}
	}
}

func TestKeyRouter_NoBorderUpdate_OnNonFocusKey(t *testing.T) {
	r, _, _, b := newRouter()
	r.Focus.Advance() // → COORD
	b.states = nil
	r.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone))
	if len(b.states) != 0 {
		t.Fatalf("forwarding a typed byte must not notify the border updater; got %v", b.states)
	}
}

// --- Modal gate ---

type fakeModalGate struct{ active bool }

func (f *fakeModalGate) IsModalActive() bool { return f.active }

func TestKeyRouter_ModalActive_YieldsAllEvents(t *testing.T) {
	r, p, m, b := newRouter()
	gate := &fakeModalGate{active: true}
	r.Modal = gate

	cases := []*tcell.EventKey{
		tcell.NewEventKey(tcell.KeyRune, 'n', tcell.ModNone),
		tcell.NewEventKey(tcell.KeyRune, 'r', tcell.ModNone),
		tcell.NewEventKey(tcell.KeyCtrlD, 0, tcell.ModCtrl),
		tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModCtrl),
		tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone),
		tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone),
	}
	for _, ev := range cases {
		out := r.HandleKey(ev)
		if out != ev {
			t.Fatalf("modal-active must yield event %v unchanged; got %v", ev, out)
		}
	}
	if m.new+m.rename+m.del+m.archive+m.listAll+m.help != 0 {
		t.Fatalf("no mutation should fire while modal is active")
	}
	if len(p.Calls()) != 0 {
		t.Fatalf("no byte forwarding while modal is active; got %d calls", len(p.Calls()))
	}
	if len(b.states) != 0 {
		t.Fatalf("no focus changes while modal is active; got %v", b.states)
	}
}

func TestKeyRouter_ModalInactive_NormalDispatch(t *testing.T) {
	r, _, m, _ := newRouter()
	r.Modal = &fakeModalGate{active: false}
	r.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'n', tcell.ModNone))
	if m.new != 1 {
		t.Fatalf("modal-inactive must let n fire OnNew; got count %d", m.new)
	}
}

// --- Stage P extended keyset routing ---

// `s`/`S` advance/revert status; RAIL-focus-only, intercepted (not forwarded).
func TestKeyRouter_StatusKeys_RailFocus_FireMutation(t *testing.T) {
	r, p, m, _ := newRouter()
	// Focus starts RAIL.
	if out := r.HandleKey(tcell.NewEventKey(tcell.KeyRune, 's', tcell.ModNone)); out != nil {
		t.Fatalf("s in RAIL must be consumed; got %v", out)
	}
	if out := r.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'S', tcell.ModNone)); out != nil {
		t.Fatalf("S in RAIL must be consumed; got %v", out)
	}
	if m.statusAdvance != 1 || m.statusRevert != 1 {
		t.Fatalf("status calls: advance=%d revert=%d, want 1/1", m.statusAdvance, m.statusRevert)
	}
	if len(p.Calls()) != 0 {
		t.Fatalf("status keys in RAIL must not forward to PTY; got %v", p.Calls())
	}
}

// `P` toggles pin; RAIL-focus-only, intercepted (not forwarded).
func TestKeyRouter_PinKey_RailFocus_FiresMutation(t *testing.T) {
	r, p, m, _ := newRouter()
	if out := r.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'P', tcell.ModNone)); out != nil {
		t.Fatalf("P in RAIL must be consumed; got %v", out)
	}
	if m.pin != 1 {
		t.Fatalf("P in RAIL must fire OnPin once; got %d", m.pin)
	}
	if len(p.Calls()) != 0 {
		t.Fatalf("P in RAIL must not forward to PTY; got %v", p.Calls())
	}
}

// In a pane, `P` is ordinary input forwarded to the PTY (not the pin key).
func TestKeyRouter_PinKey_PaneFocus_ForwardToPTY(t *testing.T) {
	r, p, m, _ := newRouter()
	r.Focus.JumpToAGENT()
	r.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'P', tcell.ModNone))
	if m.pin != 0 {
		t.Fatalf("P in pane must NOT fire OnPin; got %d", m.pin)
	}
	calls := p.Calls()
	if len(calls) != 1 || string(calls[0].Payload) != "P" || calls[0].TaskID != "agent-1" {
		t.Fatalf("P in AGENT must forward byte to agent task; got %+v", calls)
	}
}

// `J` adopts a freelancer; RAIL-focus-only, intercepted (not forwarded).
func TestKeyRouter_AdoptKey_RailFocus_FiresMutation(t *testing.T) {
	r, p, m, _ := newRouter()
	if out := r.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'J', tcell.ModNone)); out != nil {
		t.Fatalf("J in RAIL must be consumed; got %v", out)
	}
	if m.adopt != 1 {
		t.Fatalf("J in RAIL must fire OnAdopt once; got %d", m.adopt)
	}
	if len(p.Calls()) != 0 {
		t.Fatalf("J in RAIL must not forward to PTY; got %v", p.Calls())
	}
}

// Lowercase `j` is nav-down (KeyDown), NOT the adopt key.
func TestKeyRouter_LowerJ_RailFocus_IsNavNotAdopt(t *testing.T) {
	r, _, m, _ := newRouter()
	out := r.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone))
	if m.adopt != 0 {
		t.Fatalf("lowercase j must NOT fire OnAdopt; got %d", m.adopt)
	}
	if out == nil || out.Key() != tcell.KeyDown {
		t.Fatalf("lowercase j must translate to KeyDown; got %v", out)
	}
}

// In a pane, `J` is ordinary input forwarded to the PTY (not the adopt key).
func TestKeyRouter_AdoptKey_PaneFocus_ForwardToPTY(t *testing.T) {
	r, p, m, _ := newRouter()
	r.Focus.JumpToAGENT()
	r.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'J', tcell.ModNone))
	if m.adopt != 0 {
		t.Fatalf("J in pane must NOT fire OnAdopt; got %d", m.adopt)
	}
	calls := p.Calls()
	if len(calls) != 1 || string(calls[0].Payload) != "J" || calls[0].TaskID != "agent-1" {
		t.Fatalf("J in AGENT must forward byte to agent task; got %+v", calls)
	}
}

// In a pane, `s`/`S` are ordinary input forwarded to the PTY (not status keys).
func TestKeyRouter_StatusKeys_PaneFocus_ForwardToPTY(t *testing.T) {
	r, p, m, _ := newRouter()
	r.Focus.JumpToAGENT()
	r.HandleKey(tcell.NewEventKey(tcell.KeyRune, 's', tcell.ModNone))
	if m.statusAdvance != 0 {
		t.Fatalf("s in pane must NOT fire OnStatusAdvance; got %d", m.statusAdvance)
	}
	calls := p.Calls()
	if len(calls) != 1 || string(calls[0].Payload) != "s" || calls[0].TaskID != "agent-1" {
		t.Fatalf("s in AGENT must forward byte to agent task; got %+v", calls)
	}
}

// `^d`/`^r`/`^p` are RAIL-focus-only: in RAIL they fire the mutation and are
// intercepted; in a pane they forward their control byte to the bound PTY and
// do NOT fire the mutation. This table drives all three verbs at once.
func TestKeyRouter_DestructiveVerbs_RailOnly_FireInRailForwardInPane(t *testing.T) {
	type vc struct {
		name    string
		key     tcell.Key
		ctlByte byte
		count   func(*fakeMutations) int
	}
	cases := []vc{
		{"^d delete", tcell.KeyCtrlD, 0x04, func(m *fakeMutations) int { return m.del }},
		{"^r prune", tcell.KeyCtrlR, 0x12, func(m *fakeMutations) int { return m.prune }},
		{"^p open-PR", tcell.KeyCtrlP, 0x10, func(m *fakeMutations) int { return m.openPR }},
	}

	// In RAIL: fires the mutation, intercepted (not forwarded).
	for _, c := range cases {
		t.Run(c.name+"/RAIL", func(t *testing.T) {
			r, p, m, _ := newRouter()
			if out := r.HandleKey(tcell.NewEventKey(c.key, 0, tcell.ModCtrl)); out != nil {
				t.Fatalf("%s in RAIL must be consumed; got %v", c.name, out)
			}
			if c.count(m) != 1 {
				t.Fatalf("%s in RAIL must fire its mutation; got count %d", c.name, c.count(m))
			}
			if len(p.Calls()) != 0 {
				t.Fatalf("%s in RAIL must NOT forward to PTY; got %v", c.name, p.Calls())
			}
		})
	}

	// In COORD/AGENT: forwards the control byte to the bound PTY, no mutation.
	for _, c := range cases {
		for _, pane := range []struct {
			name   string
			jump   func(*KeyRouter)
			taskID string
		}{
			{"COORD", func(r *KeyRouter) { r.Focus.Advance() }, "coord-1"},
			{"AGENT", func(r *KeyRouter) { r.Focus.JumpToAGENT() }, "agent-1"},
		} {
			t.Run(c.name+"/"+pane.name, func(t *testing.T) {
				r, p, m, _ := newRouter()
				pane.jump(r)
				if out := r.HandleKey(tcell.NewEventKey(c.key, 0, tcell.ModCtrl)); out != nil {
					t.Fatalf("%s in %s must be consumed; got %v", c.name, pane.name, out)
				}
				if c.count(m) != 0 {
					t.Fatalf("%s in %s must NOT fire its mutation (RAIL-only); got count %d", c.name, pane.name, c.count(m))
				}
				calls := p.Calls()
				if len(calls) != 1 || len(calls[0].Payload) != 1 || calls[0].Payload[0] != c.ctlByte || calls[0].TaskID != pane.taskID {
					t.Fatalf("%s in %s must forward byte 0x%02x to %s; got %d calls payload=%v", c.name, pane.name, c.ctlByte, pane.taskID, len(calls), payloadOf(calls))
				}
			})
		}
	}
}

// --- Stage Q: pane scroll + in-pane agent navigation (D15) ---

// fakeScroller records ScrollFocusedPane calls so router tests can assert the
// ⇧↑/⇧↓ scroll keys reach the focused pane without moving the rail selection.
type fakeScroller struct {
	calls []scrollCall
}

type scrollCall struct {
	State FocusState
	Delta int
}

func (f *fakeScroller) ScrollFocusedPane(state FocusState, delta int) {
	f.calls = append(f.calls, scrollCall{State: state, Delta: delta})
}

// fakeInPaneNav records InPaneNavigate calls and returns a programmable focus
// state so router tests can assert ⌘↑/⌘↓ (and ^↑/^↓) move the selection while
// keeping focus inside a pane.
type fakeInPaneNav struct {
	dirs   []int
	result FocusState
}

func (f *fakeInPaneNav) InPaneNavigate(dir int) FocusState {
	f.dirs = append(f.dirs, dir)
	return f.result
}

// ⇧↓ / ⇧↑ in a pane scroll the focused pane and do NOT move the rail
// selection (no InPaneNavigate call) nor forward a byte to the PTY.
func TestKeyRouter_ShiftArrows_InPane_ScrollNotNavNotForward(t *testing.T) {
	for _, tc := range []struct {
		name  string
		key   tcell.Key
		delta int
	}{
		{"shift-up", tcell.KeyUp, +1},
		{"shift-down", tcell.KeyDown, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, p, _, _ := newRouter()
			sc := &fakeScroller{}
			nav := &fakeInPaneNav{result: FocusAGENT}
			r.Scroller = sc
			r.InPaneNav = nav
			r.Focus.JumpToAGENT()

			out := r.HandleKey(tcell.NewEventKey(tc.key, 0, tcell.ModShift))
			if out != nil {
				t.Fatalf("⇧arrow in pane must be consumed; got %v", out)
			}
			if len(sc.calls) != 1 || sc.calls[0].Delta != tc.delta {
				t.Fatalf("⇧arrow must scroll focused pane delta=%d; got %+v", tc.delta, sc.calls)
			}
			if sc.calls[0].State != FocusAGENT {
				t.Fatalf("scroll must target the focused pane state AGENT; got %v", sc.calls[0].State)
			}
			if len(nav.dirs) != 0 {
				t.Fatalf("⇧arrow must NOT move the rail selection; got nav dirs %v", nav.dirs)
			}
			if len(p.Calls()) != 0 {
				t.Fatalf("⇧arrow must NOT forward a byte to the PTY; got %v", p.Calls())
			}
			if r.Focus.State() != FocusAGENT {
				t.Fatalf("⇧arrow must not change focus; got %s", r.Focus.State())
			}
		})
	}
}

// ⇧arrows in RAIL focus are NOT scroll keys — there is no focused pane. They
// fall through to the rail (propagate as the bare arrow for tree movement).
func TestKeyRouter_ShiftArrows_InRAIL_NoScroll(t *testing.T) {
	r, _, _, _ := newRouter()
	sc := &fakeScroller{}
	r.Scroller = sc
	// Focus starts RAIL.
	r.HandleKey(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModShift))
	if len(sc.calls) != 0 {
		t.Fatalf("⇧arrow in RAIL must not scroll a pane; got %+v", sc.calls)
	}
}

// ⌘↓ / ^↓ (and up) in a pane move the rail selection to the next/prev agent
// and keep focus inside a pane bound to the new selection — never RAIL, never
// a forwarded byte.
func TestKeyRouter_ModArrows_InPane_NavigateSelectionKeepPaneFocus(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  tcell.Key
		mod  tcell.ModMask
		dir  int
	}{
		{"cmd-down", tcell.KeyDown, tcell.ModMeta, +1},
		{"cmd-up", tcell.KeyUp, tcell.ModMeta, -1},
		{"ctrl-down", tcell.KeyDown, tcell.ModCtrl, +1},
		{"ctrl-up", tcell.KeyUp, tcell.ModCtrl, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, p, _, b := newRouter()
			sc := &fakeScroller{}
			nav := &fakeInPaneNav{result: FocusAGENT}
			r.Scroller = sc
			r.InPaneNav = nav
			r.Focus.Advance() // RAIL → COORD, so we start in a pane other than the target

			out := r.HandleKey(tcell.NewEventKey(tc.key, 0, tc.mod))
			if out != nil {
				t.Fatalf("mod-arrow in pane must be consumed; got %v", out)
			}
			if len(nav.dirs) != 1 || nav.dirs[0] != tc.dir {
				t.Fatalf("mod-arrow must navigate selection dir=%d; got %v", tc.dir, nav.dirs)
			}
			if len(sc.calls) != 0 {
				t.Fatalf("mod-arrow must NOT scroll; got %+v", sc.calls)
			}
			if len(p.Calls()) != 0 {
				t.Fatalf("mod-arrow must NOT forward a byte; got %v", p.Calls())
			}
			if r.Focus.State() != FocusAGENT {
				t.Fatalf("mod-arrow must land focus in the new selection's pane (AGENT); got %s", r.Focus.State())
			}
			if r.Focus.State() == FocusRAIL {
				t.Fatalf("mod-arrow must NOT return focus to RAIL")
			}
			// The border repaints because the focus state changed to the new
			// selection's pane.
			if len(b.states) == 0 || b.states[len(b.states)-1] != FocusAGENT {
				t.Fatalf("border must be repainted to AGENT after in-pane nav; got %v", b.states)
			}
		})
	}
}

// In RAIL focus, ⌘/^ arrows are not in-pane nav — they fall through (RAIL
// already navigates the selection with bare j/k/arrows).
func TestKeyRouter_ModArrows_InRAIL_NoInPaneNav(t *testing.T) {
	r, _, _, _ := newRouter()
	nav := &fakeInPaneNav{result: FocusAGENT}
	r.InPaneNav = nav
	r.HandleKey(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModCtrl))
	if len(nav.dirs) != 0 {
		t.Fatalf("⌘/^ arrow in RAIL must not invoke in-pane nav; got %v", nav.dirs)
	}
}

// --- helpers ---

func payloadOf(c []postCall) [][]byte {
	out := make([][]byte, len(c))
	for i, x := range c {
		out[i] = x.Payload
	}
	return out
}

func taskIDsOf(c []postCall) []string {
	out := make([]string, len(c))
	for i, x := range c {
		out[i] = x.TaskID
	}
	return out
}

// hotkeyHas reports whether items contains an entry with the given key, and if
// so whether its Bar flag matches wantBar.
func hotkeyHas(items []HotkeyItem, key string, wantBar bool) bool {
	for _, it := range items {
		if it.Key == key {
			return it.Bar == wantBar
		}
	}
	return false
}

// --- BUG-027: Ctrl-Z pane fullscreen + ladder nav ---

// fakeFullscreen records OnFullscreenChanged calls so router tests can assert
// the fullscreen state transitions without a running tview app.
type fakeFullscreen struct {
	calls []fullscreenCall
}

type fullscreenCall struct {
	Pane   FocusState
	Active bool
}

func (f *fakeFullscreen) OnFullscreenChanged(pane FocusState, active bool) {
	f.calls = append(f.calls, fullscreenCall{Pane: pane, Active: active})
}

func (f *fakeFullscreen) lastCall() (fullscreenCall, bool) {
	if len(f.calls) == 0 {
		return fullscreenCall{}, false
	}
	return f.calls[len(f.calls)-1], true
}

// newRouterWithFullscreen returns a router with a fakeFullscreen wired in.
func newRouterWithFullscreen() (*KeyRouter, *fakeFullscreen) {
	r, _, _, _ := newRouter()
	fs := &fakeFullscreen{}
	r.Fullscreen = fs
	return r, fs
}

// TestKeyRouter_CtrlZ_InCOORD_EntersFullscreen proves that Ctrl-Z while the
// COORD pane has focus activates fullscreen on COORD and notifies the updater.
func TestKeyRouter_CtrlZ_InCOORD_EntersFullscreen(t *testing.T) {
	r, fs := newRouterWithFullscreen()
	r.Focus.Advance() // RAIL → COORD

	out := r.HandleKey(tcell.NewEventKey(tcell.KeyCtrlZ, 0, tcell.ModNone))
	if out != nil {
		t.Fatalf("Ctrl-Z in COORD must be consumed; got non-nil event")
	}
	if r.Focus.State() != FocusCOORD {
		t.Fatalf("Ctrl-Z must keep focus in COORD; got %s", r.Focus.State())
	}
	c, ok := fs.lastCall()
	if !ok {
		t.Fatal("Ctrl-Z in COORD must notify FullscreenUpdater; got no calls")
	}
	if !c.Active {
		t.Fatalf("fullscreen must be active after Ctrl-Z in COORD; got active=%v", c.Active)
	}
	if c.Pane != FocusCOORD {
		t.Fatalf("fullscreen pane must be COORD; got %s", c.Pane)
	}
}

// TestKeyRouter_CtrlZ_InAGENT_EntersFullscreen proves that Ctrl-Z while the
// AGENT pane has focus activates fullscreen on AGENT.
func TestKeyRouter_CtrlZ_InAGENT_EntersFullscreen(t *testing.T) {
	r, fs := newRouterWithFullscreen()
	r.Focus.JumpToAGENT()

	r.HandleKey(tcell.NewEventKey(tcell.KeyCtrlZ, 0, tcell.ModNone))

	c, ok := fs.lastCall()
	if !ok {
		t.Fatal("Ctrl-Z in AGENT must notify FullscreenUpdater; got no calls")
	}
	if !c.Active || c.Pane != FocusAGENT {
		t.Fatalf("fullscreen must be active on AGENT; got active=%v pane=%s", c.Active, c.Pane)
	}
}

// TestKeyRouter_CtrlZ_InCOORDFullscreen_ExitsFullscreen proves that a second
// Ctrl-Z while fullscreen is active exits fullscreen.
func TestKeyRouter_CtrlZ_InCOORDFullscreen_ExitsFullscreen(t *testing.T) {
	r, fs := newRouterWithFullscreen()
	r.Focus.Advance()                                                // → COORD
	r.HandleKey(tcell.NewEventKey(tcell.KeyCtrlZ, 0, tcell.ModNone)) // enter fullscreen
	fs.calls = nil                                                   // reset

	r.HandleKey(tcell.NewEventKey(tcell.KeyCtrlZ, 0, tcell.ModNone)) // exit fullscreen

	c, ok := fs.lastCall()
	if !ok {
		t.Fatal("second Ctrl-Z must notify FullscreenUpdater; got no calls")
	}
	if c.Active {
		t.Fatalf("fullscreen must be inactive after second Ctrl-Z; got active=%v", c.Active)
	}
}

// TestKeyRouter_CtrlZ_InRAIL_ConsumedNoOp proves that Ctrl-Z in RAIL focus
// is consumed (returned nil) but does NOT activate fullscreen — the 0x1a byte
// must never reach a PTY or widget.
func TestKeyRouter_CtrlZ_InRAIL_ConsumedNoOp(t *testing.T) {
	r, fs := newRouterWithFullscreen()
	// Focus starts RAIL.
	out := r.HandleKey(tcell.NewEventKey(tcell.KeyCtrlZ, 0, tcell.ModNone))
	if out != nil {
		t.Fatalf("Ctrl-Z in RAIL must be consumed; got non-nil event")
	}
	if len(fs.calls) != 0 {
		t.Fatalf("Ctrl-Z in RAIL must NOT activate fullscreen; got calls %+v", fs.calls)
	}
}

// TestKeyRouter_CtrlZ_InPane_NotForwardedToPTY proves that Ctrl-Z in pane
// focus is NEVER forwarded as the SIGTSTP byte to the bound PTY — regardless
// of whether fullscreen becomes active.
func TestKeyRouter_CtrlZ_InPane_NotForwardedToPTY(t *testing.T) {
	r, _ := newRouterWithFullscreen()
	r.Focus.JumpToAGENT()
	r.HandleKey(tcell.NewEventKey(tcell.KeyCtrlZ, 0, tcell.ModNone))
	// The poster is the fakePoster from newRouter; all calls recorded there.
	// We need access to it — rewire.
	r2, p, _, _ := newRouter()
	fs2 := &fakeFullscreen{}
	r2.Fullscreen = fs2
	r2.Focus.JumpToAGENT()
	r2.HandleKey(tcell.NewEventKey(tcell.KeyCtrlZ, 0, tcell.ModNone))
	if len(p.Calls()) != 0 {
		t.Fatalf("Ctrl-Z must NOT be forwarded to PTY; got %d calls payload=%v", len(p.Calls()), payloadOf(p.Calls()))
	}
}

// TestKeyRouter_Fullscreen_CtrlRight_COORDToAGENT proves that Ctrl-Right while
// fullscreen is active on COORD switches to fullscreen AGENT (stays fullscreen).
func TestKeyRouter_Fullscreen_CtrlRight_COORDToAGENT(t *testing.T) {
	r, fs := newRouterWithFullscreen()
	r.Focus.Advance()                                                // → COORD
	r.HandleKey(tcell.NewEventKey(tcell.KeyCtrlZ, 0, tcell.ModNone)) // enter COORD fullscreen
	fs.calls = nil

	out := r.HandleKey(tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModCtrl))
	if out != nil {
		t.Fatalf("Ctrl-Right in fullscreen COORD must be consumed; got %v", out)
	}
	if r.Focus.State() != FocusAGENT {
		t.Fatalf("Ctrl-Right in fullscreen COORD must move focus to AGENT; got %s", r.Focus.State())
	}
	c, ok := fs.lastCall()
	if !ok {
		t.Fatal("Ctrl-Right in fullscreen COORD must notify FullscreenUpdater")
	}
	if !c.Active || c.Pane != FocusAGENT {
		t.Fatalf("must stay fullscreen on AGENT; got active=%v pane=%s", c.Active, c.Pane)
	}
}

// TestKeyRouter_Fullscreen_CtrlRight_AGENTNoOp proves that Ctrl-Right while
// fullscreen is active on AGENT is a no-op (AGENT is the rightmost pane).
func TestKeyRouter_Fullscreen_CtrlRight_AGENTNoOp(t *testing.T) {
	r, fs := newRouterWithFullscreen()
	r.Focus.JumpToAGENT()
	r.HandleKey(tcell.NewEventKey(tcell.KeyCtrlZ, 0, tcell.ModNone)) // enter AGENT fullscreen
	callsBefore := len(fs.calls)

	r.HandleKey(tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModCtrl))

	if r.Focus.State() != FocusAGENT {
		t.Fatalf("Ctrl-Right in fullscreen AGENT must be a no-op; focus changed to %s", r.Focus.State())
	}
	if len(fs.calls) != callsBefore {
		t.Fatalf("Ctrl-Right in fullscreen AGENT must not call FullscreenUpdater; got extra calls")
	}
}

// TestKeyRouter_Fullscreen_CtrlLeft_AGENTToCOORD proves that Ctrl-Left while
// fullscreen is active on AGENT switches to fullscreen COORD (stays fullscreen).
func TestKeyRouter_Fullscreen_CtrlLeft_AGENTToCOORD(t *testing.T) {
	r, fs := newRouterWithFullscreen()
	r.Focus.JumpToAGENT()
	r.HandleKey(tcell.NewEventKey(tcell.KeyCtrlZ, 0, tcell.ModNone)) // enter AGENT fullscreen
	fs.calls = nil

	r.HandleKey(tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModCtrl))

	if r.Focus.State() != FocusCOORD {
		t.Fatalf("Ctrl-Left in fullscreen AGENT must move focus to COORD; got %s", r.Focus.State())
	}
	c, ok := fs.lastCall()
	if !ok {
		t.Fatal("Ctrl-Left in fullscreen AGENT must notify FullscreenUpdater")
	}
	if !c.Active || c.Pane != FocusCOORD {
		t.Fatalf("must stay fullscreen on COORD; got active=%v pane=%s", c.Active, c.Pane)
	}
}

// TestKeyRouter_Fullscreen_CtrlLeft_COORDExitsToRAIL proves that Ctrl-Left
// while fullscreen is active on COORD exits fullscreen and moves focus to RAIL.
func TestKeyRouter_Fullscreen_CtrlLeft_COORDExitsToRAIL(t *testing.T) {
	r, fs := newRouterWithFullscreen()
	r.Focus.Advance()                                                // → COORD
	r.HandleKey(tcell.NewEventKey(tcell.KeyCtrlZ, 0, tcell.ModNone)) // enter COORD fullscreen
	fs.calls = nil

	r.HandleKey(tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModCtrl))

	if r.Focus.State() != FocusRAIL {
		t.Fatalf("Ctrl-Left in fullscreen COORD must exit fullscreen and move to RAIL; got %s", r.Focus.State())
	}
	c, ok := fs.lastCall()
	if !ok {
		t.Fatal("Ctrl-Left in fullscreen COORD must notify FullscreenUpdater")
	}
	if c.Active {
		t.Fatalf("fullscreen must be inactive after Ctrl-Left from COORD fullscreen; got active=%v", c.Active)
	}
}

// TestKeyRouter_Fullscreen_CtrlQ_ExitsFullscreenToRAIL proves that Ctrl-Q while
// fullscreen is active exits fullscreen and returns focus to RAIL.
func TestKeyRouter_Fullscreen_CtrlQ_ExitsFullscreenToRAIL(t *testing.T) {
	r, fs := newRouterWithFullscreen()
	r.Focus.JumpToAGENT()
	r.HandleKey(tcell.NewEventKey(tcell.KeyCtrlZ, 0, tcell.ModNone)) // enter AGENT fullscreen
	fs.calls = nil

	r.HandleKey(tcell.NewEventKey(tcell.KeyCtrlQ, 0, tcell.ModCtrl))

	if r.Focus.State() != FocusRAIL {
		t.Fatalf("Ctrl-Q in fullscreen must exit to RAIL; got %s", r.Focus.State())
	}
	c, ok := fs.lastCall()
	if !ok {
		t.Fatal("Ctrl-Q in fullscreen must notify FullscreenUpdater")
	}
	if c.Active {
		t.Fatalf("fullscreen must be inactive after Ctrl-Q; got active=%v", c.Active)
	}
}

// TestKeyRouter_Fullscreen_NormalKeysStillForwarded proves that while fullscreen
// is active, regular typed keys are still forwarded to the focused pane's PTY.
func TestKeyRouter_Fullscreen_NormalKeysStillForwarded(t *testing.T) {
	r, p, _, _ := newRouter()
	fs := &fakeFullscreen{}
	r.Fullscreen = fs
	r.Focus.JumpToAGENT()
	r.HandleKey(tcell.NewEventKey(tcell.KeyCtrlZ, 0, tcell.ModNone)) // enter fullscreen

	r.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone))

	calls := p.Calls()
	if len(calls) != 1 || string(calls[0].Payload) != "x" || calls[0].TaskID != "agent-1" {
		t.Fatalf("regular keys in fullscreen must forward to PTY; got %+v", calls)
	}
}

// TestHotkeyItems_PanesAdvertiseCtrlZ proves the COORD and AGENT hotkey
// dictionaries include ^Z (fullscreen toggle) so it appears in argus's bottom bar.
func TestHotkeyItems_PanesAdvertiseCtrlZ(t *testing.T) {
	for _, tc := range []struct {
		name  string
		items []HotkeyItem
	}{
		{"COORD", hotkeyItems(FocusCOORD, true)},
		{"AGENT-coordful", hotkeyItems(FocusAGENT, true)},
		{"AGENT-coordless", hotkeyItems(FocusAGENT, false)},
	} {
		if !hotkeyHas(tc.items, "^Z", true) {
			t.Errorf("%s hotkeys must advertise ^Z with bar:true (fullscreen); items=%+v", tc.name, tc.items)
		}
	}
}

// hotkeyContains reports whether items contains an entry with the given key
// (regardless of Bar flag).
func hotkeyContains(items []HotkeyItem, key string) bool {
	for _, it := range items {
		if it.Key == key {
			return true
		}
	}
	return false
}

// TestHotkeyItems_RailAdvertisesPruneAndPR proves the RAIL hotkey dictionary
// surfaces ^r (prune), ^p (PR), s/S (status) so argus's help overlay (D12) can
// list them. They are help-overlay-only (Bar:false) to keep the bottom bar
// uncluttered.
func TestHotkeyItems_RailAdvertisesPruneAndPR(t *testing.T) {
	items := hotkeyItems(FocusRAIL, true)
	for _, key := range []string{"^r", "^p", "s", "S"} {
		if !hotkeyHas(items, key, false) {
			t.Errorf("RAIL hotkeys must advertise %q with bar:false; items=%+v", key, items)
		}
	}
}

// TestHotkeyItems_PaneFocusDropsPruneAndPR proves ^d/^r/^p are NOT advertised
// in the COORD/AGENT hotkey dictionaries: they are RAIL-focus-only, and in a
// pane those control bytes forward to the PTY rather than firing a mutation.
func TestHotkeyItems_PaneFocusDropsPruneAndPR(t *testing.T) {
	for _, tc := range []struct {
		name  string
		items []HotkeyItem
	}{
		{"COORD", hotkeyItems(FocusCOORD, true)},
		{"AGENT-coordful", hotkeyItems(FocusAGENT, true)},
		{"AGENT-coordless", hotkeyItems(FocusAGENT, false)},
	} {
		for _, key := range []string{"^d", "^r", "^p"} {
			if hotkeyContains(tc.items, key) {
				t.Errorf("%s hotkeys must NOT advertise %q (RAIL-only); items=%+v", tc.name, key, tc.items)
			}
		}
	}
}

// TestKeyRouter_MutationKey_w_InRAIL_FiresOnNewWorker proves that `w` in RAIL
// focus fires OnNewWorker (not forwarded as a byte).
func TestKeyRouter_MutationKey_w_InRAIL_FiresOnNewWorker(t *testing.T) {
	r, p, m, _ := newRouter()
	r.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'w', tcell.ModNone))
	if m.newWorker != 1 {
		t.Fatalf("w in RAIL must fire OnNewWorker; got count %d", m.newWorker)
	}
	if len(p.Calls()) != 0 {
		t.Fatalf("w in RAIL must NOT be forwarded as byte; got %d calls", len(p.Calls()))
	}
}

// TestKeyRouter_MutationKey_w_InCOORD_ForwardsAsByte proves that `w` in
// COORD focus is NOT intercepted by the mutation handler — it forwards the
// byte 'w' to the coord task's PTY.
func TestKeyRouter_MutationKey_w_InCOORD_ForwardsAsByte(t *testing.T) {
	r, p, m, _ := newRouter()
	r.Focus.Advance() // → COORD
	r.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'w', tcell.ModNone))
	if m.newWorker != 0 {
		t.Fatalf("w in COORD must NOT fire OnNewWorker; got count %d", m.newWorker)
	}
	calls := p.Calls()
	if len(calls) != 1 || string(calls[0].Payload) != "w" || calls[0].TaskID != "coord-1" {
		t.Fatalf("w in COORD must forward 'w' to coord task; got %d calls, payload=%q taskid=%v",
			len(calls), payloadOf(calls), taskIDsOf(calls))
	}
}

// TestKeyRouter_MutationKey_w_InAGENT_ForwardsAsByte proves that `w` in
// AGENT focus forwards the byte 'w' to the agent task's PTY.
func TestKeyRouter_MutationKey_w_InAGENT_ForwardsAsByte(t *testing.T) {
	r, p, m, _ := newRouter()
	r.Focus.JumpToAGENT()
	r.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'w', tcell.ModNone))
	if m.newWorker != 0 {
		t.Fatalf("w in AGENT must NOT fire OnNewWorker; got count %d", m.newWorker)
	}
	calls := p.Calls()
	if len(calls) != 1 || string(calls[0].Payload) != "w" || calls[0].TaskID != "agent-1" {
		t.Fatalf("w in AGENT must forward 'w' to agent task; got %d calls, payload=%q taskid=%v",
			len(calls), payloadOf(calls), taskIDsOf(calls))
	}
}

// TestHotkeyItems_RailAdvertisesWorkerKey proves that the RAIL hotkey
// dictionary includes `w` (spawn worker), so it appears in argus's help
// overlay even if it's not on the bottom bar.
func TestHotkeyItems_RailAdvertisesWorkerKey(t *testing.T) {
	items := hotkeyItems(FocusRAIL, true)
	if !hotkeyContains(items, "w") {
		t.Errorf("RAIL hotkeys must advertise 'w' (spawn worker); items=%+v", items)
	}
}

// BUG-020: Both `w` (new worker) and `J` (adopt freelancer) must be advertised
// ON THE BOTTOM BAR (Bar:true) so they appear in argus's context-sensitive
// bottom-bar strip, not only in the `?` help overlay (Bar:false). Previously
// only the overlay listed them; the bar stayed silent about two key actions.
func TestHotkeyItems_RailAdvertisesJAndWOnBottomBar(t *testing.T) {
	items := hotkeyItems(FocusRAIL, true)
	for _, tc := range []struct {
		key   string
		label string
	}{
		{"w", "new agent"},
		{"J", "adopt"},
	} {
		if !hotkeyHas(items, tc.key, true) {
			t.Errorf("RAIL hotkey %q must have Bar:true (bottom-bar visible); items=%+v", tc.key, items)
		}
	}
}

// BUG-020: `helpHotkeyItems` (the `?` overlay source) must also include both
// `w` and `J` so the comprehensive overlay matches the bottom bar. Both
// consumers read from the same hotkeyItems source, so they never drift.
func TestHelpHotkeyItems_IncludesJAndW(t *testing.T) {
	all := helpHotkeyItems(true)
	for _, key := range []string{"w", "J"} {
		if !hotkeyContains(all, key) {
			t.Errorf("helpHotkeyItems must include %q (for the ? overlay); items=%+v", key, all)
		}
	}
}

// --- rail filter routing (change rail-search) ---

// fakeRailFilter satisfies the RailFilter gate; filtering is settable.
type fakeRailFilter struct{ filtering bool }

func (f *fakeRailFilter) IsFiltering() bool { return f.filtering }

// While the rail is in filter input mode, the router must YIELD keys to the
// focused rail widget (return the event, like the modal gate) so a rune that is
// otherwise a mutation key ('a' = archive) becomes filter input instead of
// firing the mutation handler.
func TestKeyRouter_YieldsToRailWhileFiltering(t *testing.T) {
	r, _, m, _ := newRouter()
	f := &fakeRailFilter{filtering: true}
	r.Filter = f

	out := r.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModNone))
	if out == nil {
		t.Fatalf("while filtering, a key must propagate to the rail (not be consumed by the router)")
	}
	if m.archive != 0 {
		t.Fatalf("while filtering, 'a' must NOT fire the archive mutation; got %d", m.archive)
	}
}

// Esc while filtering must also yield to the rail (so the rail clears the
// filter) rather than sending the argus key-surrender release frame.
func TestKeyRouter_EscYieldsToRailWhileFiltering(t *testing.T) {
	r, _, c := newRouterWithControl()
	f := &fakeRailFilter{filtering: true}
	r.Filter = f

	out := r.HandleKey(tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone))
	if out == nil {
		t.Fatalf("Esc while filtering must propagate to the rail to clear the filter")
	}
	if c.releases != 0 {
		t.Fatalf("Esc while filtering must NOT send a release frame; got %d", c.releases)
	}
}

// When NOT filtering, routing is unchanged: 'a' fires the archive mutation.
func TestKeyRouter_FilterGateOffRoutesNormally(t *testing.T) {
	r, _, m, _ := newRouter()
	r.Filter = &fakeRailFilter{filtering: false}
	r.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModNone))
	if m.archive != 1 {
		t.Fatalf("with the filter gate off, 'a' must fire archive once; got %d", m.archive)
	}
}
