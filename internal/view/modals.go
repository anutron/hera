package view

import (
	"strings"

	"github.com/rivo/tview"
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
)

// ShowInput opens a single-line input modal centered over the base
// layout. onSubmit fires with the trimmed value when the operator hits
// OK; onCancel fires on Cancel or Esc. Both callbacks run after the
// modal page is removed and focus has been returned to the rail.
//
// Safe to call from any goroutine — the body runs through
// app.QueueUpdateDraw so it lands on the tview event loop.
func (a *App) ShowInput(title, label, initial string, onSubmit func(string), onCancel func()) {
	a.queueModal(func() {
		input := tview.NewInputField().
			SetLabel(label + ": ").
			SetText(initial).
			SetFieldWidth(40)

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

		form.AddButton("OK", func() { dismiss(true) })
		form.AddButton("Cancel", func() { dismiss(false) })
		form.SetCancelFunc(func() { dismiss(false) })

		form.SetBorder(true).SetTitle(title).SetTitleAlign(tview.AlignCenter)

		a.pieces.pages.AddPage(pageInput, centeredModal(form, 60, 7), true, true)
		a.app.SetFocus(input)
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
		field1 := tview.NewInputField().
			SetLabel(label1 + ": ").
			SetText(initial1).
			SetFieldWidth(40)
		field2 := tview.NewInputField().
			SetLabel(label2 + ": ").
			SetText(initial2).
			SetFieldWidth(40)

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

		form.AddButton("OK", func() { dismiss(true) })
		form.AddButton("Cancel", func() { dismiss(false) })
		form.SetCancelFunc(func() { dismiss(false) })

		form.SetBorder(true).SetTitle(title).SetTitleAlign(tview.AlignCenter)

		a.pieces.pages.AddPage(pageForm2, centeredModal(form, 60, 9), true, true)
		a.app.SetFocus(field1)
	})
}

// ShowConfirm opens a y/N confirmation modal. onYes runs on Yes;
// onNo runs on No / Cancel / Esc.
func (a *App) ShowConfirm(title, message string, onYes func(), onNo func()) {
	a.queueModal(func() {
		modal := tview.NewModal().
			SetText(message).
			AddButtons([]string{"Yes", "No"}).
			SetDoneFunc(func(_ int, label string) {
				a.closeModal(pageConfirm)
				if label == "Yes" {
					if onYes != nil {
						onYes()
					}
				} else if onNo != nil {
					onNo()
				}
			})
		if title != "" {
			modal.Box.SetTitle(title).SetTitleAlign(tview.AlignCenter)
		}

		a.pieces.pages.AddPage(pageConfirm, modal, true, true)
		a.app.SetFocus(modal)
	})
}

// ShowError surfaces an error string in a modal dismissed by any key.
func (a *App) ShowError(message string) {
	a.queueModal(func() {
		modal := tview.NewModal().
			SetText(message).
			AddButtons([]string{"OK"}).
			SetDoneFunc(func(_ int, _ string) { a.closeModal(pageError) })
		modal.Box.SetTitle("Error").SetTitleAlign(tview.AlignCenter)

		a.pieces.pages.AddPage(pageError, modal, true, true)
		a.app.SetFocus(modal)
	})
}

// IsModalActive reports whether any modal page is currently visible.
// The KeyRouter consults this so global focus-traversal and mutation
// keys yield to modal forms instead of swallowing keystrokes.
func (a *App) IsModalActive() bool {
	if a.pieces.pages == nil {
		return false
	}
	name, _ := a.pieces.pages.GetFrontPage()
	return name != "" && name != pageBase
}

// closeModal removes a single modal page and restores focus to the
// rail. Idempotent — removing an absent page is a no-op.
func (a *App) closeModal(name string) {
	a.pieces.pages.RemovePage(name)
	a.app.SetFocus(a.pieces.rail)
}

// queueModal runs fn on the tview event loop. Called paths come both
// from the input pump (already on the loop) and from background
// goroutines completing background ops; QueueUpdateDraw is safe in
// both cases.
func (a *App) queueModal(fn func()) {
	if a.app == nil {
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

