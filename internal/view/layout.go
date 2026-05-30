package view

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// RailWidth is the fixed character width of the navigation rail column.
// See design.md D7.
const RailWidth = 36

// layoutPieces holds references to the individual chrome and body
// primitives composed by buildLayout. The app keeps these around to
// drive updates (rail refresh, pane swap) without recomposing the Flex
// each time.
type layoutPieces struct {
	root   *tview.Flex
	topBar *tview.TextView
	rail   *railList
	coord  *pinnedTerminalPane
	agent  *pinnedTerminalPane
	bottom *tview.TextView
	body   *tview.Flex
	// pages wraps the root layout so the mutation bridge can overlay
	// input / confirm / help / error modals via AddPage. The base
	// layout lives at page "base" (visible by default); each modal is
	// added/removed by name.
	pages *tview.Pages
}

// buildLayout composes the tview Flex tree described in design.md D7:
//
//	Flex (rows):
//	  TopBar       (height 1, "HERA" left-aligned)
//	  Body  (cols):
//	    Rail       (width RailWidth)
//	    CoordPane  (flex 1)
//	    AgentPane  (flex 1)
//	  BottomBar    (height 1, key hints)
//
// Coord and agent panes split the remaining horizontal space evenly.
// The TopBar contains the literal text "HERA"; the BottomBar carries
// focus-state-aware hints (placeholder text in Stage F; Stage G/H wires
// real focus state). The two terminalpanes own their own border and
// title rendering, so this layer only wires titles and arranges them in
// the Flex.
func buildLayout(coord, agent *pinnedTerminalPane) layoutPieces {
	top := tview.NewTextView()
	top.SetText("HERA")
	top.SetTextAlign(tview.AlignLeft)
	top.SetTextColor(tcell.ColorWhite)

	rail := newRailList()

	coord.SetTitle("Coord")
	agent.SetTitle("Agent")

	body := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(rail, RailWidth, 0, false).
		AddItem(coord, 0, 1, false).
		AddItem(agent, 0, 1, false)

	bottom := tview.NewTextView()
	bottom.SetText(defaultBottomBarText())
	bottom.SetTextAlign(tview.AlignLeft)
	bottom.SetTextColor(tcell.ColorWhite)

	root := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(top, 1, 0, false).
		AddItem(body, 0, 1, false).
		AddItem(bottom, 1, 0, false)

	pages := tview.NewPages().
		AddPage(pageBase, root, true, true)

	return layoutPieces{
		root:   root,
		topBar: top,
		rail:   rail,
		coord:  coord,
		agent:  agent,
		bottom: bottom,
		body:   body,
		pages:  pages,
	}
}

// bottomBarText returns the focus-state-aware bottom-bar hints. The bracketed
// label ([RAIL]/[COORD]/[AGENT]) is the operator's primary cue for which
// element currently holds focus (alongside the colored border); without it,
// advancing focus into a pane looks like nothing happened and rail navigation
// appears frozen because j/k are forwarded to the focused pane's PTY.
// coordPresent controls whether the bar advertises the COORD pane. In
// freelance mode (D11) there is no coord, so the hints drop the coord step:
// RAIL advances straight to AGENT and AGENT retreats straight to RAIL.
func bottomBarText(state FocusState, coordPresent bool) string {
	switch state {
	case FocusCOORD:
		return "[COORD] keys → coord PTY   Ctrl-→ agent   Ctrl-← rail   ^Q rail"
	case FocusAGENT:
		if !coordPresent {
			return "[AGENT] keys → agent PTY   Ctrl-← rail   ^Q rail"
		}
		return "[AGENT] keys → agent PTY   Ctrl-← coord   ^Q rail"
	default:
		if !coordPresent {
			return "[RAIL] j/k move  Enter agent  Ctrl-→ agent  n new  r rename  ^d del  a archive  l listall  ? help"
		}
		return "[RAIL] j/k move  Enter agent  Ctrl-→ coord  n new  r rename  ^d del  a archive  l listall  ? help"
	}
}

// defaultBottomBarText is the initial (RAIL-focus) bar shown by buildLayout
// before the first focus change. The initial layout always has a coord pane.
func defaultBottomBarText() string {
	return bottomBarText(FocusRAIL, true)
}
