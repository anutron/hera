package view

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/anutron/argus-sdk/theme"
)

// Modal page names used inside *App.pieces.pages. A separate name per
// modal kind so close logic can remove a specific page without
// guessing.
const (
	pageBase      = "base"
	pageInput     = "modal-input"
	pageForm2     = "modal-form2"
	pageNewCoord  = "modal-new-coord"
	pageNewWorker = "modal-new-worker"
	pageConfirm   = "modal-confirm"
	pageError     = "modal-error"
	pageSelect    = "modal-select"
)

// modalWidth is the outer width every centered form modal is sized to (see
// centeredModal). Field widths are derived from it so inputs stay inside the
// frame.
const modalWidth = 60

// formHorizPad is the horizontal padding (in columns) added inside each side
// of the form border via SetBorderPadding, giving visible inset between the
// border and the field content (BUG-002).
const formHorizPad = 1

// formFieldWidth returns the input field width that keeps a labeled field
// fully inside a modalWidth-wide form. tview lays a vertical form's field out
// at x = innerLeft + labelWidth and draws the input's text area
// labelWidth+fieldWidth wide; with no clamp the box runs past the right
// border (BUG-002). The inner content width is modalWidth minus the two
// border columns and the two horizontal padding columns; reserving the
// longest label (plus tview's one-space label gap) leaves the width an input
// may safely occupy. labels are the raw label strings WITHOUT the trailing
// ": " that callers append.
func formFieldWidth(labels ...string) int {
	const borderCols = 2  // left+right form border
	const labelSuffix = 2 // ": " appended to every label below
	inner := modalWidth - borderCols - 2*formHorizPad
	maxLabel := 0
	for _, l := range labels {
		// +labelSuffix for the ": " each caller appends, +1 for tview's
		// inter-label/field space.
		w := len(l) + labelSuffix + 1
		if w > maxLabel {
			maxLabel = w
		}
	}
	fw := inner - maxLabel
	if fw < 1 {
		fw = 1
	}
	return fw
}

// Modal focus contract (spec: "Modal overlays take keyboard focus,
// dismiss via Enter and Esc, restore prior focus, and use the argus
// theme"):
//
//   - every Show* moves tview focus to the modal on open (captureFocus
//     records what held it);
//   - while a modal is up, OnFocusChanged withholds its SetFocus so
//     background rail repopulates can't steal focus back (the T3 trap);
//   - closeModal restores the captured primitive (falling back to the
//     rail) so dismissal lands the operator where they were.

// themeModalStyle paints a tview.Modal with the argus theme — the same black
// background every other hera surface uses (BUG-001), cyan border/title, themed
// buttons — replacing tview's default contrast (lavender) styling so overlays
// read as part of the same application. The modal is distinguished from the
// chrome behind it by its cyan border + title and its highlighted buttons, not
// a different fill. textColor styles the message body (normal for confirms,
// error red for error modals).
func themeModalStyle(m *tview.Modal, title string, textColor tcell.Color) {
	m.SetBackgroundColor(heraBackground). // form + frame
						SetTextColor(textColor).
						SetButtonBackgroundColor(theme.ColorHighlight).
						SetButtonTextColor(theme.ColorNormal).
						SetButtonActivatedStyle(tcell.StyleDefault.Background(theme.ColorTitle).Foreground(tcell.ColorBlack).Bold(true))
	m.Box.SetBackgroundColor(heraBackground)
	m.SetBorderColor(theme.ColorTitle)
	m.SetTitleColor(theme.ColorTitle)
	if title != "" {
		m.Box.SetTitle(" " + title + " ").SetTitleAlign(tview.AlignCenter)
	}
}

// themeFormStyle paints an input/form modal with the same argus brush as
// themeModalStyle so all modal flows are visually consistent.
func themeFormStyle(form *tview.Form, title string) {
	form.SetLabelColor(theme.ColorNormal).
		SetFieldBackgroundColor(theme.ColorHighlight).
		SetFieldTextColor(theme.ColorNormal).
		SetButtonBackgroundColor(theme.ColorHighlight).
		SetButtonTextColor(theme.ColorNormal).
		SetButtonActivatedStyle(tcell.StyleDefault.Background(theme.ColorTitle).Foreground(tcell.ColorBlack).Bold(true))
	form.SetBackgroundColor(heraBackground) // Box-level background (BUG-001)
	form.SetBorder(true)
	form.SetBorderColor(theme.ColorTitle)
	form.SetTitleColor(theme.ColorTitle)
	// Horizontal padding keeps fields from running edge-to-edge against the
	// border; formFieldWidth accounts for these columns (BUG-002).
	form.SetBorderPadding(0, 0, formHorizPad, formHorizPad)
	if title != "" {
		form.SetTitle(" " + title + " ").SetTitleAlign(tview.AlignCenter)
	}
}

// fieldFocusStyles are the input-field styles a modal form swaps between as
// focus moves (BUG-005). The blurred style is the form's default field paint
// (theme highlight background); the focused style brightens the field —
// cyan border-coloured text on the title background, bold — so the operator
// can always tell which input is active as they tab Name → Mission → OK →
// Cancel. (Buttons already advertise focus via SetButtonActivatedStyle.) We
// touch only foreground/border-style attributes per the focus-styling note;
// the base modal background is owned by a later pass.
var (
	fieldBlurredStyle = tcell.StyleDefault.
				Background(theme.ColorHighlight).
				Foreground(theme.ColorNormal)
	fieldFocusedStyle = tcell.StyleDefault.
				Background(theme.ColorTitle).
				Foreground(tcell.ColorBlack).
				Bold(true)
)

// styledInputField wraps tview.InputField and overrides SetFormAttributes so
// that the focused/blurred field style survives the form's per-draw attribute
// reset (BUG-002). tview's Form.Draw() calls SetFormAttributes on every item
// before drawing, overwriting any SetFieldStyle set via FocusFunc. The
// override re-applies the correct style immediately after the form resets it,
// so the cursor (and the cyan focus highlight) is visible.
type styledInputField struct {
	*tview.InputField
}

// newStyledInputField returns a new styledInputField with the blurred style
// applied as its initial appearance.
func newStyledInputField() *styledInputField {
	f := &styledInputField{InputField: tview.NewInputField()}
	f.SetFieldStyle(fieldBlurredStyle)
	return f
}

// SetFormAttributes implements tview.FormItem. It delegates to the embedded
// InputField then re-applies whichever style (focused or blurred) matches the
// current focus state. This hook fires inside Form.Draw()'s position loop,
// before the draw loop, so the correct textStyle is in place when the field
// is rendered.
func (f *styledInputField) SetFormAttributes(labelWidth int, labelColor, bgColor, fieldTextColor, fieldBgColor tcell.Color) tview.FormItem {
	f.InputField.SetFormAttributes(labelWidth, labelColor, bgColor, fieldTextColor, fieldBgColor)
	if f.HasFocus() {
		f.SetFieldStyle(fieldFocusedStyle)
	} else {
		f.SetFieldStyle(fieldBlurredStyle)
	}
	return f
}

// styledTextArea wraps tview.TextArea and overrides SetFormAttributes so that
// the focused/blurred text style survives the form's per-draw attribute reset
// (BUG-002). Same mechanism as styledInputField but for a multi-line field.
type styledTextArea struct {
	*tview.TextArea
}

// newStyledTextArea returns a new styledTextArea with 3 visible rows (enough
// for multi-line coordinator prompts), word-wrap on by default.
func newStyledTextArea() *styledTextArea {
	ta := &styledTextArea{TextArea: tview.NewTextArea()}
	ta.SetSize(3, 0) // 3 visible rows, auto width
	ta.SetTextStyle(fieldBlurredStyle)
	return ta
}

// SetFormAttributes implements tview.FormItem. It delegates to the embedded
// TextArea then re-applies the correct focused/blurred text style so the cyan
// highlight is visible even after Form.Draw() resets it.
func (ta *styledTextArea) SetFormAttributes(labelWidth int, labelColor, bgColor, fieldTextColor, fieldBgColor tcell.Color) tview.FormItem {
	ta.TextArea.SetFormAttributes(labelWidth, labelColor, bgColor, fieldTextColor, fieldBgColor)
	if ta.HasFocus() {
		ta.SetTextStyle(fieldFocusedStyle)
	} else {
		ta.SetTextStyle(fieldBlurredStyle)
	}
	return ta
}

// submitOnEnter installs an input capture so that pressing Enter while an
// input field is focused activates OK (BUG-006). Without it, tview's Form
// treats Enter like Tab (advance to the next element) and only submits when
// the OK button itself is focused — so labelling a button "OK [enter]" would
// otherwise lie. When focus is on a BUTTON, Enter is left to tview so the
// focused button (OK or Cancel) activates as the base spec requires; this
// capture only short-circuits Enter from a text field. Esc is already wired
// to cancel via form.SetCancelFunc by each caller.
func submitOnEnter(form *tview.Form, onOK func()) {
	form.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		if ev.Key() != tcell.KeyEnter {
			return ev
		}
		// Only treat Enter as submit when a form item (input field) holds
		// focus. If a button is focused, fall through to tview so Enter
		// activates that button (OK *or* Cancel).
		for i := 0; i < form.GetFormItemCount(); i++ {
			if form.GetFormItem(i).HasFocus() {
				onOK()
				return nil
			}
		}
		return ev
	})
}

// enterRoutingCapture returns the Enter-routing input capture shared by the
// new-coordinator and new-worker forms. Both modals mix selector widgets
// (inlineCycler, inlineListSelect) with text fields (styledInputField,
// styledTextArea), and Enter must mean different things per widget:
//
//   - *inlineCycler / *inlineListSelect: Enter must NOT submit — the event is
//     returned so the widget's own InputHandler handles it (it calls
//     finishedFunc(KeyTab) to lock the value and advance focus). This is the
//     BUG-035 guard: a selector must never submit the form while focused.
//   - *styledTextArea: plain Enter submits; modified Enter (Ctrl/Shift) passes
//     through so the TextArea can insert a newline (BUG-011).
//   - any other focused item (e.g. styledInputField): Enter submits.
//
// Non-Enter keys pass through untouched (printable runes reach the focused
// widget for filtering; arrows reach the cycler/list cursor). submit is the
// form's dismiss(true) callback.
func enterRoutingCapture(form *tview.Form, submit func()) func(*tcell.EventKey) *tcell.EventKey {
	return func(ev *tcell.EventKey) *tcell.EventKey {
		if ev.Key() != tcell.KeyEnter {
			return ev
		}
		for i := 0; i < form.GetFormItemCount(); i++ {
			item := form.GetFormItem(i)
			if !item.HasFocus() {
				continue
			}
			switch item.(type) {
			case *inlineCycler:
				return ev // Cycler: Enter handled by its own InputHandler
			case *inlineListSelect:
				// List: Enter handled by its own InputHandler, which calls
				// finishedFunc(KeyTab) to lock + advance focus — not submit.
				return ev
			case *styledTextArea:
				// Plain Enter submits; modified Enter inserts newline in TextArea.
				if ev.Modifiers() == 0 {
					submit()
					return nil
				}
				return ev
			default:
				submit()
				return nil
			}
		}
		return ev
	}
}

// captureFocus records the primitive currently holding tview focus so
// closeModal can restore it after the modal goes. Runs on the event loop
// (queueModal body), immediately before focus moves to the modal.
func (a *App) captureFocus() {
	if a.app != nil {
		a.modalPrevFocus = a.app.GetFocus()
	}
}

// ShowInput opens a single-line input modal centered over the base
// layout. onSubmit fires with the trimmed value when the operator hits
// OK; onCancel fires on Cancel or Esc. Both callbacks run after the
// modal page is removed and focus has been restored.
//
// Safe to call from any goroutine — the body runs through
// app.QueueUpdateDraw so it lands on the tview event loop.
func (a *App) ShowInput(title, label, initial string, onSubmit func(string), onCancel func()) {
	a.queueModal(func() {
		input := newStyledInputField()
		input.SetLabel(label + ": ")
		input.SetText(initial)
		input.SetFieldWidth(formFieldWidth(label))

		form := tview.NewForm().
			AddFormItem(input).
			SetButtonsAlign(tview.AlignCenter)

		dismiss := func(submitted bool) {
			text := strings.TrimSpace(input.GetText())
			a.closeModal(pageInput)
			if submitted {
				if onSubmit != nil {
					onSubmit(text)
				}
			} else if onCancel != nil {
				onCancel()
			}
		}

		form.AddButton("OK [enter]", func() { dismiss(true) })
		form.AddButton("Cancel [esc]", func() { dismiss(false) })
		form.SetCancelFunc(func() { dismiss(false) })
		submitOnEnter(form, func() { dismiss(true) })

		themeFormStyle(form, title)

		a.captureFocus()
		a.pieces.pages.AddPage(pageInput, centeredModal(form, modalWidth, 7), true, true)
		if a.app != nil {
			a.app.SetFocus(input)
		}
	})
}

// ShowForm2 opens a two-field input modal centered over the base layout.
// onSubmit fires with both trimmed values when the operator hits OK; onCancel
// fires on Cancel or Esc. Mirrors ShowInput's idiom (tview.Form), adding a
// second field — used by the new-project flow (name required, prompt
// optional).
//
// Safe to call from any goroutine — the body runs through app.QueueUpdateDraw
// so it lands on the tview event loop.
func (a *App) ShowForm2(title, label1, initial1, label2, initial2 string, onSubmit func(v1, v2 string), onCancel func()) {
	a.queueModal(func() {
		// Both fields share the longest label's reserved width so they align
		// and neither overflows the frame (BUG-002).
		fw := formFieldWidth(label1, label2)
		field1 := newStyledInputField()
		field1.SetLabel(label1 + ": ")
		field1.SetText(initial1)
		field1.SetFieldWidth(fw)
		field2 := newStyledInputField()
		field2.SetLabel(label2 + ": ")
		field2.SetText(initial2)
		field2.SetFieldWidth(fw)

		form := tview.NewForm().
			AddFormItem(field1).
			AddFormItem(field2).
			SetButtonsAlign(tview.AlignCenter)

		dismiss := func(submitted bool) {
			v1 := strings.TrimSpace(field1.GetText())
			v2 := strings.TrimSpace(field2.GetText())
			a.closeModal(pageForm2)
			if submitted {
				if onSubmit != nil {
					onSubmit(v1, v2)
				}
			} else if onCancel != nil {
				onCancel()
			}
		}

		form.AddButton("OK [enter]", func() { dismiss(true) })
		form.AddButton("Cancel [esc]", func() { dismiss(false) })
		form.SetCancelFunc(func() { dismiss(false) })
		submitOnEnter(form, func() { dismiss(true) })

		themeFormStyle(form, title)

		a.captureFocus()
		a.pieces.pages.AddPage(pageForm2, centeredModal(form, modalWidth, 9), true, true)
		if a.app != nil {
			a.app.SetFocus(field1)
		}
	})
}

// inlineCycler is a custom tview.FormItem presenting a set of options as an
// inline single-line selector. It replaces tview.DropDown to avoid the green
// popup overlay and the "Enter submits the form" behaviour (BUG-035).
//
// Display:
//   - focused:  ◄ current (n/m) ►  — cyan-on-title-bg, bold
//   - blurred:   current            — normal style on highlight bg
//
// Key handling:
//   - Left / Up    — previous option (wraps)
//   - Right / Down — next option (wraps)
//   - Enter        — locks current value; advances focus via finishedFunc(KeyTab)
//   - Tab          — advance focus (finishedFunc(KeyTab))
//   - Backtab      — retreat focus (finishedFunc(KeyBacktab))
//   - Esc          — cancel (finishedFunc(KeyEscape))
type inlineCycler struct {
	*tview.Box

	label   string
	options []string
	current int

	// fieldWidth is the declared field width for the form layout.
	fieldWidth int

	// form attributes injected by SetFormAttributes
	labelWidth     int
	labelColor     tcell.Color
	bgColor        tcell.Color
	fieldTextColor tcell.Color
	fieldBgColor   tcell.Color

	disabled     bool
	finishedFunc func(tcell.Key)
}

func newInlineCycler(label string, options []string, initial, fw int) *inlineCycler {
	if initial < 0 || (len(options) > 0 && initial >= len(options)) {
		initial = 0
	}
	return &inlineCycler{
		Box:        tview.NewBox(),
		label:      label,
		options:    options,
		current:    initial,
		fieldWidth: fw,
	}
}

// GetCurrentOption returns the index and text of the currently selected option.
func (c *inlineCycler) GetCurrentOption() (int, string) {
	if len(c.options) == 0 {
		return -1, ""
	}
	return c.current, c.options[c.current]
}

// GetLabel implements tview.FormItem.
func (c *inlineCycler) GetLabel() string { return c.label }

// SetFormAttributes implements tview.FormItem.
func (c *inlineCycler) SetFormAttributes(labelWidth int, labelColor, bgColor, fieldTextColor, fieldBgColor tcell.Color) tview.FormItem {
	c.labelWidth = labelWidth
	c.labelColor = labelColor
	c.bgColor = bgColor
	c.fieldTextColor = fieldTextColor
	c.fieldBgColor = fieldBgColor
	return c
}

// GetFieldWidth implements tview.FormItem.
func (c *inlineCycler) GetFieldWidth() int { return c.fieldWidth }

// GetFieldHeight implements tview.FormItem (single-line widget).
func (c *inlineCycler) GetFieldHeight() int { return 1 }

// SetFinishedFunc implements tview.FormItem.
func (c *inlineCycler) SetFinishedFunc(handler func(key tcell.Key)) tview.FormItem {
	c.finishedFunc = handler
	return c
}

// SetDisabled implements tview.FormItem.
func (c *inlineCycler) SetDisabled(disabled bool) tview.FormItem {
	c.disabled = disabled
	return c
}

// Draw implements tview.Primitive.
func (c *inlineCycler) Draw(screen tcell.Screen) {
	c.DrawForSubclass(screen, c)
	x, y, width, _ := c.GetInnerRect()
	if width <= 0 {
		return
	}

	// Label — right-padded to labelWidth
	labelStyle := tcell.StyleDefault.Foreground(c.labelColor).Background(c.bgColor)
	labelRunes := []rune(c.label)
	for i := 0; i < c.labelWidth; i++ {
		r := ' '
		if i < len(labelRunes) {
			r = labelRunes[i]
		}
		screen.SetContent(x+i, y, r, nil, labelStyle)
	}

	// Field area — remaining width after label
	fieldX := x + c.labelWidth
	fieldW := width - c.labelWidth
	if fieldW <= 0 {
		return
	}

	fStyle := fieldBlurredStyle
	if c.HasFocus() {
		fStyle = fieldFocusedStyle
	}

	for i := 0; i < fieldW; i++ {
		screen.SetContent(fieldX+i, y, ' ', nil, fStyle)
	}

	if len(c.options) == 0 {
		return
	}
	cur := c.options[c.current]
	var text string
	if c.HasFocus() {
		text = fmt.Sprintf("◄ %s (%d/%d) ►", cur, c.current+1, len(c.options))
	} else {
		text = cur
	}
	for i, r := range []rune(text) {
		if i >= fieldW {
			break
		}
		screen.SetContent(fieldX+i, y, r, nil, fStyle)
	}
}

// InputHandler implements tview.Primitive.
func (c *inlineCycler) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return c.WrapInputHandler(func(event *tcell.EventKey, _ func(p tview.Primitive)) {
		if c.disabled || len(c.options) == 0 {
			return
		}
		n := len(c.options)
		switch event.Key() {
		case tcell.KeyLeft, tcell.KeyUp:
			if c.current > 0 {
				c.current--
			} else {
				c.current = n - 1
			}
		case tcell.KeyRight, tcell.KeyDown:
			c.current = (c.current + 1) % n
		case tcell.KeyEnter:
			if c.finishedFunc != nil {
				c.finishedFunc(tcell.KeyTab)
			}
		case tcell.KeyTab:
			if c.finishedFunc != nil {
				c.finishedFunc(tcell.KeyTab)
			}
		case tcell.KeyBacktab:
			if c.finishedFunc != nil {
				c.finishedFunc(tcell.KeyBacktab)
			}
		case tcell.KeyEscape:
			if c.finishedFunc != nil {
				c.finishedFunc(tcell.KeyEscape)
			}
		}
	})
}

// listSelectVisibleRows is the number of option rows the inlineListSelect
// shows at once (its scrollable window). The widget's total field height is
// this plus one row for the inline filter/counter line.
const listSelectVisibleRows = 5

// noProjectsSentinel is the single visible entry shown when the configured
// project list is empty. GetCurrentOption maps it to "" so the ops layer
// falls back to the coordinator's project (preserving the empty-list
// contract the cycler had).
const noProjectsSentinel = "(no projects configured)"

// inlineListSelect is a custom tview.FormItem presenting a set of options as
// a scrollable, type-to-filter list — the same control argus's New Task modal
// uses for project selection. It replaces inlineCycler for the Project field
// while keeping the full tview.FormItem interface, so it drops into the
// existing tview.NewForm() flow unchanged.
//
// It is deliberately NOT a tview.DropDown: DropDown brings a green popup
// overlay, an un-themed background, and "Enter submits while the list is open"
// behaviour (BUG-035). This widget draws its own bounded, scrolling list.
//
// Display:
//   - a one-line filter/counter row: the typed filter string plus an (N/M)
//     counter (cursor position within filtered options / filtered count);
//   - up to listSelectVisibleRows option rows, the highlighted one prefixed
//     with a '>' cursor; the visible window scrolls to keep the cursor in view.
//
// Key handling (only while focused):
//   - Down / Up        — move the cursor within the FILTERED options (clamped,
//     no wrap); does NOT submit;
//   - printable rune   — append to the filter, re-filter (case-insensitive
//     substring over labels), reset cursor to the first match;
//   - Backspace        — delete the last filter rune (empty filter restores
//     the full list);
//   - Enter            — lock current selection; advance focus via
//     finishedFunc(KeyTab) (does NOT submit the form);
//   - Tab              — advance focus (finishedFunc(KeyTab));
//   - Backtab          — retreat focus (finishedFunc(KeyBacktab));
//   - Esc              — cancel (finishedFunc(KeyEscape)).
type inlineListSelect struct {
	*tview.Box

	label   string
	options []string // the full, unfiltered option list (never empty: sentinel filled)

	// filter is the live type-to-filter string; filtered holds the indices
	// into options that currently match (case-insensitive substring). cursor
	// indexes into filtered (the highlighted FILTERED row). top is the index
	// into filtered of the first visible row (scroll window).
	filter   string
	filtered []int
	cursor   int
	top      int

	// fieldWidth is the declared field width for the form layout.
	fieldWidth int

	// form attributes injected by SetFormAttributes
	labelWidth     int
	labelColor     tcell.Color
	bgColor        tcell.Color
	fieldTextColor tcell.Color
	fieldBgColor   tcell.Color

	disabled     bool
	finishedFunc func(tcell.Key)
}

// newInlineListSelect builds a list selector over options. An empty options
// slice degrades to a single noProjectsSentinel entry (mapped to "" on
// confirm). initial selects an option index (clamped) when in range.
func newInlineListSelect(label string, options []string, initial, fw int) *inlineListSelect {
	opts := options
	if len(opts) == 0 {
		opts = []string{noProjectsSentinel}
	}
	if initial < 0 || initial >= len(opts) {
		initial = 0
	}
	ls := &inlineListSelect{
		Box:        tview.NewBox(),
		label:      label,
		options:    opts,
		fieldWidth: fw,
	}
	ls.refilter()
	// Place the cursor on the requested initial option within the (full) list.
	for i, fi := range ls.filtered {
		if fi == initial {
			ls.cursor = i
			break
		}
	}
	ls.scrollToCursor()
	return ls
}

// refilter recomputes ls.filtered from ls.filter (case-insensitive substring
// over option labels). An empty filter matches all options. The cursor is
// reset to the first filtered row and the scroll window re-anchored.
func (ls *inlineListSelect) refilter() {
	needle := strings.ToLower(ls.filter)
	ls.filtered = ls.filtered[:0]
	for i, o := range ls.options {
		if needle == "" || strings.Contains(strings.ToLower(o), needle) {
			ls.filtered = append(ls.filtered, i)
		}
	}
	ls.cursor = 0
	ls.top = 0
}

// scrollToCursor adjusts ls.top so the cursor row is within the visible
// window of listSelectVisibleRows rows.
func (ls *inlineListSelect) scrollToCursor() {
	if ls.cursor < ls.top {
		ls.top = ls.cursor
	}
	if ls.cursor >= ls.top+listSelectVisibleRows {
		ls.top = ls.cursor - listSelectVisibleRows + 1
	}
	if ls.top < 0 {
		ls.top = 0
	}
}

// filteredCounter returns the 1-based cursor position within the filtered set
// and the filtered count — the (N/M) the widget renders. With no matches it
// returns (0, 0).
func (ls *inlineListSelect) filteredCounter() (int, int) {
	m := len(ls.filtered)
	if m == 0 {
		return 0, 0
	}
	return ls.cursor + 1, m
}

// GetCurrentOption returns the cursor position within the currently FILTERED
// options and the highlighted option's label (the (N/M) N is this index + 1).
// The empty-list sentinel maps to (0, "") so callers fall back to the
// coordinator's project. With no current match it returns (-1, "").
func (ls *inlineListSelect) GetCurrentOption() (int, string) {
	if len(ls.filtered) == 0 || ls.cursor < 0 || ls.cursor >= len(ls.filtered) {
		return -1, ""
	}
	label := ls.options[ls.filtered[ls.cursor]]
	if label == noProjectsSentinel {
		return 0, ""
	}
	return ls.cursor, label
}

// GetLabel implements tview.FormItem.
func (ls *inlineListSelect) GetLabel() string { return ls.label }

// SetFormAttributes implements tview.FormItem.
func (ls *inlineListSelect) SetFormAttributes(labelWidth int, labelColor, bgColor, fieldTextColor, fieldBgColor tcell.Color) tview.FormItem {
	ls.labelWidth = labelWidth
	ls.labelColor = labelColor
	ls.bgColor = bgColor
	ls.fieldTextColor = fieldTextColor
	ls.fieldBgColor = fieldBgColor
	return ls
}

// GetFieldWidth implements tview.FormItem.
func (ls *inlineListSelect) GetFieldWidth() int { return ls.fieldWidth }

// GetFieldHeight implements tview.FormItem. The widget occupies a fixed
// multi-row block: one filter/counter row plus listSelectVisibleRows option
// rows.
func (ls *inlineListSelect) GetFieldHeight() int { return 1 + listSelectVisibleRows }

// SetFinishedFunc implements tview.FormItem.
func (ls *inlineListSelect) SetFinishedFunc(handler func(key tcell.Key)) tview.FormItem {
	ls.finishedFunc = handler
	return ls
}

// SetDisabled implements tview.FormItem.
func (ls *inlineListSelect) SetDisabled(disabled bool) tview.FormItem {
	ls.disabled = disabled
	return ls
}

// Draw implements tview.Primitive. Row 0 is the label + filter/counter line;
// the following rows are the scrolling option window with a '>' cursor on the
// highlighted (focused) row.
func (ls *inlineListSelect) Draw(screen tcell.Screen) {
	ls.DrawForSubclass(screen, ls)
	x, y, width, height := ls.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	focused := ls.HasFocus()
	fStyle := fieldBlurredStyle
	if focused {
		fStyle = fieldFocusedStyle
	}

	// --- Row 0: label + filter/counter ---
	labelStyle := tcell.StyleDefault.Foreground(ls.labelColor).Background(ls.bgColor)
	labelRunes := []rune(ls.label)
	for i := 0; i < ls.labelWidth && i < width; i++ {
		r := ' '
		if i < len(labelRunes) {
			r = labelRunes[i]
		}
		screen.SetContent(x+i, y, r, nil, labelStyle)
	}

	fieldX := x + ls.labelWidth
	fieldW := width - ls.labelWidth
	if fieldW <= 0 {
		return
	}
	// Paint the filter row background.
	for i := 0; i < fieldW; i++ {
		screen.SetContent(fieldX+i, y, ' ', nil, fStyle)
	}
	n, m := ls.filteredCounter()
	var header string
	if focused {
		header = fmt.Sprintf("%s (%d/%d)", ls.filter, n, m)
	} else {
		// Blurred: show the current selection, no filter chrome.
		_, cur := ls.GetCurrentOption()
		if cur == "" {
			cur = noProjectsSentinel
		}
		header = cur
	}
	for i, r := range []rune(header) {
		if i >= fieldW {
			break
		}
		screen.SetContent(fieldX+i, y, r, nil, fStyle)
	}

	// --- Rows 1..: option window ---
	rows := height - 1
	if rows > listSelectVisibleRows {
		rows = listSelectVisibleRows
	}
	for r := 0; r < rows; r++ {
		fi := ls.top + r
		ry := y + 1 + r
		// Clear the option row background to the field background.
		rowStyle := fieldBlurredStyle
		for i := 0; i < fieldW; i++ {
			screen.SetContent(fieldX+i, ry, ' ', nil, rowStyle)
		}
		if fi >= len(ls.filtered) {
			continue
		}
		opt := ls.options[ls.filtered[fi]]
		cursorMark := "  "
		oStyle := fieldBlurredStyle
		if fi == ls.cursor {
			cursorMark = "> "
			if focused {
				oStyle = fieldFocusedStyle
			}
		}
		line := cursorMark + opt
		for i, ch := range []rune(line) {
			if i >= fieldW {
				break
			}
			screen.SetContent(fieldX+i, ry, ch, nil, oStyle)
		}
	}
}

// InputHandler implements tview.Primitive.
func (ls *inlineListSelect) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return ls.WrapInputHandler(func(event *tcell.EventKey, _ func(p tview.Primitive)) {
		if ls.disabled {
			return
		}
		switch event.Key() {
		case tcell.KeyDown:
			if ls.cursor < len(ls.filtered)-1 {
				ls.cursor++
				ls.scrollToCursor()
			}
		case tcell.KeyUp:
			if ls.cursor > 0 {
				ls.cursor--
				ls.scrollToCursor()
			}
		case tcell.KeyRune:
			r := event.Rune()
			// Printable runes filter; ignore control/space-only noise but
			// allow space inside a filter (some project names contain it).
			if r == 0 {
				return
			}
			ls.filter += string(r)
			ls.refilter()
		case tcell.KeyBackspace, tcell.KeyBackspace2:
			if ls.filter != "" {
				rs := []rune(ls.filter)
				ls.filter = string(rs[:len(rs)-1])
				ls.refilter()
			}
		case tcell.KeyEnter:
			if ls.finishedFunc != nil {
				ls.finishedFunc(tcell.KeyTab)
			}
		case tcell.KeyTab:
			if ls.finishedFunc != nil {
				ls.finishedFunc(tcell.KeyTab)
			}
		case tcell.KeyBacktab:
			if ls.finishedFunc != nil {
				ls.finishedFunc(tcell.KeyBacktab)
			}
		case tcell.KeyEscape:
			if ls.finishedFunc != nil {
				ls.finishedFunc(tcell.KeyEscape)
			}
		}
	})
}

// ShowNewCoordForm opens the five-field new-coordinator form modal:
// Name (required), Project (inline cycler), Branch (optional, defaults to
// "origin/main"), Backend (inline cycler), Prompt (multi-line textarea).
// onSubmit fires with all field values when the operator confirms with a
// non-empty name; onCancel fires on Esc.
//
// Project and Backend use inlineCycler instead of tview.DropDown to avoid the
// green popup overlay, the no-theme background, and the "Enter submits the
// form while the dropdown list is open" behaviour (BUG-035). Arrow keys cycle;
// Enter on a cycler locks the current value and advances focus to the next
// field — it does NOT submit the form.
//
// projects and backends are the option lists loaded before open (from argus).
// Either list may be empty; defaults are shown when empty.
//
// Safe to call from any goroutine — the body runs through app.QueueUpdateDraw
// so it lands on the tview event loop.
func (a *App) ShowNewCoordForm(title string, projects, backends []string, onSubmit func(NewCoordFormInput), onCancel func()) {
	a.queueModal(func() {
		fw := formFieldWidth("Name", "Project", "Branch", "Backend", "Prompt")

		nameField := newStyledInputField()
		nameField.SetLabel("Name: ")
		nameField.SetFieldWidth(fw)

		projectSelect := newInlineListSelect("Project: ", projects, 0, fw)

		// Branch defaults to "origin/main" — argus uses the project's configured
		// default branch when the field is empty, but pre-filling gives the operator
		// a useful starting point without an extra API call.
		branchField := newStyledInputField()
		branchField.SetLabel("Branch: ")
		branchField.SetText("origin/main")
		branchField.SetFieldWidth(fw)

		backendOptions := backends
		if len(backendOptions) == 0 {
			backendOptions = []string{"claude"}
		}
		backendCycler := newInlineCycler("Backend: ", backendOptions, 0, fw)

		promptField := newStyledTextArea()
		promptField.SetLabel("Prompt: ")

		form := tview.NewForm().
			AddFormItem(nameField).
			AddFormItem(projectSelect).
			AddFormItem(branchField).
			AddFormItem(backendCycler).
			AddFormItem(promptField).
			SetButtonsAlign(tview.AlignCenter)

		dismiss := func(submitted bool) {
			name := strings.TrimSpace(nameField.GetText())
			// inlineListSelect maps its empty-list sentinel to "" itself.
			_, projOpt := projectSelect.GetCurrentOption()
			_, backendOpt := backendCycler.GetCurrentOption()
			a.closeModal(pageNewCoord)
			if submitted && name != "" {
				if onSubmit != nil {
					onSubmit(NewCoordFormInput{
						Name:    name,
						Project: projOpt,
						Branch:  strings.TrimSpace(branchField.GetText()),
						Backend: backendOpt,
						Prompt:  strings.TrimSpace(promptField.GetText()),
					})
				}
			} else if !submitted {
				if onCancel != nil {
					onCancel()
				}
			}
		}

		form.AddButton("Submit [enter]", func() { dismiss(true) })
		form.AddButton("Cancel [esc]", func() { dismiss(false) })
		form.SetCancelFunc(func() { dismiss(false) })

		// Enter routing: selectors (Project list, Backend cycler) pass Enter to
		// their own InputHandler (advance, never submit — BUG-035); the Prompt
		// TextArea submits on plain Enter and inserts a newline on modified Enter
		// (BUG-011); any other focused field submits. Shared with ShowNewWorkerForm.
		form.SetInputCapture(enterRoutingCapture(form, func() { dismiss(true) }))

		themeFormStyle(form, title)

		// Modal height: 3 single-row fields (Name, Branch, Backend), 1 multi-row
		// inlineListSelect (Project), and 1 three-row TextArea (Prompt), with
		// itemPadding=1 between each, plus button row, surrounding padding rows,
		// and 2 border rows. The all-single-row baseline (as for the old cycler
		// Project) was 17; the Project field grew from a 1-row cycler to a
		// (1 + listSelectVisibleRows)-row list, adding listSelectVisibleRows rows.
		const newCoordModalHeight = 17 + listSelectVisibleRows

		a.captureFocus()
		a.pieces.pages.AddPage(pageNewCoord, centeredModal(form, modalWidth, newCoordModalHeight), true, true)
		if a.app != nil {
			a.app.SetFocus(nameField)
		}
	})
}

// ShowNewWorkerForm opens the four-field new-worker form modal, at field-and-
// behaviour parity with the new-coordinator form and argus's New Task modal:
// Project (scrollable type-to-filter list), Branch (single-line input,
// defaulting empty), Backend (inline cycler), and Prompt (multi-line textarea).
// onSubmit fires with (project, branch, backend, prompt) when the operator
// confirms; onCancel fires on Esc.
//
// projects is the list loaded before open (from argus). defaultProjectIdx is
// the index of the coordinator's own project within that list; it initializes
// the Project list cursor. When projects is empty the list shows a single
// "(no projects configured)" entry and submit maps that sentinel to "" (the
// ops layer falls back to the coordinator's project).
//
// backends is the configured backend list loaded before open; defaultBackendIdx
// initializes the Backend cycler. An empty Backend selection means "project
// default" downstream. The Branch field defaults empty: an empty Branch
// branches the worker off the effective project's default ref.
//
// Enter on the Project list or the Backend cycler advances focus (does NOT
// submit — each widget's own InputHandler calls finishedFunc(KeyTab)). Plain
// Enter on the Prompt textarea submits; Shift/Ctrl+Enter inserts a newline
// (BUG-011). Typing while the Project list is focused filters it; a paste
// reaching a focused text field lands there as one chunk (the list consumes
// input only while IT holds focus).
//
// Safe to call from any goroutine — the body runs through app.QueueUpdateDraw
// so it lands on the tview event loop.
func (a *App) ShowNewWorkerForm(title string, projects []string, defaultProjectIdx int, backends []string, defaultBackendIdx int, onSubmit func(project, branch, backend, prompt string), onCancel func()) {
	a.queueModal(func() {
		fw := formFieldWidth("Project", "Branch", "Backend", "Prompt")

		projectSelect := newInlineListSelect("Project: ", projects, defaultProjectIdx, fw)

		// Branch defaults EMPTY — an empty Branch uses the effective project's
		// default ref (delta: "Empty Branch branches off the project default ref").
		branchField := newStyledInputField()
		branchField.SetLabel("Branch: ")
		branchField.SetFieldWidth(fw)

		backendOptions := backends
		if len(backendOptions) == 0 {
			backendOptions = []string{"claude"}
		}
		backendCycler := newInlineCycler("Backend: ", backendOptions, defaultBackendIdx, fw)

		promptField := newStyledTextArea()
		promptField.SetLabel("Prompt: ")

		form := tview.NewForm().
			AddFormItem(projectSelect).
			AddFormItem(branchField).
			AddFormItem(backendCycler).
			AddFormItem(promptField).
			SetButtonsAlign(tview.AlignCenter)

		dismiss := func(submitted bool) {
			// inlineListSelect maps its empty-list sentinel to "" itself.
			_, projOpt := projectSelect.GetCurrentOption()
			_, backendOpt := backendCycler.GetCurrentOption()
			branch := strings.TrimSpace(branchField.GetText())
			prompt := strings.TrimSpace(promptField.GetText())
			a.closeModal(pageNewWorker)
			if submitted {
				if onSubmit != nil {
					onSubmit(projOpt, branch, backendOpt, prompt)
				}
			} else if onCancel != nil {
				onCancel()
			}
		}

		form.AddButton("Submit [enter]", func() { dismiss(true) })
		form.AddButton("Cancel [esc]", func() { dismiss(false) })
		form.SetCancelFunc(func() { dismiss(false) })

		// Enter routing: selectors (Project list, Backend cycler) pass Enter to
		// their own InputHandler (advance, never submit — BUG-035); the Prompt
		// TextArea submits on plain Enter and inserts a newline on modified Enter
		// (BUG-011); any other focused field submits. Shared with ShowNewCoordForm.
		form.SetInputCapture(enterRoutingCapture(form, func() { dismiss(true) }))

		themeFormStyle(form, title)

		// Modal height: 2 single-row fields (Branch, Backend), 1 multi-row
		// inlineListSelect (Project), and 1 three-row TextArea (Prompt), with
		// itemPadding=1 between each, plus button row, surrounding padding rows,
		// and 2 border rows. The old two-field (cycler Project + TextArea) modal
		// was 11; adding the Branch + Backend single-row fields adds 2*2 = 4 rows,
		// and the Project field grew from a 1-row cycler to a
		// (1 + listSelectVisibleRows)-row list, adding listSelectVisibleRows rows.
		const newWorkerModalHeight = 11 + 4 + listSelectVisibleRows

		a.captureFocus()
		a.pieces.pages.AddPage(pageNewWorker, centeredModal(form, modalWidth, newWorkerModalHeight), true, true)
		if a.app != nil {
			a.app.SetFocus(projectSelect)
		}
	})
}

// ShowConfirm opens a y/N confirmation modal. onYes runs on Yes; onNo runs
// on No / Cancel / Esc. Honoring the "(y/N)" convention in every confirm
// message, No is the default-focused button (a bare Enter declines) and the
// bare `y` / `n` runes decide directly.
func (a *App) ShowConfirm(title, message string, onYes func(), onNo func()) {
	a.queueModal(func() {
		decide := func(yes bool) {
			a.closeModal(pageConfirm)
			if yes {
				if onYes != nil {
					onYes()
				}
			} else if onNo != nil {
				onNo()
			}
		}

		modal := tview.NewModal().
			SetText(message).
			AddButtons([]string{"Yes", "No"}).
			SetDoneFunc(func(_ int, label string) { decide(label == "Yes") })
		themeModalStyle(modal, title, theme.ColorNormal)
		// Default to No per the (y/N) convention — Enter must not fire the
		// destructive path.
		modal.SetFocus(1)
		modal.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
			if ev.Key() == tcell.KeyRune {
				switch ev.Rune() {
				case 'y', 'Y':
					decide(true)
					return nil
				case 'n', 'N':
					decide(false)
					return nil
				}
			}
			return ev
		})

		a.captureFocus()
		a.pieces.pages.AddPage(pageConfirm, modal, true, true)
		if a.app != nil {
			a.app.SetFocus(modal)
		}
	})
}

// ShowSelect opens a single-choice picker listing items in a themed,
// focusable, dismissable modal. onSelect fires with the chosen 0-based index
// when the operator confirms a row (Enter or click); onCancel fires on Esc.
// Both callbacks run after the modal page is removed and focus is restored.
// Backs the `J` adopt orchestrator picker.
//
// Safe to call from any goroutine — the body runs through app.QueueUpdateDraw
// so it lands on the tview event loop.
func (a *App) ShowSelect(title, label string, items []string, onSelect func(idx int), onCancel func()) {
	a.queueModal(func() {
		list := tview.NewList().
			ShowSecondaryText(false).
			SetWrapAround(true).
			SetHighlightFullLine(true)

		done := false
		finish := func(submitted bool, idx int) {
			if done {
				return
			}
			done = true
			a.closeModal(pageSelect)
			if submitted {
				if onSelect != nil {
					onSelect(idx)
				}
			} else if onCancel != nil {
				onCancel()
			}
		}

		for i, it := range items {
			idx := i
			list.AddItem(it, "", 0, func() { finish(true, idx) })
		}
		// Esc cancels without a selection.
		list.SetDoneFunc(func() { finish(false, -1) })

		// Theme the list to match the other modals. Use the title colour
		// (cyan) as the selection background for high contrast — dark gray
		// (ColorHighlight) was too close to the modal background (BUG-023 S6).
		list.SetMainTextColor(theme.ColorNormal).
			SetSelectedTextColor(tcell.ColorBlack).
			SetSelectedBackgroundColor(theme.ColorTitle)
		list.SetBackgroundColor(heraBackground)
		list.SetBorder(true)
		list.SetBorderColor(theme.ColorTitle)
		list.SetTitleColor(theme.ColorTitle)
		if label != "" {
			list.SetTitle(" " + title + " — " + label + " ").SetTitleAlign(tview.AlignCenter)
		} else {
			list.SetTitle(" " + title + " ").SetTitleAlign(tview.AlignCenter)
		}

		// Height: one row per item plus borders, capped so a long list stays
		// on screen (the list scrolls internally past the cap).
		height := len(items) + 2
		if height > 14 {
			height = 14
		}
		a.captureFocus()
		a.pieces.pages.AddPage(pageSelect, centeredModal(list, 60, height), true, true)
		if a.app != nil {
			a.app.SetFocus(list)
		}
	})
}

// ShowError surfaces an error string in a modal dismissed by Enter (OK)
// or Esc.
func (a *App) ShowError(message string) {
	a.queueModal(func() {
		modal := tview.NewModal().
			SetText(message).
			AddButtons([]string{"OK"}).
			SetDoneFunc(func(_ int, _ string) { a.closeModal(pageError) })
		themeModalStyle(modal, "Error", theme.ColorError)

		a.captureFocus()
		a.pieces.pages.AddPage(pageError, modal, true, true)
		if a.app != nil {
			a.app.SetFocus(modal)
		}
	})
}

// IsModalActive reports whether any modal page is currently visible.
// The KeyRouter consults this so global focus-traversal and mutation
// keys yield to modal forms instead of swallowing keystrokes; the
// OnFocusChanged border repaint consults it so background refreshes
// never steal tview focus from an open modal.
func (a *App) IsModalActive() bool {
	if a.pieces.pages == nil {
		return false
	}
	name, _ := a.pieces.pages.GetFrontPage()
	return name != "" && name != pageBase
}

// closeModal removes a single modal page and restores tview focus to the
// primitive that held it when the modal opened (falling back to the
// rail). Idempotent — removing an absent page is a no-op.
func (a *App) closeModal(name string) {
	a.pieces.pages.RemovePage(name)
	target := a.modalPrevFocus
	a.modalPrevFocus = nil
	if target == nil {
		target = tview.Primitive(a.pieces.rail)
	}
	if a.app != nil {
		a.app.SetFocus(target)
	}
}

// queueModal runs fn on the tview event loop via QueueUpdateDraw.
//
// CONTRACT: callers MUST be off the event loop. tview v0.42's QueueUpdate
// BLOCKS until the queued func has executed, so a call from the loop itself
// (e.g. a key handler or a modal button callback) deadlocks the application
// permanently. The mutation bridge guarantees this by opening every modal
// from a background goroutine (goUI / mutate); from such a goroutine the
// call merely blocks that goroutine until the loop services it.
//
// modalSync (tests only) skips the bounce: the event loop is not running
// in unit tests, so QueueUpdate would block forever.
func (a *App) queueModal(fn func()) {
	if a.app == nil || a.modalSync {
		fn()
		return
	}
	a.app.QueueUpdateDraw(fn)
}

// centeredModal wraps p in a centered overlay sized (width, height).
// The surrounding flex captures the remaining space, leaving the base
// page visible behind the modal.
func centeredModal(p tview.Primitive, width, height int) tview.Primitive {
	inner := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(nil, 0, 1, false).
		AddItem(p, height, 1, true).
		AddItem(nil, 0, 1, false)
	return tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(inner, width, 1, true).
		AddItem(nil, 0, 1, false)
}
