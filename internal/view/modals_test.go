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

// --- ShowTextAreaInput tests ---

func TestShowTextAreaInput_EscCancelsAndRestoresFocus(t *testing.T) {
	a := newModalTestApp(t)

	cancels := 0
	a.ShowTextAreaInput("New worker", "Prompt", "", nil, func() { cancels++ })

	if !a.IsModalActive() {
		t.Fatalf("textarea input modal must be active")
	}
	dispatchKey(a, tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone))
	if a.IsModalActive() {
		t.Fatalf("Esc must dismiss the textarea input modal")
	}
	if cancels != 1 {
		t.Fatalf("Esc must fire onCancel once, got %d", cancels)
	}
	if !a.pieces.rail.HasFocus() {
		t.Fatalf("textarea input dismissal must restore focus to the rail")
	}
}

func TestShowTextAreaInput_EnterSubmits(t *testing.T) {
	a := newModalTestApp(t)

	var got string
	submitted := false
	a.ShowTextAreaInput("New worker", "Prompt", "", func(v string) { got = v; submitted = true }, nil)

	form := frontForm(t, a)
	promptField, ok := form.GetFormItem(0).(*styledTextArea)
	if !ok {
		t.Fatalf("form item 0 must be *styledTextArea")
	}
	promptField.SetText("do the thing", false)
	a.app.SetFocus(promptField)

	dispatchKey(a, tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))

	if a.IsModalActive() {
		t.Fatalf("Enter must dismiss the textarea input modal")
	}
	if !submitted || got != "do the thing" {
		t.Fatalf("Enter must submit the typed value: submitted=%v got=%q", submitted, got)
	}
}

func TestShowTextAreaInput_ShiftEnterDoesNotSubmit(t *testing.T) {
	a := newModalTestApp(t)

	submitted := false
	a.ShowTextAreaInput("New worker", "Prompt", "", func(_ string) { submitted = true }, nil)

	form := frontForm(t, a)
	promptField, ok := form.GetFormItem(0).(*styledTextArea)
	if !ok {
		t.Fatalf("form item 0 must be *styledTextArea")
	}
	a.app.SetFocus(promptField)

	dispatchKey(a, tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModShift))

	if submitted {
		t.Fatalf("Shift+Enter must NOT submit the textarea input modal")
	}
	if !a.IsModalActive() {
		t.Fatalf("Shift+Enter must not dismiss the textarea input modal")
	}
}

func TestShowTextAreaInput_RendersWithArgusTheme(t *testing.T) {
	a := newModalTestApp(t)
	a.ShowTextAreaInput("New worker", "Prompt", "", nil, nil)
	assertArgusThemedOverlay(t, renderPages(t, a, 80, 24))
}

func TestShowTextAreaInput_FieldIsTextArea(t *testing.T) {
	a := newModalTestApp(t)
	a.ShowTextAreaInput("New worker", "Prompt", "", nil, nil)

	form := frontForm(t, a)
	if _, ok := form.GetFormItem(0).(*styledTextArea); !ok {
		t.Fatalf("ShowTextAreaInput form item 0 must be *styledTextArea")
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
// tview's default contrast (lavender/blue) modal background NOR the old
// grey-blue overlay (theme.ColorStatusBG), and at least one cell must carry
// the single hera background (heraBackground) — the same black the chrome and
// pane interiors use (BUG-001). The modal reads as part of the app by its cyan
// border/title, not a distinct fill.
func assertArgusThemedOverlay(t *testing.T, sim tcell.SimulationScreen) {
	t.Helper()
	w, h := sim.Size()
	// tview's stock lavender contrast color, captured before BuildApp repoints
	// tview.Styles at heraBackground, so a regression to the default still trips.
	lavenderBG := tcell.ColorBlue
	greyBlue, themed := 0, 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			_, style, _ := sim.Get(x, y)
			_, bg, _ := style.Decompose()
			// The old grey-blue surfaces (tview lavender contrast OR the argus
			// status-bar dark gray) must not appear on any hera surface.
			if bg == lavenderBG || bg == theme.ColorStatusBG {
				greyBlue++
			}
			if bg == heraBackground {
				themed++
			}
		}
	}
	if greyBlue > 0 {
		t.Fatalf("modal renders %d cells with a grey-blue background — every hera surface must use the consistent black (BUG-001)", greyBlue)
	}
	if themed == 0 {
		t.Fatalf("expected the modal surface to use the single hera background (heraBackground)")
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

// formButtonLabels returns the button labels of the form on the front modal
// page, in order. Fails if the front page isn't a tview.Form.
func formButtonLabels(t *testing.T, a *App) []string {
	t.Helper()
	form := frontForm(t, a)
	labels := make([]string, 0, form.GetButtonCount())
	for i := 0; i < form.GetButtonCount(); i++ {
		labels = append(labels, form.GetButton(i).GetLabel())
	}
	return labels
}

// frontForm unwraps the centeredModal flex on the front page to the
// tview.Form it contains.
func frontForm(t *testing.T, a *App) *tview.Form {
	t.Helper()
	outer, ok := frontModal(t, a).(*tview.Flex)
	if !ok {
		t.Fatalf("front modal is not a flex wrapper")
	}
	inner, ok := outer.GetItem(1).(*tview.Flex)
	if !ok {
		t.Fatalf("centeredModal inner is not a flex")
	}
	form, ok := inner.GetItem(1).(*tview.Form)
	if !ok {
		t.Fatalf("centeredModal content is not a tview.Form")
	}
	return form
}

func TestShowInput_ButtonsAdvertiseKeys(t *testing.T) {
	a := newModalTestApp(t)
	a.ShowInput("Rename", "New name", "", nil, nil)
	got := formButtonLabels(t, a)
	want := []string{"OK [enter]", "Cancel [esc]"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("input modal buttons must advertise keys: got %v want %v", got, want)
	}
}

func TestShowForm2_ButtonsAdvertiseKeys(t *testing.T) {
	a := newModalTestApp(t)
	a.ShowForm2("New project", "Name", "", "Mission (optional)", "", nil, nil)
	got := formButtonLabels(t, a)
	want := []string{"OK [enter]", "Cancel [esc]"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("form2 modal buttons must advertise keys: got %v want %v", got, want)
	}
}

// TestShowInput_EnterSubmits proves the key advertised on the OK button is
// real: a bare Enter (from the focused input field, not the button) must
// activate OK and dismiss with the typed value.
func TestShowInput_EnterSubmits(t *testing.T) {
	a := newModalTestApp(t)

	var got string
	submitted := false
	a.ShowInput("Rename", "New name", "typed", func(v string) { got = v; submitted = true }, nil)

	dispatchKey(a, tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))

	if a.IsModalActive() {
		t.Fatalf("Enter must activate OK and dismiss the input modal")
	}
	if !submitted || got != "typed" {
		t.Fatalf("Enter must submit the typed value: submitted=%v got=%q", submitted, got)
	}
}

// TestShowForm2_EnterSubmits proves Enter submits the two-field form
// (matching the "OK [enter]" label) rather than merely advancing fields.
func TestShowForm2_EnterSubmits(t *testing.T) {
	a := newModalTestApp(t)

	var v1, v2 string
	submitted := false
	a.ShowForm2("New project", "Name", "proj", "Mission (optional)", "ship it",
		func(a, b string) { v1, v2 = a, b; submitted = true }, nil)

	dispatchKey(a, tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))

	if a.IsModalActive() {
		t.Fatalf("Enter must activate OK and dismiss the form2 modal")
	}
	if !submitted || v1 != "proj" || v2 != "ship it" {
		t.Fatalf("Enter must submit both values: submitted=%v v1=%q v2=%q", submitted, v1, v2)
	}
}

// TestFormFieldWidth_FitsInsideFrame asserts the derived field width keeps a
// labeled field inside the modal frame: label + ": " + field must not exceed
// the inner content width (modalWidth minus the two border columns and two
// horizontal padding columns), and the longest label drives the shared width
// (BUG-002).
func TestFormFieldWidth_FitsInsideFrame(t *testing.T) {
	// inner = modalWidth - 2 (border) - 2*formHorizPad (padding).
	const inner = modalWidth - 2 - 2*formHorizPad
	cases := [][]string{
		{"New name"},
		{"Name", "Mission (optional)"},
	}
	for _, labels := range cases {
		fw := formFieldWidth(labels...)
		if fw < 1 {
			t.Fatalf("field width must be positive for labels %v, got %d", labels, fw)
		}
		longest := 0
		for _, l := range labels {
			if len(l) > longest {
				longest = len(l)
			}
		}
		// label + ": " + one tview gap + field must fit the inner width.
		used := longest + 2 + 1 + fw
		if used > inner {
			t.Fatalf("labels %v overflow frame: used=%d inner=%d (fw=%d)", labels, used, inner, fw)
		}
	}
}

// frontList unwraps the centeredModal flex on the front page to the
// tview.List it contains.
func frontList(t *testing.T, a *App) *tview.List {
	t.Helper()
	outer, ok := frontModal(t, a).(*tview.Flex)
	if !ok {
		t.Fatalf("front modal is not a flex wrapper")
	}
	inner, ok := outer.GetItem(1).(*tview.Flex)
	if !ok {
		t.Fatalf("centeredModal inner is not a flex")
	}
	list, ok := inner.GetItem(1).(*tview.List)
	if !ok {
		t.Fatalf("centeredModal content is not a tview.List")
	}
	return list
}

// TestShowError_TitleHasPadding verifies the error modal title has one space of
// padding on each side so the label is legible in the border rule (BUG-023 J4).
func TestShowError_TitleHasPadding(t *testing.T) {
	a := newModalTestApp(t)
	a.ShowError("boom")

	m, ok := frontModal(t, a).(*tview.Modal)
	if !ok {
		t.Fatalf("error modal must be a *tview.Modal")
	}
	title := m.GetTitle()
	if len(title) < 2 || title[0] != ' ' || title[len(title)-1] != ' ' {
		t.Fatalf("error modal title must have surrounding spaces (BUG-023 J4), got %q", title)
	}
}

// TestShowInput_TitleHasPadding verifies the input form modal title has
// surrounding spaces (BUG-023 J4).
func TestShowInput_TitleHasPadding(t *testing.T) {
	a := newModalTestApp(t)
	a.ShowInput("Rename", "New name", "", nil, nil)

	form := frontForm(t, a)
	title := form.GetTitle()
	if len(title) < 2 || title[0] != ' ' || title[len(title)-1] != ' ' {
		t.Fatalf("input form title must have surrounding spaces (BUG-023 J4), got %q", title)
	}
}

// TestShowSelect_TitleHasPadding verifies the select-list modal title has
// surrounding spaces (BUG-023 J4).
func TestShowSelect_TitleHasPadding(t *testing.T) {
	a := newModalTestApp(t)
	a.ShowSelect("pick", "Coordinator", []string{"alpha"}, nil, nil)

	list := frontList(t, a)
	title := list.GetTitle()
	if len(title) < 2 || title[0] != ' ' || title[len(title)-1] != ' ' {
		t.Fatalf("select modal title must have surrounding spaces (BUG-023 J4), got %q", title)
	}
}

// TestShowSelect_SelectedRowIsHighContrast verifies the selected row uses the
// title colour (cyan) as its background so it is clearly legible against the
// dark modal background (BUG-023 S6: grey ColorHighlight was low-contrast).
func TestShowSelect_SelectedRowIsHighContrast(t *testing.T) {
	a := newModalTestApp(t)
	a.ShowSelect("pick", "Coordinator", []string{"alpha", "beta"}, nil, nil)

	sim := renderPages(t, a, 80, 24)
	w, h := sim.Size()
	found := false
	for y := 0; y < h && !found; y++ {
		for x := 0; x < w; x++ {
			_, style, _ := sim.Get(x, y)
			_, bg, _ := style.Decompose()
			if bg == theme.ColorTitle {
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatalf("selected row must use theme.ColorTitle background for high contrast (BUG-023 S6)")
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

// --- ShowNewCoordForm tests ---

// TestShowNewCoordForm_SubmitFiresCallback opens the form, sets the Name field,
// presses Enter, and verifies the onSubmit callback fires with the right values.
func TestShowNewCoordForm_SubmitFiresCallback(t *testing.T) {
	a := newModalTestApp(t)

	var got NewCoordFormInput
	submitted := false
	a.ShowNewCoordForm("New coordinator", []string{"proj-a"}, []string{"claude"},
		func(in NewCoordFormInput) { got = in; submitted = true }, nil)

	if !a.IsModalActive() {
		t.Fatalf("new coord form must be active after ShowNewCoordForm")
	}

	form := frontForm(t, a)
	if form.GetFormItemCount() < 5 {
		t.Fatalf("form must have at least 5 items (Name, Project, Branch, Backend, Prompt); got %d", form.GetFormItemCount())
	}

	nameField, ok := form.GetFormItem(0).(*styledInputField)
	if !ok {
		t.Fatalf("first form item must be styledInputField for Name")
	}
	nameField.SetText("my-coord")

	dispatchKey(a, tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))

	if a.IsModalActive() {
		t.Fatalf("Enter must dismiss the new coord form")
	}
	if !submitted {
		t.Fatalf("Enter must fire onSubmit")
	}
	if got.Name != "my-coord" {
		t.Fatalf("got Name %q, want %q", got.Name, "my-coord")
	}
}

// TestShowNewCoordForm_EscCancels verifies Esc fires onCancel.
func TestShowNewCoordForm_EscCancels(t *testing.T) {
	a := newModalTestApp(t)

	cancelled := false
	a.ShowNewCoordForm("New coordinator", []string{"p1"}, []string{"claude"},
		nil, func() { cancelled = true })

	dispatchKey(a, tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone))

	if a.IsModalActive() {
		t.Fatalf("Esc must dismiss")
	}
	if !cancelled {
		t.Fatalf("Esc must fire onCancel")
	}
}

// TestShowNewCoordForm_EmptyNameDoesNotSubmit verifies that an empty Name
// field does NOT fire onSubmit.
func TestShowNewCoordForm_EmptyNameDoesNotSubmit(t *testing.T) {
	a := newModalTestApp(t)

	submitted := false
	a.ShowNewCoordForm("New coordinator", []string{"p1"}, []string{"claude"},
		func(_ NewCoordFormInput) { submitted = true }, nil)

	// Name stays empty (default); press Enter
	dispatchKey(a, tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))

	if submitted {
		t.Fatalf("empty Name must NOT invoke onSubmit")
	}
}

// TestShowNewCoordForm_PromptIsTextArea verifies the Prompt field (item 4) is
// a multi-line styledTextArea so coordinator prompts can wrap across lines
// (BUG-011). Enter-to-submit is enforced by the form's input capture, not by
// restricting the widget to a single-line InputField.
func TestShowNewCoordForm_PromptIsTextArea(t *testing.T) {
	a := newModalTestApp(t)
	a.ShowNewCoordForm("New coordinator", []string{"p1"}, []string{"claude"}, nil, nil)

	form := frontForm(t, a)
	if _, ok := form.GetFormItem(4).(*styledTextArea); !ok {
		t.Fatalf("form item 4 (Prompt) must be *styledTextArea (BUG-011)")
	}
}

// TestShowNewCoordForm_EnterOnPromptSubmits verifies plain Enter while the
// Prompt TextArea is focused submits the form (BUG-011). The form's input
// capture intercepts KeyEnter without modifiers and calls dismiss so the
// TextArea never sees the keystroke as a newline insertion.
func TestShowNewCoordForm_EnterOnPromptSubmits(t *testing.T) {
	a := newModalTestApp(t)

	var got NewCoordFormInput
	submitted := false
	a.ShowNewCoordForm("New coordinator", []string{"proj-a"}, []string{"claude"},
		func(in NewCoordFormInput) { got = in; submitted = true }, nil)

	form := frontForm(t, a)
	nameField, ok := form.GetFormItem(0).(*styledInputField)
	if !ok {
		t.Fatalf("item 0 must be styledInputField for Name")
	}
	nameField.SetText("my-coord")

	promptField, ok := form.GetFormItem(4).(*styledTextArea)
	if !ok {
		t.Fatalf("item 4 must be styledTextArea for Prompt")
	}
	promptField.SetText("some prompt text", false)
	a.app.SetFocus(promptField)

	dispatchKey(a, tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))

	if a.IsModalActive() {
		t.Fatalf("Enter on Prompt field must dismiss the new coord form")
	}
	if !submitted {
		t.Fatalf("Enter on Prompt field must fire onSubmit")
	}
	if got.Name != "my-coord" {
		t.Fatalf("got Name %q, want %q", got.Name, "my-coord")
	}
	if got.Prompt != "some prompt text" {
		t.Fatalf("got Prompt %q, want %q", got.Prompt, "some prompt text")
	}
}

// TestShowNewCoordForm_CtrlEnterOnPromptDoesNotSubmit verifies that a modified
// Enter (Ctrl+Enter) while the Prompt TextArea is focused does NOT submit the
// form (BUG-011). The form's capture lets modified Enter through so the TextArea
// can insert a newline.
func TestShowNewCoordForm_CtrlEnterOnPromptDoesNotSubmit(t *testing.T) {
	a := newModalTestApp(t)

	submitted := false
	a.ShowNewCoordForm("New coordinator", []string{"proj-a"}, []string{"claude"},
		func(_ NewCoordFormInput) { submitted = true }, nil)

	form := frontForm(t, a)
	promptField, ok := form.GetFormItem(4).(*styledTextArea)
	if !ok {
		t.Fatalf("item 4 must be styledTextArea for Prompt")
	}
	a.app.SetFocus(promptField)

	dispatchKey(a, tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModCtrl))

	if submitted {
		t.Fatalf("Ctrl+Enter on Prompt field must NOT submit the form")
	}
	if !a.IsModalActive() {
		t.Fatalf("Ctrl+Enter on Prompt field must not dismiss the modal")
	}
}

// TestShowNewCoordForm_FieldsContainedInFrame verifies field widths keep all
// inputs inside the modal frame (BUG-002 compliance).
func TestShowNewCoordForm_FieldsContainedInFrame(t *testing.T) {
	// inner = modalWidth - 2 (border) - 2*formHorizPad (padding).
	const inner = modalWidth - 2 - 2*formHorizPad
	labels := []string{"Name", "Project", "Branch", "Backend", "Prompt"}
	fw := formFieldWidth(labels...)
	if fw < 1 {
		t.Fatalf("field width must be positive, got %d", fw)
	}
	longest := 0
	for _, l := range labels {
		if len(l) > longest {
			longest = len(l)
		}
	}
	// label + ": " + tview gap + field must fit inside inner width.
	used := longest + 2 + 1 + fw
	if used > inner {
		t.Fatalf("labels %v overflow frame: used=%d inner=%d (fw=%d)", labels, used, inner, fw)
	}
}

// TestShowNewCoordForm_NameFieldCursorVisible verifies that after opening the
// new-coord form, the terminal cursor is visible inside the Name field's text
// area (BUG-002). The cursor is positioned by tview's TextArea.Draw via
// screen.ShowCursor; the simulation screen records this position.
func TestShowNewCoordForm_NameFieldCursorVisible(t *testing.T) {
	a := newModalTestApp(t)
	a.ShowNewCoordForm("New coordinator", []string{"proj-a"}, []string{"claude"}, nil, nil)

	sim := renderPages(t, a, 80, 24)

	x, y, visible := sim.GetCursor()
	if !visible {
		t.Fatalf("cursor must be visible in the focused Name field after ShowNewCoordForm (BUG-002); got hidden cursor")
	}
	// Sanity check: cursor must be somewhere within the modal area (not at 0,0
	// or outside the screen).
	if x <= 0 || y <= 0 {
		t.Fatalf("cursor position (%d,%d) is outside the modal area — focus may not have reached the Name field", x, y)
	}
}

// TestShowNewCoordForm_FocusedFieldHasCyanBackground verifies that the focused
// Name field renders with the cyan (theme.ColorTitle) background that
// distinguishes the active input from the rest of the form (BUG-002).
// styledInputField.SetFormAttributes re-applies fieldFocusedStyle after
// Form.Draw() resets textStyle; this test proves that mechanism works.
func TestShowNewCoordForm_FocusedFieldHasCyanBackground(t *testing.T) {
	a := newModalTestApp(t)
	a.ShowNewCoordForm("New coordinator", []string{"proj-a"}, []string{"claude"}, nil, nil)

	sim := renderPages(t, a, 80, 24)
	w, h := sim.Size()

	found := false
	for y := 0; y < h && !found; y++ {
		for x := 0; x < w; x++ {
			_, style, _ := sim.Get(x, y)
			_, bg, _ := style.Decompose()
			if bg == theme.ColorTitle {
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatalf("focused Name field must render with theme.ColorTitle (cyan) background (BUG-002); styledInputField.SetFormAttributes may not be re-applying fieldFocusedStyle")
	}
}

// TestShowNewCoordForm_ButtonLabels verifies the button labels advertise keys.
func TestShowNewCoordForm_ButtonLabels(t *testing.T) {
	a := newModalTestApp(t)
	a.ShowNewCoordForm("New coordinator", []string{"p1"}, []string{"claude"}, nil, nil)
	got := formButtonLabels(t, a)
	want := []string{"Submit [enter]", "Cancel [esc]"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("new coord form buttons must advertise keys: got %v want %v", got, want)
	}
}

// frontCycler unwraps the centeredModal flex to an inlineCycler at the given
// form item index. Fails if the item is not an *inlineCycler.
func frontCycler(t *testing.T, a *App, idx int) *inlineCycler {
	t.Helper()
	form := frontForm(t, a)
	c, ok := form.GetFormItem(idx).(*inlineCycler)
	if !ok {
		t.Fatalf("form item %d must be *inlineCycler", idx)
	}
	return c
}

// TestShowNewCoordForm_ProjectIsInlineCycler verifies the Project field (item 1)
// uses inlineCycler — not tview.DropDown — so no green popup overlay (BUG-035).
func TestShowNewCoordForm_ProjectIsInlineCycler(t *testing.T) {
	a := newModalTestApp(t)
	a.ShowNewCoordForm("New coordinator", []string{"proj-a", "proj-b"}, []string{"claude"}, nil, nil)
	_ = frontCycler(t, a, 1) // asserts *inlineCycler
}

// TestShowNewCoordForm_BackendIsInlineCycler verifies the Backend field (item 3)
// uses inlineCycler (BUG-035).
func TestShowNewCoordForm_BackendIsInlineCycler(t *testing.T) {
	a := newModalTestApp(t)
	a.ShowNewCoordForm("New coordinator", []string{"proj-a"}, []string{"claude", "opus"}, nil, nil)
	_ = frontCycler(t, a, 3)
}

// TestShowNewCoordForm_BranchDefaultsToOriginMain verifies the Branch field
// (item 2) is pre-filled with "origin/main" as a sensible default.
func TestShowNewCoordForm_BranchDefaultsToOriginMain(t *testing.T) {
	a := newModalTestApp(t)
	a.ShowNewCoordForm("New coordinator", []string{"proj-a"}, []string{"claude"}, nil, nil)

	form := frontForm(t, a)
	branchField, ok := form.GetFormItem(2).(*styledInputField)
	if !ok {
		t.Fatalf("form item 2 must be styledInputField for Branch")
	}
	if got := branchField.GetText(); got != "origin/main" {
		t.Fatalf("Branch field must default to %q, got %q", "origin/main", got)
	}
}

// TestShowNewCoordForm_CyclerLeftRightCycles verifies that Left/Right on the
// Project cycler cycles through options without submitting the form.
func TestShowNewCoordForm_CyclerLeftRightCycles(t *testing.T) {
	a := newModalTestApp(t)
	a.ShowNewCoordForm("New coordinator", []string{"alpha", "beta", "gamma"}, []string{"claude"}, nil, nil)

	cycler := frontCycler(t, a, 1)

	// Initial: "alpha" (index 0)
	if idx, opt := cycler.GetCurrentOption(); idx != 0 || opt != "alpha" {
		t.Fatalf("initial option must be alpha (0), got %d %q", idx, opt)
	}

	// Focus the cycler so dispatchKey routes to it.
	a.app.SetFocus(cycler)

	dispatchKey(a, tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModNone))
	if idx, opt := cycler.GetCurrentOption(); idx != 1 || opt != "beta" {
		t.Fatalf("after Right expected beta (1), got %d %q", idx, opt)
	}

	dispatchKey(a, tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModNone))
	if idx, opt := cycler.GetCurrentOption(); idx != 0 || opt != "alpha" {
		t.Fatalf("after Left expected alpha (0), got %d %q", idx, opt)
	}

	// Form must NOT have been submitted
	if !a.IsModalActive() {
		t.Fatalf("Left/Right on cycler must not dismiss the modal")
	}
}

// TestShowNewCoordForm_CyclerEnterAdvancesFocusNoSubmit verifies that pressing
// Enter while the Project cycler is focused advances focus (does NOT submit).
func TestShowNewCoordForm_CyclerEnterAdvancesFocusNoSubmit(t *testing.T) {
	a := newModalTestApp(t)

	submitted := false
	a.ShowNewCoordForm("New coordinator", []string{"proj-a"}, []string{"claude"},
		func(_ NewCoordFormInput) { submitted = true }, nil)

	cycler := frontCycler(t, a, 1)
	a.app.SetFocus(cycler)

	dispatchKey(a, tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))

	if submitted {
		t.Fatalf("Enter on Project cycler must NOT submit the form")
	}
	if !a.IsModalActive() {
		t.Fatalf("Enter on cycler must not dismiss the modal")
	}
}

// TestShowNewCoordForm_CyclerWrapAround verifies that cycling past the last
// option wraps back to the first, and vice versa.
func TestShowNewCoordForm_CyclerWrapAround(t *testing.T) {
	a := newModalTestApp(t)
	a.ShowNewCoordForm("New coordinator", []string{"x", "y"}, []string{"claude"}, nil, nil)

	cycler := frontCycler(t, a, 1)
	a.app.SetFocus(cycler)

	// Left from index 0 wraps to last
	dispatchKey(a, tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModNone))
	if idx, _ := cycler.GetCurrentOption(); idx != 1 {
		t.Fatalf("Left from 0 must wrap to last (1), got %d", idx)
	}

	// Right from last wraps to first
	dispatchKey(a, tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModNone))
	if idx, _ := cycler.GetCurrentOption(); idx != 0 {
		t.Fatalf("Right from last must wrap to 0, got %d", idx)
	}
}

// TestShowNewCoordForm_SubmitPassesCyclerValues verifies that the onSubmit
// callback receives the Project/Backend values from the cyclers.
func TestShowNewCoordForm_SubmitPassesCyclerValues(t *testing.T) {
	a := newModalTestApp(t)

	var got NewCoordFormInput
	submitted := false
	a.ShowNewCoordForm("New coordinator", []string{"proj-a", "proj-b"}, []string{"claude", "opus"},
		func(in NewCoordFormInput) { got = in; submitted = true }, nil)

	form := frontForm(t, a)
	nameField, ok := form.GetFormItem(0).(*styledInputField)
	if !ok {
		t.Fatalf("item 0 must be styledInputField for Name")
	}
	nameField.SetText("my-coord")

	// Advance Backend cycler to "opus" (index 1)
	backendCycler := frontCycler(t, a, 3)
	a.app.SetFocus(backendCycler)
	dispatchKey(a, tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModNone))

	// Submit via the Name field (restore focus to nameField then Enter)
	a.app.SetFocus(nameField)
	dispatchKey(a, tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))

	if !submitted {
		t.Fatalf("Enter must fire onSubmit")
	}
	if got.Project != "proj-a" {
		t.Fatalf("Project must be proj-a (initial), got %q", got.Project)
	}
	if got.Backend != "opus" {
		t.Fatalf("Backend must be opus (after Right), got %q", got.Backend)
	}
}
