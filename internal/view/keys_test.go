package view

import (
	"context"
	"sync"
	"testing"

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
	new, rename, del, archive, listAll, help int
}

func (f *fakeMutations) OnNew()     { f.new++ }
func (f *fakeMutations) OnRename()  { f.rename++ }
func (f *fakeMutations) OnDelete()  { f.del++ }
func (f *fakeMutations) OnArchive() { f.archive++ }
func (f *fakeMutations) OnListAll() { f.listAll++ }
func (f *fakeMutations) OnHelp()    { f.help++ }

type fakeBorder struct {
	states []FocusState
}

func (f *fakeBorder) OnFocusChanged(s FocusState) { f.states = append(f.states, s) }

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

func TestKeyRouter_NoAgentTaskID_DropsKeystroke(t *testing.T) {
	r, p, _, _ := newRouter()
	r.Targets = &fakeTargets{coord: "", agent: ""}
	r.Focus.JumpToAGENT()
	r.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'z', tcell.ModNone))
	if len(p.Calls()) != 0 {
		t.Fatalf("missing task ID must drop keystroke, not forward; got %d calls", len(p.Calls()))
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

func TestKeyRouter_MutationKey_CtrlD_InCOORD_NoMutationOnlyForward(t *testing.T) {
	r, p, m, _ := newRouter()
	r.Focus.Advance() // → COORD
	r.HandleKey(tcell.NewEventKey(tcell.KeyCtrlD, 0, tcell.ModCtrl))
	if m.del != 0 {
		t.Fatalf("Ctrl-D in COORD must NOT fire OnDelete; got count %d", m.del)
	}
	calls := p.Calls()
	if len(calls) != 1 || len(calls[0].Payload) != 1 || calls[0].Payload[0] != 0x04 {
		t.Fatalf("Ctrl-D in COORD must forward 0x04 (EOT); got %d calls, payload=%v", len(calls), payloadOf(calls))
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
