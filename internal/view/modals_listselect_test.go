package view

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// --- inlineListSelect widget unit tests (stage 1.1) ---
//
// These exercise the widget in isolation (no form, no App) so the list's
// filter/cursor/finishedFunc contract is proven independently of the modal
// machinery it later drops into.

// newTestListSelect builds an inlineListSelect with a recording finishedFunc
// so tests can assert which tcell.Key the widget signalled (Tab on
// Enter/Tab, Backtab on Backtab, Escape on Esc).
func newTestListSelect(t *testing.T, options []string) (*inlineListSelect, *tcell.Key) {
	t.Helper()
	ls := newInlineListSelect("Project: ", options, 0, 40)
	var last tcell.Key = -1
	ls.SetFinishedFunc(func(k tcell.Key) { last = k })
	return ls, &last
}

// feed drives a key event through the widget's InputHandler the way tview's
// event loop does, with a no-op focus delegate. The widget must hold focus
// for WrapInputHandler to dispatch, so feed focuses it first.
func feed(ls *inlineListSelect, ev *tcell.EventKey) {
	ls.Focus(func(tview.Primitive) {})
	if h := ls.InputHandler(); h != nil {
		h(ev, func(tview.Primitive) {})
	}
}

// feedKey is feed for a bare key with no rune/modifiers.
func feedKey(ls *inlineListSelect, k tcell.Key) {
	feed(ls, tcell.NewEventKey(k, 0, tcell.ModNone))
}

func TestInlineListSelect_DownUpMoveCursorClamped(t *testing.T) {
	ls, last := newTestListSelect(t, []string{"alpha", "beta", "gamma"})

	if idx, opt := ls.GetCurrentOption(); idx != 0 || opt != "alpha" {
		t.Fatalf("initial: want (0, alpha), got (%d, %q)", idx, opt)
	}

	feedKey(ls, tcell.KeyDown)
	if idx, opt := ls.GetCurrentOption(); idx != 1 || opt != "beta" {
		t.Fatalf("after Down: want (1, beta), got (%d, %q)", idx, opt)
	}
	feedKey(ls, tcell.KeyDown)
	if idx, _ := ls.GetCurrentOption(); idx != 2 {
		t.Fatalf("after 2nd Down: want 2, got %d", idx)
	}
	// Clamp at the bottom (no wrap).
	feedKey(ls, tcell.KeyDown)
	if idx, _ := ls.GetCurrentOption(); idx != 2 {
		t.Fatalf("Down at last must clamp at 2, got %d", idx)
	}

	feedKey(ls, tcell.KeyUp)
	feedKey(ls, tcell.KeyUp)
	if idx, _ := ls.GetCurrentOption(); idx != 0 {
		t.Fatalf("after 2 Up: want 0, got %d", idx)
	}
	// Clamp at the top (no wrap).
	feedKey(ls, tcell.KeyUp)
	if idx, _ := ls.GetCurrentOption(); idx != 0 {
		t.Fatalf("Up at first must clamp at 0, got %d", idx)
	}

	// Down/Up must NOT have signalled finished (no submit, no focus advance).
	if *last != -1 {
		t.Fatalf("Down/Up must not call finishedFunc; got key %v", *last)
	}
}

func TestInlineListSelect_TypingFiltersCaseInsensitiveSubstring(t *testing.T) {
	ls, _ := newTestListSelect(t, []string{"foo-frontend", "foo-backend", "bar-api"})

	for _, r := range "BACK" {
		feed(ls, tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}

	idx, opt := ls.GetCurrentOption()
	if opt != "foo-backend" {
		t.Fatalf("filter 'BACK' must narrow to foo-backend; got %q", opt)
	}
	if idx != 0 {
		t.Fatalf("cursor must rest on first filtered option (0); got %d", idx)
	}
	if n, m := ls.filteredCounter(); n != 1 || m != 1 {
		t.Fatalf("(N/M) must reflect 1/1 filtered; got %d/%d", n, m)
	}
}

func TestInlineListSelect_FilterResetsCursorToFirstMatch(t *testing.T) {
	ls, _ := newTestListSelect(t, []string{"alpha", "beta", "gamma"})

	feedKey(ls, tcell.KeyDown)
	feedKey(ls, tcell.KeyDown) // cursor on "gamma" (idx 2)

	// Typing 'a' matches all three (alpha, beta, gamma — all contain 'a');
	// the cursor must snap to the first filtered row.
	feed(ls, tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModNone))
	if idx, _ := ls.GetCurrentOption(); idx != 0 {
		t.Fatalf("filter change must reset cursor to first match (0); got %d", idx)
	}
}

func TestInlineListSelect_BackspaceEditsFilterAndClearingRestoresFullList(t *testing.T) {
	ls, _ := newTestListSelect(t, []string{"foo-frontend", "foo-backend", "bar-api"})

	for _, r := range "back" {
		feed(ls, tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}
	if _, m := ls.filteredCounter(); m != 1 {
		t.Fatalf("after 'back' filter must be 1; got %d", m)
	}

	// Delete one rune ("bac") — still 1 match (foo-backend).
	feed(ls, tcell.NewEventKey(tcell.KeyBackspace2, 0, tcell.ModNone))
	if _, m := ls.filteredCounter(); m != 1 {
		t.Fatalf("after deleting one rune ('bac') still 1 match; got %d", m)
	}

	// Delete the rest — empty filter restores the full list.
	for i := 0; i < 3; i++ {
		feed(ls, tcell.NewEventKey(tcell.KeyBackspace2, 0, tcell.ModNone))
	}
	if _, m := ls.filteredCounter(); m != 3 {
		t.Fatalf("clearing filter must restore full list (3); got %d", m)
	}
}

func TestInlineListSelect_EnterCallsFinishedTab(t *testing.T) {
	ls, last := newTestListSelect(t, []string{"alpha", "beta"})
	feedKey(ls, tcell.KeyEnter)
	if *last != tcell.KeyTab {
		t.Fatalf("Enter must call finishedFunc(KeyTab); got %v", *last)
	}
}

func TestInlineListSelect_TabAndBacktabCallFinished(t *testing.T) {
	ls, last := newTestListSelect(t, []string{"alpha", "beta"})
	feedKey(ls, tcell.KeyTab)
	if *last != tcell.KeyTab {
		t.Fatalf("Tab must call finishedFunc(KeyTab); got %v", *last)
	}
	feedKey(ls, tcell.KeyBacktab)
	if *last != tcell.KeyBacktab {
		t.Fatalf("Backtab must call finishedFunc(KeyBacktab); got %v", *last)
	}
}

func TestInlineListSelect_EscCallsFinishedEscape(t *testing.T) {
	ls, last := newTestListSelect(t, []string{"alpha", "beta"})
	feedKey(ls, tcell.KeyEscape)
	if *last != tcell.KeyEscape {
		t.Fatalf("Esc must call finishedFunc(KeyEscape); got %v", *last)
	}
}

func TestInlineListSelect_EmptyOptionsSentinelMapsToEmpty(t *testing.T) {
	ls, _ := newTestListSelect(t, nil)
	idx, opt := ls.GetCurrentOption()
	if idx != 0 || opt != "" {
		t.Fatalf("empty options: GetCurrentOption must map the sentinel to (0, \"\"); got (%d, %q)", idx, opt)
	}
	// The widget still has one visible row to render.
	if _, m := ls.filteredCounter(); m != 1 {
		t.Fatalf("empty options must show a single sentinel row; got %d", m)
	}
}

func TestInlineListSelect_GetFieldHeightMultiRow(t *testing.T) {
	ls, _ := newTestListSelect(t, []string{"a", "b", "c", "d", "e", "f", "g", "h"})
	if h := ls.GetFieldHeight(); h <= 1 {
		t.Fatalf("GetFieldHeight must be multi-row (filter row + bounded list); got %d", h)
	}
}

// TestInlineListSelect_ZeroMatchesGetCurrentOptionEmpty locks the no-match
// contract: a filter matching no option leaves the list with zero filtered
// rows, and GetCurrentOption returns (-1, "") — the same "" a caller maps to
// the coordinator's project fallback (documented empty/no-match → fallback).
func TestInlineListSelect_ZeroMatchesGetCurrentOptionEmpty(t *testing.T) {
	ls, _ := newTestListSelect(t, []string{"alpha", "beta", "gamma"})

	for _, r := range "zzz-no-such-project" {
		feed(ls, tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}

	if _, m := ls.filteredCounter(); m != 0 {
		t.Fatalf("a no-match filter must leave zero filtered options; got %d", m)
	}
	idx, opt := ls.GetCurrentOption()
	if idx != -1 || opt != "" {
		t.Fatalf("no-match GetCurrentOption must be (-1, \"\"); got (%d, %q)", idx, opt)
	}
}

// TestInlineListSelect_RendersCursorAndCounter draws the widget onto a
// SimulationScreen and asserts the '>' cursor glyph and the (N/M) counter
// appear when focused.
func TestInlineListSelect_RendersCursorAndCounter(t *testing.T) {
	ls := newInlineListSelect("Project: ", []string{"alpha", "beta", "gamma"}, 0, 40)
	ls.SetFormAttributes(9, tcell.ColorWhite, tcell.ColorBlack, tcell.ColorWhite, tcell.ColorBlack)

	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	defer sim.Fini()
	sim.SetSize(60, 12)
	ls.SetRect(0, 0, 50, ls.GetFieldHeight())
	// Focus the widget so the cursor + counter render.
	ls.Focus(func(tview.Primitive) {})
	ls.Draw(sim)
	sim.Show()

	cells, _, _ := sim.GetContents()
	var sb strings.Builder
	for _, c := range cells {
		for _, r := range c.Runes {
			sb.WriteRune(r)
		}
	}
	rendered := sb.String()
	if !strings.Contains(rendered, ">") {
		t.Fatalf("focused list must render a '>' cursor; rendered=%q", rendered)
	}
	if !strings.Contains(rendered, "(1/3)") {
		t.Fatalf("focused list must render an (N/M) counter '(1/3)'; rendered=%q", rendered)
	}
}
