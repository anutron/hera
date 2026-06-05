package view

import (
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/anutron/argus-sdk/theme"
)

// newModalTestApp builds an App with the test-only synchronous modal mode
// (the tview event loop is not running in tests, and QueueUpdate blocks
// until the loop services it). Focus starts on the rail, mirroring the
// production RAIL-focus state every mutation modal opens from.
func newModalTestApp(t *testing.T) *App {
	t.Helper()
	d := openTestDB(t)
	a, err := BuildApp(d, nil)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	t.Cleanup(a.Close)
	a.modalSync = true
	a.app.SetFocus(a.pieces.rail)
	return a
}

// dispatchKey delivers a key event exactly the way tview's event loop
// does after the input capture yields it: to the root primitive's
// InputHandler with the application's SetFocus as the focus delegate.
// This exercises the real Pages → focused-page → modal-form dispatch.
func dispatchKey(a *App, ev *tcell.EventKey) {
	root := tview.Primitive(a.pieces.pages)
	if !root.HasFocus() {
		return
	}
	if handler := root.InputHandler(); handler != nil {
		handler(ev, func(p tview.Primitive) { a.app.SetFocus(p) })
	}
}

// frontModal returns the front page's primitive when a modal is active.
func frontModal(t *testing.T, a *App) tview.Primitive {
	t.Helper()
	name, item := a.pieces.pages.GetFrontPage()
	if name == pageBase || item == nil {
		t.Fatalf("expected a modal page on top, front = %q", name)
	}
	return item
}

func TestShowError_FocusMovesToModalAndEnterDismisses(t *testing.T) {
	a := newModalTestApp(t)

	a.ShowError("ops.stepStatus: boom")

	if !a.IsModalActive() {
		t.Fatalf("error modal must be active after ShowError")
	}
	modal := frontModal(t, a)
	if !modal.HasFocus() {
		t.Fatalf("error modal must take tview focus on open — without it Enter routes to the rail tree behind the overlay")
	}
	if a.pieces.rail.HasFocus() {
		t.Fatalf("rail must not retain focus while the error modal is up")
	}

	dispatchKey(a, tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))

	if a.IsModalActive() {
		t.Fatalf("Enter must activate OK and dismiss the error modal")
	}
	if !a.pieces.rail.HasFocus() {
		t.Fatalf("dismissing the modal must restore focus to the rail")
	}
}

func TestShowError_EscDismissesAndRestoresFocus(t *testing.T) {
	a := newModalTestApp(t)

	a.ShowError("boom")
	dispatchKey(a, tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone))

	if a.IsModalActive() {
		t.Fatalf("Esc must dismiss the error modal")
	}
	if !a.pieces.rail.HasFocus() {
		t.Fatalf("Esc-dismiss must restore focus to the rail")
	}
}

// The live T3 trap: a background rail repopulate (argus state refresh /
// DAO write) lands in OnFocusChanged via the body-mode path and used to
// SetFocus(rail) unconditionally — stealing tview focus from the open
// modal, so Enter went to the rail tree behind the overlay and the modal
// could never be dismissed. The border repaint must NOT move focus while
// a modal is active.
func TestOnFocusChanged_DoesNotStealFocusFromActiveModal(t *testing.T) {
	a := newModalTestApp(t)

	a.ShowError("boom")
	modal := frontModal(t, a)

	// Simulate the periodic repopulate → setBodyMode → OnFocusChanged path.
	a.OnFocusChanged(FocusRAIL)

	if !modal.HasFocus() {
		t.Fatalf("OnFocusChanged while a modal is active must not steal tview focus from the modal")
	}
	if a.pieces.rail.HasFocus() {
		t.Fatalf("rail must not regain focus while the modal is up")
	}

	// The operator can still dismiss.
	dispatchKey(a, tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	if a.IsModalActive() {
		t.Fatalf("Enter must still dismiss the modal after a background border repaint")
	}
	if !a.pieces.rail.HasFocus() {
		t.Fatalf("focus must land back on the rail after dismissal")
	}
}

func TestShowConfirm_FocusMovesAndEscIsNo(t *testing.T) {
	a := newModalTestApp(t)

	yes, no := 0, 0
	a.ShowConfirm("Delete?", "DESTRUCTIVE. Continue? (y/N)", func() { yes++ }, func() { no++ })

	modal := frontModal(t, a)
	if !modal.HasFocus() {
		t.Fatalf("confirm modal must take tview focus on open")
	}

	dispatchKey(a, tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone))

	if a.IsModalActive() {
		t.Fatalf("Esc must dismiss the confirm modal")
	}
	if yes != 0 || no != 1 {
		t.Fatalf("Esc must mean No: yes=%d no=%d", yes, no)
	}
	if !a.pieces.rail.HasFocus() {
		t.Fatalf("confirm dismissal must restore focus to the rail")
	}
}

func TestShowConfirm_EnterDefaultsToNo(t *testing.T) {
	// The confirm text reads "(y/N)" — capital N marks No as the default,
	// so a bare Enter must NOT fire the destructive path.
	a := newModalTestApp(t)

	yes, no := 0, 0
	a.ShowConfirm("Delete?", "Continue? (y/N)", func() { yes++ }, func() { no++ })
	dispatchKey(a, tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))

	if a.IsModalActive() {
		t.Fatalf("Enter must dismiss the confirm modal")
	}
	if yes != 0 || no != 1 {
		t.Fatalf("bare Enter must default to No per (y/N): yes=%d no=%d", yes, no)
	}
}

func TestShowConfirm_YAndNRunesDecide(t *testing.T) {
	a := newModalTestApp(t)

	yes, no := 0, 0
	a.ShowConfirm("Delete?", "Continue? (y/N)", func() { yes++ }, func() { no++ })
	dispatchKey(a, tcell.NewEventKey(tcell.KeyRune, 'y', tcell.ModNone))
	if a.IsModalActive() || yes != 1 || no != 0 {
		t.Fatalf("`y` must confirm: active=%v yes=%d no=%d", a.IsModalActive(), yes, no)
	}

	a.ShowConfirm("Delete?", "Continue? (y/N)", func() { yes++ }, func() { no++ })
	dispatchKey(a, tcell.NewEventKey(tcell.KeyRune, 'n', tcell.ModNone))
	if a.IsModalActive() || yes != 1 || no != 1 {
		t.Fatalf("`n` must decline: active=%v yes=%d no=%d", a.IsModalActive(), yes, no)
	}
	if !a.pieces.rail.HasFocus() {
		t.Fatalf("focus must return to the rail after rune-decided confirm")
	}
}

func TestShowInput_EscCancelsAndRestoresFocus(t *testing.T) {
	a := newModalTestApp(t)

	cancels := 0
	a.ShowInput("Rename", "New name", "old", nil, func() { cancels++ })

	if !a.IsModalActive() {
		t.Fatalf("input modal must be active")
	}
	dispatchKey(a, tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone))
	if a.IsModalActive() {
		t.Fatalf("Esc must dismiss the input modal")
	}
	if cancels != 1 {
		t.Fatalf("Esc must fire onCancel once, got %d", cancels)
	}
	if !a.pieces.rail.HasFocus() {
		t.Fatalf("input dismissal must restore focus to the rail")
	}
}

// renderPages draws the pages root (base + any modal overlay) and returns
// the simulation screen so cell styles can be inspected.
func renderPages(t *testing.T, a *App, w, h int) tcell.SimulationScreen {
	t.Helper()
	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	sim.SetSize(w, h)
	a.pieces.pages.SetRect(0, 0, w, h)
	a.pieces.pages.Draw(sim)
	sim.Show()
	return sim
}

// assertArgusThemedOverlay scans the drawn surface: no cell may carry
// tview's default contrast (lavender/blue) modal background, and at least
// one cell must carry the argus dark overlay background.
func assertArgusThemedOverlay(t *testing.T, sim tcell.SimulationScreen) {
	t.Helper()
	w, h := sim.Size()
	defaultBG := tview.Styles.ContrastBackgroundColor
	lavender, themed := 0, 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			_, style, _ := sim.Get(x, y)
			_, bg, _ := style.Decompose()
			if bg == defaultBG {
				lavender++
			}
			if bg == theme.ColorStatusBG {
				themed++
			}
		}
	}
	if lavender > 0 {
		t.Fatalf("modal renders %d cells with tview's default contrast background — must use the argus theme", lavender)
	}
	if themed == 0 {
		t.Fatalf("expected the modal surface to use the argus dark background (theme.ColorStatusBG)")
	}
}

func TestShowError_RendersWithArgusTheme(t *testing.T) {
	a := newModalTestApp(t)
	a.ShowError("boom")
	assertArgusThemedOverlay(t, renderPages(t, a, 80, 24))
}

func TestShowConfirm_RendersWithArgusTheme(t *testing.T) {
	a := newModalTestApp(t)
	a.ShowConfirm("Delete?", "Continue? (y/N)", nil, nil)
	assertArgusThemedOverlay(t, renderPages(t, a, 80, 24))
}

func TestShowInput_RendersWithArgusTheme(t *testing.T) {
	a := newModalTestApp(t)
	a.ShowInput("Rename", "New name", "", nil, nil)
	assertArgusThemedOverlay(t, renderPages(t, a, 80, 24))
}

func TestShowSelect_EnterInvokesOnSelectWithChosenIndex(t *testing.T) {
	a := newModalTestApp(t)

	var got int
	picked := false
	cancelled := false
	a.ShowSelect("Adopt \"feat-x\" into…", "Coordinator", []string{"alpha", "beta", "gamma"},
		func(idx int) { got = idx; picked = true },
		func() { cancelled = true })

	if !a.IsModalActive() {
		t.Fatalf("select modal must be active after ShowSelect")
	}
	modal := frontModal(t, a)
	if !modal.HasFocus() {
		t.Fatalf("select modal must take tview focus on open")
	}

	// Move down one (to "beta") and confirm with Enter.
	dispatchKey(a, tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone))
	dispatchKey(a, tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))

	if a.IsModalActive() {
		t.Fatalf("Enter must select a row and dismiss the picker")
	}
	if !picked || cancelled {
		t.Fatalf("onSelect must fire (picked=%v cancelled=%v)", picked, cancelled)
	}
	if got != 1 {
		t.Fatalf("expected index 1 (beta) after one Down + Enter; got %d", got)
	}
	if !a.pieces.rail.HasFocus() {
		t.Fatalf("dismissing the picker must restore focus to the rail")
	}
}

func TestShowSelect_EscInvokesOnCancel(t *testing.T) {
	a := newModalTestApp(t)

	picked := false
	cancelled := false
	a.ShowSelect("pick", "Coordinator", []string{"alpha", "beta"},
		func(idx int) { picked = true },
		func() { cancelled = true })

	dispatchKey(a, tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone))

	if a.IsModalActive() {
		t.Fatalf("Esc must dismiss the picker")
	}
	if picked || !cancelled {
		t.Fatalf("Esc must fire onCancel, not onSelect (picked=%v cancelled=%v)", picked, cancelled)
	}
	if !a.pieces.rail.HasFocus() {
		t.Fatalf("dismissing the picker must restore focus to the rail")
	}
}
