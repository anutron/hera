package view

import (
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/anutron/argus-sdk/theme"
)

// Modal page names used inside *App.pieces.pages. A separate name per
// modal kind so close logic can remove a specific page without
// guessing.
const (
	pageBase    = "base"
	pageInput   = "modal-input"
	pageForm2   = "modal-form2"
	pageConfirm = "modal-confirm"
	pageError   = "modal-error"
	pageSelect  = "modal-select"
)

// modalWidth is the outer width every centered form modal is sized to (see
// centeredModal). Field widths are derived from it so inputs stay inside the
// frame.
const modalWidth = 60

// formFieldWidth returns the input field width that keeps a labeled field
// fully inside a modalWidth-wide form. tview lays a vertical form's field out
// at x = innerLeft + labelWidth and draws the input's text area
// labelWidth+fieldWidth wide; with no clamp the box runs past the right
// border (BUG-002). The inner content width is modalWidth minus the form's
// two border columns; reserving the longest label (plus tview's one-space
// label gap) leaves the width an input may safely occupy. labels are the
// raw label strings WITHOUT the trailing ": " that callers append.
func formFieldWidth(labels ...string) int {
	const borderCols = 2 // left+right form border
	const labelSuffix = 2 // ": " appended to every label below
	inner := modalWidth - borderCols
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
		m.Box.SetTitle(title).SetTitleAlign(tview.AlignCenter)
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
	form.SetTitle(title).SetTitleAlign(tview.AlignCenter)
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

// wireFieldFocusStyle gives an input field a visible focus indicator by
// swapping its field style on focus/blur. Returns the field for chaining.
func wireFieldFocusStyle(in *tview.InputField) *tview.InputField {
	in.SetFieldStyle(fieldBlurredStyle)
	in.SetFocusFunc(func() { in.SetFieldStyle(fieldFocusedStyle) })
	in.SetBlurFunc(func() { in.SetFieldStyle(fieldBlurredStyle) })
	return in
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
		input := tview.NewInputField().
			SetLabel(label + ": ").
			SetText(initial).
			SetFieldWidth(formFieldWidth(label))
		wireFieldFocusStyle(input)

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
// second field — used by the new-project flow (name required, mission
// optional).
//
// Safe to call from any goroutine — the body runs through app.QueueUpdateDraw
// so it lands on the tview event loop.
func (a *App) ShowForm2(title, label1, initial1, label2, initial2 string, onSubmit func(v1, v2 string), onCancel func()) {
	a.queueModal(func() {
		// Both fields share the longest label's reserved width so they align
		// and neither overflows the frame (BUG-002).
		fw := formFieldWidth(label1, label2)
		field1 := tview.NewInputField().
			SetLabel(label1 + ": ").
			SetText(initial1).
			SetFieldWidth(fw)
		field2 := tview.NewInputField().
			SetLabel(label2 + ": ").
			SetText(initial2).
			SetFieldWidth(fw)
		wireFieldFocusStyle(field1)
		wireFieldFocusStyle(field2)

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

		// Theme the list to match the other modals.
		list.SetMainTextColor(theme.ColorNormal).
			SetSelectedTextColor(tcell.ColorBlack).
			SetSelectedBackgroundColor(theme.ColorHighlight)
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
